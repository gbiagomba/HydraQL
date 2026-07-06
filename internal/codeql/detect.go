package codeql

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gbiagomba/hydraql/internal/report"
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
	AllowMissingDB   bool
	ForceScamUnready bool
	DryRun           bool
	Verbose          bool
	Fancy            bool
	UnlockCache      bool
	CheckLockProcess bool
	KillLockProcess  bool
}

// DetectDatabases returns a map of lang→dbPath for usable databases.
func DetectDatabases(langs []string, dbRoot string, cfg *DetectConfig) (map[string]string, bool) {
	fmt.Printf("[*] HydraQL looking for CodeQL databases in %s/...\n", dbRoot)
	found := map[string]string{}
	var missing, unfinalized []string

	for _, raw := range langs {
		lang := LangAlias(raw)
		dbDir := filepath.Join(dbRoot, lang)
		meta := filepath.Join(dbDir, "codeql-database.yml")

		if _, err := os.Stat(meta); os.IsNotExist(err) {
			fmt.Printf("  %s⚠️  Missing DB for language: %s (expected %s)%s\n", report.Yellow, lang, dbDir, report.Reset)
			if cfg.AutoInitDB {
				if cfg.SourceRoot == "" {
					fmt.Printf("%s✖ --auto-init-db requires --source-root <path>%s\n", report.Red, report.Reset)
					return nil, false
				}
				fmt.Printf("  → Creating DB for %s at %s\n", lang, dbDir)
				if !cfg.DryRun && !InitDB(dbDir, lang, cfg.SourceRoot, cfg.Verbose) {
					if !cfg.AllowMissingDB {
						return nil, false
					}
				}
			} else {
				missing = append(missing, lang)
				continue
			}
		}

		HandleCacheLocks(dbDir, cfg.UnlockCache, cfg.CheckLockProcess, cfg.KillLockProcess, cfg.Verbose)

		if cfg.AutoFinalizeDB && !cfg.DryRun {
			fmt.Printf("  → Finalizing DB for %s at %s\n", lang, dbDir)
			if !FinalizeDB(dbDir, cfg.Verbose) {
				unfinalized = append(unfinalized, lang)
				if !cfg.AllowMissingDB {
					continue
				}
			}
		}

		if !DBStructureOK(dbDir) {
			fmt.Printf("  %s⚠️  DB at %s looks unusual (no db-* subdirs). Proceeding cautiously.%s\n", report.Yellow, dbDir, report.Reset)
		}

		if !cfg.ForceScamUnready && IsDBEmpty(dbDir) {
			fmt.Printf("  %s⚠️  DB for %s appears empty/unusable; skipping (use --force-scan-unready to override).%s\n", report.Yellow, lang, report.Reset)
			continue
		}

		fmt.Printf("  ✅ Found: %s → %s\n", lang, dbDir)
		found[lang] = dbDir
	}

	if len(missing) > 0 || len(unfinalized) > 0 {
		if !cfg.AllowMissingDB {
			if len(missing) > 0 {
				fmt.Printf("%s✖ Some requested DBs are missing:%s %s\n", report.Red, report.Reset, strings.Join(missing, ", "))
			}
			if len(unfinalized) > 0 {
				fmt.Printf("%s✖ Some requested DBs failed to finalize:%s %s\n", report.Red, report.Reset, strings.Join(unfinalized, ", "))
			}
			fmt.Println("Use --allow-missing-db to continue anyway.")
			return nil, false
		}
	}
	return found, true
}
