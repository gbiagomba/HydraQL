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

// Project is one source tree that gets its own CodeQL database cluster.
type Project struct {
	Name       string // sanitized basename of the entry as presented
	RawPath    string // what the filesystem presented, before symlink resolution
	SourceRoot string // absolute, symlinks resolved; this is what codeql receives
	DBPath     string // <db-root>/<db-prefix><name>
}

// BuildConfig drives database discovery and creation.
type BuildConfig struct {
	BuildRoot  string   // parent directory whose children are project source dirs
	SourceDirs []string // explicit project source dirs
	DBRoot     string
	DBPrefix   string
	Langs      []string // already passed through LangAlias and deduped

	Exclude         []string // globs matched against build-root child names
	ResolveSymlinks bool
	Rebuild         bool
	BuildTimeout    int // seconds; 0 = no timeout
	BuildThreads    int

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

		// The name comes from the entry as presented, not from the resolved
		// target: "pdf-export-core" is a better label than "export-core".
		name := sanitizeName(filepath.Base(c.path))
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

		abs, _ := filepath.Abs(ExpandHome(c.path))
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
	}

	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	return projects, nil
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

// BuildDatabases creates and finalizes one database cluster per project and
// returns the projects whose clusters are usable afterwards.
func BuildDatabases(ctx context.Context, projects []Project, cfg *BuildConfig) []Project {
	threads := cfg.BuildThreads
	if threads < 1 {
		threads = 1
	}

	var (
		mu    sync.Mutex
		built []Project
		wg    sync.WaitGroup
	)
	sem := make(chan struct{}, threads)

	for _, p := range projects {
		wg.Add(1)
		go func(p Project) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if buildOne(ctx, p, cfg) {
				mu.Lock()
				built = append(built, p)
				mu.Unlock()
			}
		}(p)
	}
	wg.Wait()

	sort.Slice(built, func(i, j int) bool { return built[i].Name < built[j].Name })
	return built
}

func buildOne(ctx context.Context, p Project, cfg *BuildConfig) bool {
	langArg := strings.Join(cfg.Langs, ",")

	if !cfg.Rebuild && clusterReady(p.DBPath, cfg.Langs) {
		fmt.Printf("  ⏭  %s: database already built at %s (use --rebuild-db to recreate)\n", p.Name, p.DBPath)
		return finalizeCluster(p, cfg)
	}

	if _, err := os.Stat(p.DBPath); err == nil {
		HandleCacheLocks(p.DBPath, cfg.UnlockCache, cfg.CheckLockProcess, cfg.KillLockProcess, cfg.Verbose)
	}

	args := []string{"database", "create", p.DBPath,
		"--db-cluster",
		"--language=" + langArg,
		"--source-root=" + p.SourceRoot,
	}
	if cfg.Rebuild {
		args = append(args, "--overwrite")
	}

	if cfg.DryRun {
		fmt.Printf("  [dry-run] codeql %s\n", strings.Join(args, " "))
		return false
	}

	// `codeql database create --db-cluster` refuses to create the cluster's
	// parent directory, so the db-root has to exist first.
	if err := os.MkdirAll(filepath.Dir(p.DBPath), 0o755); err != nil {
		fmt.Printf("%s✖ Cannot create %s: %v%s\n", report.Red, filepath.Dir(p.DBPath), err, report.Reset)
		return false
	}

	if cfg.Fancy {
		fmt.Printf("%s🏗  Building %s (%s)%s\n", report.Cyan, p.Name, langArg, report.Reset)
	} else {
		fmt.Printf("  → Building database for %s (%s)\n", p.Name, langArg)
	}

	buildCtx := ctx
	if cfg.BuildTimeout > 0 {
		var cancel context.CancelFunc
		buildCtx, cancel = context.WithTimeout(ctx, time.Duration(cfg.BuildTimeout)*time.Second)
		defer cancel()
	}

	stopProgress := startProgress("build " + p.Name)
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
				report.Yellow, p.Name, cfg.BuildTimeout, report.Reset)
			appendLog("hydraql_failures.log",
				fmt.Sprintf("BUILD TIMEOUT after %ds: %s\n", cfg.BuildTimeout, p.Name))
			return false
		}
		stderr := errBuf.String()
		appendLog("hydraql_failures.log", fmt.Sprintf("BUILD FAIL %s:\n%s\n", p.Name, stderr))
		if !cfg.Verbose {
			fmt.Printf("%s", stderr[:min(len(stderr), 800)])
		}
		fmt.Printf("%s✖ Failed to build database for %s%s\n", report.Red, p.Name, report.Reset)
		return false
	}

	fmt.Printf("  ✅ Built %s in %ds\n", p.Name, int(time.Since(start).Seconds()))
	return finalizeCluster(p, cfg)
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
		fmt.Printf("%s⚠️  No language databases found under %s%s\n", report.Yellow, p.DBPath, report.Reset)
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
