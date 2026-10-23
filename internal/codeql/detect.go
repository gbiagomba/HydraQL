package codeql

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gbiagomba/hydraql/v2/internal/report"
)

var excludeDirs = map[string]bool{
	"default/cache": true,
	"logs":          true,
	"results":       true,
	"working":       true,
	"diagnostic":    true,
}

func LangAlias(lang string) string {
	switch strings.ToLower(lang) {
	case "typescript":
		return "javascript"
	case "kotlin":
		return "java"
	}
	return strings.ToLower(lang)
}

// knownLanguages are the names --langs and --build-command accept. The aliases
// typescript and kotlin are included because CodeQL resolves them to the
// javascript and java extractors.
var knownLanguages = map[string]bool{
	"javascript": true, "typescript": true, "java": true, "kotlin": true,
	"python": true, "cpp": true, "csharp": true, "go": true,
	"ruby": true, "swift": true, "rust": true, "actions": true,
}

// IsKnownLanguage reports whether name is a language HydraQL recognizes.
func IsKnownLanguage(name string) bool { return knownLanguages[strings.ToLower(name)] }

// AliasLangs maps every entry through LangAlias and drops duplicates, keeping
// the original order. CodeQL has no separate typescript extractor, so passing
// "javascript,typescript" to `database create --db-cluster` would extract the
// same files twice into two sibling directories.
func AliasLangs(langs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range langs {
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		a := LangAlias(l)
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

func DBStructureOK(dbDir string) bool {
	if _, err := os.Stat(filepath.Join(dbDir, "codeql-database.yml")); err != nil {
		return false
	}
	entries, err := os.ReadDir(dbDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "db-") {
			return true
		}
	}
	return false
}

func IsDBEmpty(dbDir string) bool {
	count := 0
	err := filepath.WalkDir(dbDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dbDir, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			for excl := range excludeDirs {
				if strings.HasPrefix(rel, excl) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		for excl := range excludeDirs {
			if strings.HasPrefix(rel, excl) {
				return nil
			}
		}
		count++
		if count > 50 {
			return fmt.Errorf("stop") // signal enough files found
		}
		return nil
	})
	return err == nil && count <= 50
}

type DetectConfig struct {
	AutoFinalizeDB   bool
	AutoInitDB       bool
	SourceRoot       string
	CreateOptions    CreateOptions
	ResolveSymlinks  bool
	AllowMissingDB   bool
	ForceScamUnready bool
	DryRun           bool
	Verbose          bool
	Fancy            bool
	UnlockCache      bool
	CheckLockProcess bool
	KillLockProcess  bool
}

// ScanTarget is one (project, language, database) triple to run queries against.
// Project is empty for the flat single-project layout.
type ScanTarget struct {
	Project string
	Lang    string
	DBPath  string
}

// Label renders the target for log lines.
func (t ScanTarget) Label() string {
	if t.Project == "" {
		return t.Lang
	}
	return t.Project + "/" + t.Lang
}

type detectState struct {
	targets     []ScanTarget
	missing     []string
	unfinalized []string
}

// consider vets one candidate database directory and records it as a scan
// target when it is usable. initOK enables --auto-init-db for this slot;
// softMissing skips a missing database silently, which is what a multi-project
// layout needs because projects legitimately differ in language.
func (s *detectState) consider(project, lang, dbDir string, cfg *DetectConfig, initOK, softMissing bool) bool {
	label := lang
	if project != "" {
		label = project + "/" + lang
	}
	meta := filepath.Join(dbDir, "codeql-database.yml")

	if _, err := os.Stat(meta); os.IsNotExist(err) {
		if initOK && cfg.AutoInitDB {
			if cfg.SourceRoot == "" {
				fmt.Printf("%s✖ --auto-init-db requires --source-root <path>%s\n", report.Red, report.Reset)
				return false
			}
			sourceRoot, err := ResolveSourceRoot(cfg.SourceRoot, cfg.ResolveSymlinks)
			if err != nil {
				fmt.Printf("%s✖ %v%s\n", report.Red, err, report.Reset)
				return false
			}
			fmt.Printf("  → Creating DB for %s at %s (source %s)\n", lang, dbDir, sourceRoot)
			if !cfg.DryRun && !InitDB(dbDir, lang, sourceRoot, cfg.CreateOptions, cfg.Verbose) {
				if !cfg.AllowMissingDB {
					return false
				}
			}
		} else if softMissing {
			if cfg.Verbose {
				fmt.Printf("  • No %s database for %s; skipping\n", lang, project)
			}
			return true
		} else {
			fmt.Printf("  %s⚠️  Missing DB for language: %s (expected %s)%s\n",
				report.Yellow, label, dbDir, report.Reset)
			s.missing = append(s.missing, label)
			return true
		}
	}

	// A cluster built for several languages gets a directory per language even
	// when a language has no code: `database create --db-cluster` warns ("not
	// any written in Python") and still leaves an empty directory behind. That
	// is a normal outcome, not a failure, so it is skipped quietly instead of
	// counting as an unfinalized database.
	if softMissing && !DBStructureOK(dbDir) {
		if cfg.Verbose {
			fmt.Printf("  • No %s code was extracted for %s; skipping\n", lang, project)
		}
		return true
	}

	HandleCacheLocks(dbDir, cfg.UnlockCache, cfg.CheckLockProcess, cfg.KillLockProcess, cfg.Verbose)

	if cfg.AutoFinalizeDB && !cfg.DryRun {
		fmt.Printf("  → Finalizing DB for %s at %s\n", label, dbDir)
		if !FinalizeDB(dbDir, cfg.Verbose) {
			s.unfinalized = append(s.unfinalized, label)
			if !cfg.AllowMissingDB {
				return true
			}
		}
	}

	if !DBStructureOK(dbDir) {
		fmt.Printf("  %s⚠️  DB at %s looks unusual (no db-* subdirs). Proceeding cautiously.%s\n",
			report.Yellow, dbDir, report.Reset)
	}

	if !cfg.ForceScamUnready && IsDBEmpty(dbDir) {
		fmt.Printf("  %s⚠️  DB for %s appears empty/unusable; skipping (use --force-scan-unready to override).%s\n",
			report.Yellow, label, report.Reset)
		return true
	}

	fmt.Printf("  ✅ Found: %s → %s\n", label, dbDir)
	s.targets = append(s.targets, ScanTarget{Project: project, Lang: lang, DBPath: dbDir})
	return true
}

// flatLayoutExists reports whether db-root directly holds <lang>/ databases.
func flatLayoutExists(dbRoot string, langs []string) bool {
	for _, lang := range langs {
		if _, err := os.Stat(filepath.Join(dbRoot, LangAlias(lang), "codeql-database.yml")); err == nil {
			return true
		}
	}
	return false
}

// nestedProjects lists db-root children that hold <project>/<lang>/ databases.
func nestedProjects(dbRoot string, langs []string) []string {
	entries, err := os.ReadDir(dbRoot)
	if err != nil {
		return nil
	}
	var projects []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || nonLangDirs[strings.ToLower(e.Name())] {
			continue
		}
		for _, lang := range langs {
			probe := filepath.Join(dbRoot, e.Name(), LangAlias(lang), "codeql-database.yml")
			if _, err := os.Stat(probe); err == nil {
				projects = append(projects, e.Name())
				break
			}
		}
	}
	return projects
}

// singleDBLang infers the language of a non-cluster database directory.
func singleDBLang(dbDir string) string {
	entries, err := os.ReadDir(dbDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "db-") {
				return LangAlias(strings.TrimPrefix(e.Name(), "db-"))
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(dbDir, "codeql-database.yml"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "primaryLanguage:"); ok {
			return LangAlias(strings.Trim(strings.TrimSpace(rest), `"'`))
		}
	}
	return ""
}

// DetectTargets resolves db-root into the list of databases to scan. Three
// layouts are recognized, in order:
//
//  1. <db-root>/codeql-database.yml            a single non-cluster database
//  2. <db-root>/<lang>/                        the flat layout
//  3. <db-root>/<project>/<lang>/              one cluster per project
//
// Layout 2 is also the fallback, so --auto-init-db keeps working against an
// empty db-root.
func DetectTargets(langs []string, dbRoot string, cfg *DetectConfig) ([]ScanTarget, bool) {
	fmt.Printf("[*] HydraQL looking for CodeQL databases in %s/...\n", dbRoot)

	langs = AliasLangs(langs)
	wanted := map[string]bool{}
	for _, l := range langs {
		wanted[l] = true
	}

	s := &detectState{}

	switch {
	case IsCodeQLDatabase(dbRoot):
		lang := singleDBLang(dbRoot)
		if lang == "" {
			fmt.Printf("%s✖ Cannot determine the language of the database at %s%s\n",
				report.Red, dbRoot, report.Reset)
			return nil, false
		}
		if !wanted[lang] {
			fmt.Printf("  %s⚠️  Database at %s is %s, which is not in --langs; skipping.%s\n",
				report.Yellow, dbRoot, lang, report.Reset)
			break
		}
		project := filepath.Base(strings.TrimSuffix(filepath.Clean(dbRoot), string(filepath.Separator)))
		if !s.consider(project, lang, dbRoot, cfg, false, false) {
			return nil, false
		}

	case !flatLayoutExists(dbRoot, langs) && len(nestedProjects(dbRoot, langs)) > 0:
		for _, project := range nestedProjects(dbRoot, langs) {
			before := len(s.targets)
			for _, lang := range langs {
				if !s.consider(project, lang, filepath.Join(dbRoot, project, lang), cfg, false, true) {
					return nil, false
				}
			}
			if len(s.targets) == before {
				fmt.Printf("  %s⚠️  No usable database for project %s%s\n", report.Yellow, project, report.Reset)
			}
		}

	default:
		for _, lang := range langs {
			if !s.consider("", lang, filepath.Join(dbRoot, lang), cfg, true, false) {
				return nil, false
			}
		}
	}

	if len(s.missing) > 0 || len(s.unfinalized) > 0 {
		if !cfg.AllowMissingDB {
			if len(s.missing) > 0 {
				fmt.Printf("%s✖ Some requested DBs are missing:%s %s\n",
					report.Red, report.Reset, strings.Join(s.missing, ", "))
			}
			if len(s.unfinalized) > 0 {
				fmt.Printf("%s✖ Some requested DBs failed to finalize:%s %s\n",
					report.Red, report.Reset, strings.Join(s.unfinalized, ", "))
			}
			fmt.Println("Use --allow-missing-db to continue anyway.")
			return nil, false
		}
	}
	return s.targets, true
}
