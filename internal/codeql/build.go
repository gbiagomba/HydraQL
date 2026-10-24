package codeql

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gbiagomba/hydraql/v2/internal/report"
)

// nonLangDirs are the bookkeeping directories CodeQL writes inside a database
// cluster. They are never language databases, so they are never finalized and
// never treated as a scan target.
var nonLangDirs = map[string]bool{
	"diagnostic": true,
	"log":        true,
	"logs":       true,
	"results":    true,
	"working":    true,
	"temp":       true,
}

var unsafeNameChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// DefaultExcludes are build-root children that are never source projects worth
// a database: dependency trees, build output, and report directories. Each
// entry is a case-insensitive glob matched against the directory name.
var DefaultExcludes = []string{
	"node_modules", "vendor", "dist", "build", "out", "target",
	"reports", "report", "coverage", "tmp", "temp",
}

// CreateOptions carries the `codeql database create` settings that control how
// the source tree is built. It is the single owner of that flag translation,
// shared by the cluster build phase and the single-language --auto-init-db path.
type CreateOptions struct {
	BuildMode     string   // none | autobuild | manual; empty lets CodeQL decide
	BuildCommands []string // apply to every language

	// PerLang holds commands bound to a single language, keyed by the aliased
	// language name. CodeQL runs one build command per `database create`, so a
	// tree needing different builds per language (an Android build for java, an
	// iOS build for swift) cannot be expressed in a single cluster invocation.
	PerLang map[string][]string
}

// CommandsFor returns the build commands that apply to one language: its own if
// any were bound to it, otherwise the global ones.
func (o CreateOptions) CommandsFor(lang string) []string {
	if cmds, ok := o.PerLang[LangAlias(lang)]; ok {
		return cmds
	}
	return o.BuildCommands
}

// HasPerLang reports whether any command is bound to a specific language.
func (o CreateOptions) HasPerLang() bool { return len(o.PerLang) > 0 }

// ParseBuildCommands splits raw --build-command values into global commands and
// per-language ones.
//
// A value counts as per-language only when the text before the first "=" is a
// known language name and contains no whitespace. That keeps a global command
// such as `make CC=gcc` global, since "make CC" has a space and is not a
// language.
func ParseBuildCommands(raw []string) (global []string, perLang map[string][]string) {
	perLang = map[string][]string{}
	for _, entry := range raw {
		if prefix, cmd, found := strings.Cut(entry, "="); found {
			candidate := strings.TrimSpace(prefix)
			if candidate != "" && !strings.ContainsAny(candidate, " \t") && IsKnownLanguage(candidate) {
				lang := LangAlias(candidate)
				perLang[lang] = append(perLang[lang], cmd)
				continue
			}
		}
		global = append(global, entry)
	}
	if len(perLang) == 0 {
		perLang = nil
	}
	return global, perLang
}

// ValidBuildModes are the modes `codeql database create --build-mode` accepts.
var ValidBuildModes = []string{"none", "autobuild", "manual"}

// Validate rejects a mode CodeQL would not accept and the one contradictory
// combination: a build command cannot run under build mode "none".
func (o CreateOptions) Validate() error {
	if o.BuildMode != "" {
		ok := false
		for _, m := range ValidBuildModes {
			if o.BuildMode == m {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("unknown --build-mode %q; expected one of %s",
				o.BuildMode, strings.Join(ValidBuildModes, ", "))
		}
	}
	if o.BuildMode == "none" && len(o.BuildCommands) > 0 {
		return fmt.Errorf("--build-command cannot be used with --build-mode none, " +
			"because that mode does not build the source root")
	}
	return nil
}

// args renders the options for a set of languages that share one command list.
// Specifying a command already implies manual build mode, so no mode is added
// for it.
func (o CreateOptions) args(langs []string) []string {
	var out []string
	if o.BuildMode != "" {
		out = append(out, "--build-mode="+o.BuildMode)
	}
	cmds := o.BuildCommands
	if len(langs) > 0 {
		cmds = o.CommandsFor(langs[0])
	}
	for _, cmd := range cmds {
		out = append(out, "--command="+cmd)
	}
	return out
}

// buildGroup is a set of languages that share one build command list and can
// therefore be created in a single invocation.
type buildGroup struct {
	langs []string
	key   string
}

// groupLangs partitions languages by the command list that applies to them,
// preserving the order languages were requested in.
func groupLangs(langs []string, opts CreateOptions) []buildGroup {
	var groups []buildGroup
	index := map[string]int{}
	for _, lang := range langs {
		key := strings.Join(opts.CommandsFor(lang), "\x1f")
		if i, ok := index[key]; ok {
			groups[i].langs = append(groups[i].langs, lang)
			continue
		}
		index[key] = len(groups)
		groups = append(groups, buildGroup{langs: []string{lang}, key: key})
	}
	return groups
}

// Project is one source tree that gets its own CodeQL database cluster.
type Project struct {
	Name       string // sanitized basename of the entry as presented
	RawPath    string // what the filesystem presented, before symlink resolution
	SourceRoot string // absolute, symlinks resolved; this is what codeql receives
	DBPath     string // <db-root>/<db-prefix><name>

	Scan     SourceScan // what a walk of SourceRoot found
	FromLink string     // the nested symlink this project was expanded from, if any
}

// BuildConfig drives database discovery and creation.
type BuildConfig struct {
	BuildRoot  string   // parent directory whose children are project source dirs
	SourceDirs []string // explicit project source dirs
	DBRoot     string
	DBPrefix   string
	Langs      []string // already passed through LangAlias and deduped

	CreateOptions

	Exclude         []string // globs matched against build-root child names
	ResolveSymlinks bool
	Rebuild         bool
	AllowMissingDB  bool

	// FollowNested expands symlinked directories found inside a source root
	// into projects of their own. CodeQL never follows them, so without this
	// the code behind them is silently absent from every database.
	FollowNested bool
	// AllLangs requests every language for every project, even where no source
	// for it was found.
	AllLangs     bool
	BuildTimeout int // seconds; 0 = no timeout
	BuildThreads int

	UnlockCache      bool
	CheckLockProcess bool
	KillLockProcess  bool

	DryRun  bool
	Verbose bool
	Fancy   bool
}

// Requested reports whether the caller asked for a build phase at all.
func (c *BuildConfig) Requested() bool {
	return c != nil && (c.BuildRoot != "" || len(c.SourceDirs) > 0)
}

// ExpandHome turns a leading ~ into the current user's home directory.
func ExpandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

// ResolveSourceRoot returns the absolute path to hand to codeql --source-root.
//
// When resolve is true every component of the path is followed through its
// symlinks. This matters a great deal: given a directory of symlinks into a
// monorepo, CodeQL otherwise walks up to the targets' common ancestor and
// extracts the whole monorepo instead of the linked subtrees.
func ResolveSourceRoot(path string, resolve bool) (string, error) {
	abs, err := filepath.Abs(ExpandHome(path))
	if err != nil {
		return "", err
	}
	out := abs
	if resolve {
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return "", fmt.Errorf("cannot resolve %s: %w", path, err)
		}
		out = resolved
	}
	info, err := os.Stat(out)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", out)
	}
	return out, nil
}

func sanitizeName(name string) string {
	name = unsafeNameChars.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-.")
	if name == "" {
		return "project"
	}
	return name
}

// IsCodeQLDatabase reports whether dir is itself a CodeQL database directory.
func IsCodeQLDatabase(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "codeql-database.yml"))
	return err == nil
}

// excluded reports whether name matches any of the configured globs. Matching
// is case-insensitive so "Reports" and "reports" behave the same.
func excluded(name string, patterns []string) bool {
	lower := strings.ToLower(name)
	for _, pat := range patterns {
		if pat = strings.TrimSpace(strings.ToLower(pat)); pat == "" {
			continue
		}
		if ok, err := filepath.Match(pat, lower); err == nil && ok {
			return true
		}
	}
	return false
}

// looksLikeDBDir keeps the build from treating previously generated database
// directories as source projects. The equivalent shell loop (`ls -d */`) picks
// up cqlDB_eac/ and builds cqlDB_eac-cqlDB_eac from it.
func looksLikeDBDir(name, prefix string) bool {
	if strings.HasPrefix(strings.ToLower(name), "cqldb") {
		return true
	}
	return prefix != "" && strings.HasPrefix(name, prefix)
}

type candidate struct {
	path          string
	fromBuildRoot bool
}

// DiscoverProjects collects the source trees to build databases for: every
// explicit --source-dir, plus every one-level child of --build-root.
func DiscoverProjects(cfg *BuildConfig) ([]Project, error) {
	var candidates []candidate
	for _, sd := range cfg.SourceDirs {
		if sd = strings.TrimSpace(sd); sd != "" {
			candidates = append(candidates, candidate{path: ExpandHome(sd)})
		}
	}

	if cfg.BuildRoot != "" {
		root := ExpandHome(cfg.BuildRoot)
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, fmt.Errorf("cannot read --build-root %s: %w", root, err)
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			candidates = append(candidates, candidate{
				path:          filepath.Join(root, e.Name()),
				fromBuildRoot: true,
			})
		}
	}

	used := map[string]bool{}
	var projects []Project

	for _, c := range candidates {
		info, err := os.Lstat(c.path)
		if err != nil {
			fmt.Printf("  %s⚠️  Skipping %s: %v%s\n", report.Yellow, c.path, err, report.Reset)
			continue
		}
		// Symlinks need a follow-through Stat to learn whether they point at a
		// directory; Lstat only describes the link itself.
		if !info.IsDir() {
			target, err := os.Stat(c.path)
			if err != nil || !target.IsDir() {
				if cfg.Verbose {
					fmt.Printf("  • Skipping %s (not a directory)\n", filepath.Base(c.path))
				}
				continue
			}
		}

		// Only filter build-root children; an explicit --source-dir is the
		// user's deliberate choice and is honored as given.
		if c.fromBuildRoot {
			base := filepath.Base(c.path)
			if IsCodeQLDatabase(c.path) || looksLikeDBDir(base, cfg.DBPrefix) {
				if cfg.Verbose {
					fmt.Printf("  • Skipping %s (existing CodeQL database)\n", base)
				}
				continue
			}
			if excluded(base, cfg.Exclude) {
				fmt.Printf("  • Skipping %s (matches --exclude)\n", base)
				continue
			}
		}

		sourceRoot, err := ResolveSourceRoot(c.path, cfg.ResolveSymlinks)
		if err != nil {
			fmt.Printf("  %s⚠️  Skipping %s: %v%s\n", report.Yellow, filepath.Base(c.path), err, report.Reset)
			continue
		}

		abs, err := filepath.Abs(ExpandHome(c.path))
		if err != nil {
			fmt.Printf("  %s⚠️  Skipping %s: %v%s\n", report.Yellow, c.path, err, report.Reset)
			continue
		}

		// The name comes from the entry as presented rather than the resolved
		// target ("pdf-export-core" is a better label than "export-core"), but
		// from its absolute form, so that "--source-dir ." is named after the
		// directory instead of degenerating to the "." basename.
		name := sanitizeName(filepath.Base(abs))
		if used[name] {
			for i := 2; ; i++ {
				alt := fmt.Sprintf("%s-%d", name, i)
				if !used[alt] {
					name = alt
					break
				}
			}
		}
		used[name] = true

		p := Project{
			Name:       name,
			RawPath:    abs,
			SourceRoot: sourceRoot,
			DBPath:     filepath.Join(cfg.DBRoot, cfg.DBPrefix+name),
		}
		projects = append(projects, p)

		if p.RawPath != p.SourceRoot {
			fmt.Printf("  🔗 %s: %s resolves to %s\n", name, p.RawPath, p.SourceRoot)
		} else if cfg.Verbose {
			fmt.Printf("  • %s: %s\n", name, p.SourceRoot)
		}

		p.Scan = ScanSourceRoot(p.SourceRoot, cfg.Exclude)
		projects[len(projects)-1] = p
		describeScan(p, cfg)

		// A symlinked directory inside the source root is invisible to CodeQL.
		// Analyzing it as its own project is the only way its code is seen.
		if cfg.FollowNested {
			for _, link := range p.Scan.Nested {
				if strings.HasPrefix(link.Target, p.SourceRoot+string(filepath.Separator)) {
					continue // already inside this root
				}
				if used[link.Target] {
					continue
				}
				used[link.Target] = true

				linkName := sanitizeName(strings.ReplaceAll(filepath.ToSlash(link.Rel), "/", "-"))
				if used[linkName] {
					linkName = sanitizeName(filepath.Base(link.Target))
				}
				used[linkName] = true

				child := Project{
					Name:       linkName,
					RawPath:    filepath.Join(p.SourceRoot, link.Rel),
					SourceRoot: link.Target,
					DBPath:     filepath.Join(cfg.DBRoot, cfg.DBPrefix+linkName),
					FromLink:   link.Rel,
				}
				child.Scan = ScanSourceRoot(child.SourceRoot, cfg.Exclude)
				fmt.Printf("  ↳ %s: following %s into %s\n", linkName, link.Rel, link.Target)
				describeScan(child, cfg)
				projects = append(projects, child)
			}
		} else if len(p.Scan.Nested) > 0 {
			warnNestedSymlinks(p, cfg)
		}
	}

	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	return projects, nil
}

// nestedSymlinkDepth bounds the search for symlinks inside a source root. Only
// directory entries are read, never file contents, so a few levels stay cheap
// even on a very large tree.
const nestedSymlinkDepth = 3

// nestedSymlinkLimit caps how many are listed, to keep the warning readable.
const nestedSymlinkLimit = 10

// findNestedSymlinks lists directory symlinks inside root, which CodeQL will
// not follow when it extracts.
func findNestedSymlinks(root string, exclude []string) (found []string, total int) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || excluded(name, exclude) || nonLangDirs[strings.ToLower(name)] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			// Only a symlink that resolves to a directory hides source.
			if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
				total++
				if len(found) < nestedSymlinkLimit {
					target, _ := filepath.EvalSymlinks(path)
					found = append(found, rel+" -> "+target)
				}
			}
			return nil
		}
		if d.IsDir() && strings.Count(filepath.ToSlash(rel), "/") >= nestedSymlinkDepth-1 {
			return filepath.SkipDir
		}
		return nil
	})
	return found, total
}

// describeScan reports what a source root actually contains.
func describeScan(p Project, cfg *BuildConfig) {
	if len(p.Scan.Languages) == 0 {
		fmt.Printf("     %s⚠️  no recognized source found under %s%s\n",
			report.Yellow, p.SourceRoot, report.Reset)
		return
	}
	if !cfg.Verbose {
		return
	}
	var parts []string
	for _, lang := range AliasLangs(cfg.Langs) {
		if n := p.Scan.Languages[lang]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s:%d", lang, n))
		}
	}
	if len(parts) > 0 {
		fmt.Printf("     source files: %s\n", strings.Join(parts, " "))
	}
}

// warnNestedSymlinks reports source that will silently go unanalyzed.
//
// HydraQL resolves the source root it hands to CodeQL, but CodeQL does not
// follow symlinks found *inside* that root, and it reports no error for them.
// A tree whose code sits behind nested links therefore produces a database
// that looks fine and is missing most of the source, so this is worth saying
// loudly rather than leaving to be discovered later.
func warnNestedSymlinks(p Project, cfg *BuildConfig) {
	found, total := findNestedSymlinks(p.SourceRoot, cfg.Exclude)
	if total == 0 {
		return
	}
	fmt.Printf("  %s⚠️  %s contains %d symlinked director%s that CodeQL will not follow:%s\n",
		report.Yellow, p.Name, total, plural(total), report.Reset)
	for _, entry := range found {
		fmt.Printf("       %s\n", entry)
	}
	if total > len(found) {
		fmt.Printf("       … and %d more\n", total-len(found))
	}
	fmt.Printf("     That code will NOT be analyzed. To include it, point --build-root at the\n")
	fmt.Printf("     directory holding the links so each becomes its own project, or pass each\n")
	fmt.Printf("     resolved target as its own --source-dir.\n")
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// dbFinalized reads the finalised flag out of a database's metadata.
func dbFinalized(dbDir string) bool {
	data, err := os.ReadFile(filepath.Join(dbDir, "codeql-database.yml"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "finalised: true" || line == "finalized: true" {
			return true
		}
	}
	return false
}

// clusterLangDirs lists the language database directories inside a cluster,
// skipping CodeQL's bookkeeping directories.
func clusterLangDirs(dbPath string) []string {
	entries, err := os.ReadDir(dbPath)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() || nonLangDirs[strings.ToLower(e.Name())] {
			continue
		}
		dirs = append(dirs, filepath.Join(dbPath, e.Name()))
	}
	return dirs
}

// clusterReady reports whether every requested language already has a usable
// database under dbPath.
func clusterReady(dbPath string, langs []string) bool {
	if len(langs) == 0 {
		return false
	}
	for _, lang := range langs {
		if !DBStructureOK(filepath.Join(dbPath, lang)) {
			return false
		}
	}
	return true
}

// BuildDatabases creates and finalizes databases for each project and reports,
// per project, which languages ended up scannable.
func BuildDatabases(ctx context.Context, projects []Project, cfg *BuildConfig) []BuildOutcome {
	threads := cfg.BuildThreads
	if threads < 1 {
		threads = 1
	}

	var (
		mu       sync.Mutex
		outcomes []BuildOutcome
		wg       sync.WaitGroup
	)
	sem := make(chan struct{}, threads)

	for _, p := range projects {
		wg.Add(1)
		go func(p Project) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out := buildOne(ctx, p, cfg)
			mu.Lock()
			outcomes = append(outcomes, out)
			mu.Unlock()
		}(p)
	}
	wg.Wait()

	sort.Slice(outcomes, func(i, j int) bool { return outcomes[i].Project.Name < outcomes[j].Project.Name })
	return outcomes
}

// BuildOutcome records what a project's build actually produced.
type BuildOutcome struct {
	Project Project
	Usable  []string // languages with a scannable database
	Empty   []string // languages without a scannable database
	Skipped []string // languages not requested, for want of source

	// AllCreatesFailed distinguishes "codeql never ran successfully" from
	// "it ran but the build produced no code", which need different advice.
	AllCreatesFailed bool
}

// OK reports whether at least one language is scannable. A project skipped for
// having no relevant source is not a failure.
func (o BuildOutcome) OK() bool { return len(o.Usable) > 0 || len(o.Skipped) > 0 }

// withLangs returns a copy of the config scoped to one project's languages.
func (c *BuildConfig) withLangs(langs []string) *BuildConfig {
	clone := *c
	clone.Langs = langs
	return &clone
}

func buildOne(ctx context.Context, p Project, cfg *BuildConfig) BuildOutcome {
	// Requesting a language a project has no source for costs a full
	// extraction attempt, and for a compiled language it also runs the build
	// command in a tree that cannot satisfy it.
	langs := cfg.Langs
	if !cfg.AllLangs {
		present, absent := p.Scan.PresentLangs(cfg.Langs)
		if len(present) == 0 {
			fmt.Printf("  %s⏭  %s: none of %s has source here; skipping%s\n",
				report.Yellow, p.Name, strings.Join(cfg.Langs, ","), report.Reset)
			return BuildOutcome{Project: p, Skipped: absent}
		}
		if len(absent) > 0 {
			fmt.Printf("  ⏭  %s: skipping %s (no source in this tree)\n",
				p.Name, strings.Join(absent, ", "))
		}
		langs = present
	}

	// buildOne works from the project's own language set from here on.
	cfg = cfg.withLangs(langs)
	groups := groupLangs(cfg.Langs, cfg.CreateOptions)

	if !cfg.Rebuild && clusterReady(p.DBPath, cfg.Langs) {
		fmt.Printf("  ⏭  %s: database already built at %s (use --rebuild-db to recreate)\n", p.Name, p.DBPath)
		finalizeCluster(p, cfg)
		return classify(p, cfg)
	}

	jobs := planCreates(p, groups, cfg)

	if cfg.DryRun {
		for _, j := range jobs {
			fmt.Printf("  [dry-run] codeql %s\n", strings.Join(j.args, " "))
		}
		return BuildOutcome{Project: p}
	}

	// `codeql database create` refuses to create its target's parent directory.
	// A cluster create targets p.DBPath, so only its parent must exist; a
	// per-language create targets p.DBPath/<lang>, so p.DBPath itself must.
	needed := filepath.Dir(p.DBPath)
	for _, j := range jobs {
		if !j.cluster {
			needed = p.DBPath
			break
		}
	}
	if err := os.MkdirAll(needed, 0o755); err != nil {
		fmt.Printf("%s✖ Cannot create %s: %v%s\n", report.Red, needed, err, report.Reset)
		return BuildOutcome{Project: p}
	}
	if _, err := os.Stat(p.DBPath); err == nil {
		HandleCacheLocks(p.DBPath, cfg.UnlockCache, cfg.CheckLockProcess, cfg.KillLockProcess, cfg.Verbose)
	}

	if len(jobs) > 1 {
		fmt.Printf("  %sℹ  %d build command group(s) means %d create call(s), so the source is scanned %d time(s).%s\n",
			report.Yellow, len(groups), len(jobs), len(jobs), report.Reset)
	}

	failures := 0
	for _, j := range jobs {
		if !runCreate(ctx, p, j, cfg) {
			failures++
		}
	}
	creates := len(jobs)

	// Finalize regardless of exit codes. CodeQL finalizes a cluster as one step
	// at the end, so one language failing there leaves every language
	// unfinalized even when its extraction completed. Finalizing per language
	// recovers the ones that did.
	if failures > 0 {
		fmt.Printf("  %s⚠️  %s: codeql database create reported failures; salvaging what extracted%s\n",
			report.Yellow, p.Name, report.Reset)
	}
	finalizeCluster(p, cfg)

	out := classify(p, cfg)
	// A cluster create exits non-zero when any one of its languages fails, so a
	// failed create does not mean nothing was produced. Only claim total
	// failure when the salvage pass also found nothing usable.
	out.AllCreatesFailed = creates > 0 && failures == creates && len(out.Usable) == 0
	reportOutcome(out, cfg)
	return out
}

// createJob is one `database create` invocation.
type createJob struct {
	langs   []string
	args    []string
	cluster bool
}

// planCreates returns every create invocation for a project, in execution
// order.
//
// At most one group may use --db-cluster, and it has to run first. A cluster
// create with --overwrite destroys the sibling language directories under its
// root, while a per-language create with --overwrite leaves them alone (both
// measured against CodeQL 2.25.6). Clustering the largest group keeps one
// shared source scan for the languages that can share one, which matters when
// the source tree is large.
func planCreates(p Project, groups []buildGroup, cfg *BuildConfig) []createJob {
	clusterIdx := -1
	if len(groups) == 1 {
		clusterIdx = 0
	} else {
		best := 1 // only worth clustering two or more languages
		for i, g := range groups {
			if len(g.langs) > best {
				best, clusterIdx = len(g.langs), i
			}
		}
	}

	var jobs []createJob
	if clusterIdx >= 0 {
		jobs = append(jobs, clusterJob(p, groups[clusterIdx], cfg))
	}
	for i, g := range groups {
		if i == clusterIdx {
			continue
		}
		for _, lang := range g.langs {
			jobs = append(jobs, singleJob(p, g, lang, cfg))
		}
	}
	return jobs
}

func clusterJob(p Project, g buildGroup, cfg *BuildConfig) createJob {
	args := []string{"database", "create", p.DBPath,
		"--db-cluster",
		"--language=" + strings.Join(g.langs, ","),
		"--source-root=" + p.SourceRoot,
	}
	args = append(args, cfg.CreateOptions.args(g.langs)...)
	if cfg.Rebuild {
		args = append(args, "--overwrite")
	}
	return createJob{langs: g.langs, args: args, cluster: true}
}

func singleJob(p Project, g buildGroup, lang string, cfg *BuildConfig) createJob {
	args := []string{"database", "create", filepath.Join(p.DBPath, lang),
		"--language=" + lang,
		"--source-root=" + p.SourceRoot,
	}
	args = append(args, cfg.CreateOptions.args([]string{lang})...)
	if cfg.Rebuild {
		args = append(args, "--overwrite")
	}
	return createJob{langs: []string{lang}, args: args}
}

func runCreate(ctx context.Context, p Project, j createJob, cfg *BuildConfig) bool {
	args := j.args
	label := p.Name + " (" + strings.Join(j.langs, ",") + ")"

	if cfg.Fancy {
		fmt.Printf("%s🏗  Building %s%s\n", report.Cyan, label, report.Reset)
	} else {
		fmt.Printf("  → Building database for %s\n", label)
	}

	buildCtx := ctx
	if cfg.BuildTimeout > 0 {
		var cancel context.CancelFunc
		buildCtx, cancel = context.WithTimeout(ctx, time.Duration(cfg.BuildTimeout)*time.Second)
		defer cancel()
	}

	stopProgress := startProgress("build " + label)
	start := time.Now()

	cmd := exec.CommandContext(buildCtx, "codeql", args...)
	var errBuf bytes.Buffer
	if cfg.Verbose {
		fmt.Printf("   cmd: %s\n", strings.Join(cmd.Args, " "))
		cmd.Stderr = io.MultiWriter(&errBuf, os.Stderr)
	} else {
		cmd.Stderr = &errBuf
	}
	err := cmd.Run()
	stopProgress()

	if err != nil {
		if buildCtx.Err() == context.DeadlineExceeded {
			fmt.Printf("%s⏱  Database build for %s timed out after %ds%s\n",
				report.Yellow, label, cfg.BuildTimeout, report.Reset)
			appendLog("hydraql_failures.log",
				fmt.Sprintf("BUILD TIMEOUT after %ds: %s\n", cfg.BuildTimeout, label))
			return false
		}
		stderr := errBuf.String()
		appendLog("hydraql_failures.log", fmt.Sprintf("BUILD FAIL %s:\n%s\n", label, stderr))
		if !cfg.Verbose {
			fmt.Printf("%s", stderr[:min(len(stderr), 800)])
		}
		fmt.Printf("%s✖ codeql database create failed for %s%s\n", report.Red, label, report.Reset)
		return false
	}

	fmt.Printf("  ✅ Built %s in %ds\n", label, int(time.Since(start).Seconds()))
	return true
}

// classify sorts the requested languages into those with a scannable database
// and those that extracted nothing.
func classify(p Project, cfg *BuildConfig) BuildOutcome {
	out := BuildOutcome{Project: p}
	for _, lang := range cfg.Langs {
		if DBStructureOK(filepath.Join(p.DBPath, lang)) {
			out.Usable = append(out.Usable, lang)
		} else {
			out.Empty = append(out.Empty, lang)
		}
	}
	return out
}

func reportOutcome(out BuildOutcome, cfg *BuildConfig) {
	if len(out.Usable) > 0 {
		fmt.Printf("   ✅ %s: %s ready\n", out.Project.Name, strings.Join(out.Usable, ", "))
	}
	if len(out.Empty) > 0 {
		if out.AllCreatesFailed {
			// Blaming the build command here would send the reader down the
			// wrong path: codeql itself never got as far as extracting.
			fmt.Printf("   %s✖ %s: no database was created for %s%s\n",
				report.Red, out.Project.Name, strings.Join(out.Empty, ", "), report.Reset)
			fmt.Printf("      Every codeql database create call failed; see the error above and hydraql_failures.log.\n")
		} else {
			// Separate the two causes: a compiled language needs a build, while
			// an interpreted one that came up empty simply is not in the tree.
			var needsBuild, absent []string
			for _, lang := range out.Empty {
				if NeedsBuild(lang) {
					needsBuild = append(needsBuild, lang)
				} else {
					absent = append(absent, lang)
				}
			}
			if len(absent) > 0 {
				fmt.Printf("   %s⚠️  %s: no %s source found in this tree%s\n",
					report.Yellow, out.Project.Name, strings.Join(absent, ", "), report.Reset)
				fmt.Printf("      Drop it from --langs to skip the extraction attempt.\n")
			}
			if len(needsBuild) > 0 {
				fmt.Printf("   %s⚠️  %s: %s extracted no code%s\n",
					report.Yellow, out.Project.Name, strings.Join(needsBuild, ", "), report.Reset)
				if len(cfg.CreateOptions.CommandsFor(needsBuild[0])) == 0 {
					fmt.Printf("      A compiled language needs --build-command naming a command that invokes its compiler.\n")
				} else {
					fmt.Printf("      Check that the build command actually compiles it; CodeQL only sees a real build.\n")
				}
			}
		}
	}
	fmt.Printf("   Built %d of %d requested language(s) for %s\n",
		len(out.Usable), len(cfg.Langs), out.Project.Name)
}

// finalizeCluster finalizes each language database in the cluster. `codeql
// database create` normally finalizes on its own, so this is the safety net
// for a build that was interrupted partway through.
func finalizeCluster(p Project, cfg *BuildConfig) bool {
	if cfg.DryRun {
		return false
	}
	dirs := clusterLangDirs(p.DBPath)
	if len(dirs) == 0 {
		if cfg.Verbose {
			fmt.Printf("   • Nothing to finalize under %s\n", p.DBPath)
		}
		return false
	}
	ok := true
	for _, dir := range dirs {
		if dbFinalized(dir) {
			if cfg.Verbose {
				fmt.Printf("   • %s already finalized\n", filepath.Base(dir))
			}
			continue
		}
		fmt.Printf("   → Finalizing %s\n", dir)
		if !FinalizeDB(dir, cfg.Verbose) {
			ok = false
		}
	}
	return ok
}
