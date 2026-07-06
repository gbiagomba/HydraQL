// HydraQL — Parallel CodeQL Scanning Engine (Go edition)
// Many-headed like the Hydra: runs CodeQL queries across multiple databases
// and languages in parallel, merging results into CSV, JSON, or SARIF.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gbiagomba/hydraql/internal/codeql"
	"github.com/gbiagomba/hydraql/internal/merge"
	"github.com/gbiagomba/hydraql/internal/report"
)

const version = "2.0.0"

// stringSliceFlag supports repeated --flag value or --flag v1,v2
type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(value string) error {
	for _, v := range strings.Split(value, ",") {
		if v = strings.TrimSpace(v); v != "" {
			*s = append(*s, v)
		}
	}
	return nil
}

func main() {
	// ---- Flags ----
	var queryDirs stringSliceFlag

	langs := flag.String("langs", "java,javascript,typescript,python",
		"Comma-separated languages to scan")
	dbRoot := flag.String("db-root", "cqlDB",
		"Root of CodeQL databases (expects <db-root>/<lang>/codeql-database.yml)")
	flag.Var(&queryDirs, "query-dir",
		"Query directory (repeat or comma-separate for multiple)")
	threads := flag.Int("threads", 6, "Parallel worker goroutines")
	flag.IntVar(threads, "parallel", 6, "Alias for --threads")
	outputFormat := flag.String("output-format", "csv", "Output format: csv, json, sarif")
	severityFilter := flag.String("severity", "", "Filter findings by severity (e.g. HIGH, CRITICAL)")
	strictSeverity := flag.Bool("strict-severity", false, "Exact severity match only (no loose mapping)")
	fancy := flag.Bool("fancy", false, "Colored output and ASCII chart")
	dryRun := flag.Bool("dry-run", false, "List actions without running CodeQL")
	packInstall := flag.Bool("pack-install", false, "Run `codeql pack install` before scanning")
	allowMissingDB := flag.Bool("allow-missing-db", false, "Continue if some DBs are missing/unfinalized")
	verbose := flag.Bool("verbose", false, "Verbose command logging and error details")
	suiteOnly := flag.Bool("suite-only", false, "Run only .qls suites, skip individual .ql files")

	// Lock handling
	unlockCache := flag.Bool("unlock-cache", false, "Delete stale IMB cache .lock files")
	checkLockProc := flag.Bool("check-lock-process", false, "Inspect PID inside any cache .lock file")
	killLockProc := flag.Bool("kill-lock-process", false, "DANGER: kill -9 the PID found in the cache .lock")

	// DB automation
	autoFinalizeDB := flag.Bool("auto-finalize-db", false, "Automatically finalize unfinalized DBs")
	autoInitDB := flag.Bool("auto-init-db", false, "Create missing DBs (requires --source-root)")
	sourceRoot := flag.String("source-root", "", "Source root for --auto-init-db")
	forceScamUnready := flag.Bool("force-scan-unready", false, "Scan DBs that appear empty/unusable")

	// Timeout
	queryTimeout := flag.Int("query-timeout", 600, "Per-query timeout in seconds (0 = no timeout)")
	noTimeout := flag.Bool("no-timeout", false, "Disable per-query timeout entirely")

	showVersion := flag.Bool("version", false, "Print version and exit")

	flag.Parse()

	if *showVersion {
		fmt.Printf("HydraQL %s (Go edition)\n", version)
		os.Exit(0)
	}

	if *noTimeout {
		*queryTimeout = 0
	}

	if *fancy {
		fmt.Printf("%sHydraQL%s starting…\n", report.Green, report.Reset)
	} else {
		fmt.Println("HydraQL starting…")
	}

	// ---- Normalize query dirs ----
	dirs := normalizeQueryDirs([]string(queryDirs))

	// ---- Normalize langs ----
	var langList []string
	for _, l := range strings.Split(*langs, ",") {
		if l = strings.TrimSpace(l); l != "" {
			langList = append(langList, codeql.LangAlias(l))
		}
	}
	// de-duplicate
	seen := map[string]bool{}
	var uniqLangs []string
	for _, l := range langList {
		if !seen[l] {
			seen[l] = true
			uniqLangs = append(uniqLangs, l)
		}
	}
	langList = uniqLangs

	// ---- Pack install ----
	if *packInstall {
		installPacks(*fancy, *verbose)
	}

	// ---- Detect databases ----
	detectCfg := &codeql.DetectConfig{
		AutoFinalizeDB:   *autoFinalizeDB,
		AutoInitDB:       *autoInitDB,
		SourceRoot:        *sourceRoot,
		AllowMissingDB:   *allowMissingDB,
		ForceScamUnready: *forceScamUnready,
		DryRun:           *dryRun,
		Verbose:          *verbose,
		Fancy:            *fancy,
		UnlockCache:      *unlockCache,
		CheckLockProcess: *checkLockProc,
		KillLockProcess:  *killLockProc,
	}
	foundDBs, ok := codeql.DetectDatabases(langList, *dbRoot, detectCfg)
	if !ok {
		os.Exit(1)
	}
	if len(foundDBs) == 0 {
		fmt.Println("No valid databases found; exiting.")
		os.Exit(1)
	}

	// ---- Gather queries ----
	pairs := codeql.GatherQueries(dirs, langList, *suiteOnly, *verbose)
	if len(pairs) == 0 {
		fmt.Println("No queries found; exiting.")
		fmt.Println(" Searched in:")
		for _, d := range dirs {
			fmt.Printf("  - %s\n", d)
		}
		fmt.Println(" For languages:")
		for _, l := range langList {
			fmt.Printf("  - %s\n", l)
		}
		os.Exit(1)
	}

	if *fancy {
		fmt.Printf("%s[*]%s Found %d queries to run\n", report.Magenta, report.Reset, len(pairs))
	}

	// ---- Parallel execution ----
	tmpDir := "tmp_hydraql_output"
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create tmp dir: %v\n", err)
		os.Exit(1)
	}
	// Reset failure log
	_ = os.WriteFile("hydraql_failures.log", nil, 0644)

	runCfg := &codeql.RunConfig{
		Verbose:          *verbose,
		Fancy:            *fancy,
		DryRun:           *dryRun,
		QueryTimeout:     *queryTimeout,
		AutoFinalizeDB:   *autoFinalizeDB,
		UnlockCache:      *unlockCache,
		CheckLockProcess: *checkLockProc,
		KillLockProcess:  *killLockProc,
		SeverityFilter:   *severityFilter,
		StrictSeverity:   *strictSeverity,
		OutputFormat:     *outputFormat,
	}

	type result struct {
		outFile  string
		findings int
		timedOut bool
	}

	results := make(chan result, len(pairs))
	workerSem := make(chan struct{}, *threads)
	ctx := context.Background()

	var wg sync.WaitGroup
	for _, pair := range pairs {
		dbPath, exists := foundDBs[pair.Lang]
		if !exists {
			if *verbose {
				fmt.Printf("%s⚠️  Skipping %s — no DB for %s%s\n",
					report.Yellow, pair.Query, pair.Lang, report.Reset)
			}
			continue
		}
		wg.Add(1)
		go func(q codeql.QueryPair, db string) {
			defer wg.Done()
			workerSem <- struct{}{}
			defer func() { <-workerSem }()

			r := codeql.RunQuery(ctx, q.Query, q.Lang, db, tmpDir, runCfg)
			results <- result{r.OutFile, r.Findings, r.TimedOut}
		}(pair, dbPath)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var outFiles []string
	var perQueryCounts []int
	timedOutCount := 0
	for r := range results {
		if r.timedOut {
			timedOutCount++
		}
		if r.outFile != "" {
			outFiles = append(outFiles, r.outFile)
		}
		perQueryCounts = append(perQueryCounts, r.findings)
	}

	// ---- Merge results ----
	ts := time.Now().Format("20060102150405")
	outputFile := fmt.Sprintf("HydraQL_output-%s.%s", ts, *outputFormat)

	var total int
	var mergeErr error
	switch *outputFormat {
	case "csv":
		total, mergeErr = merge.MergeCSV(outFiles, outputFile, *severityFilter, *strictSeverity)
	case "json":
		total, mergeErr = merge.MergeJSON(outFiles, outputFile, *severityFilter, *strictSeverity)
	default:
		total, mergeErr = merge.MergeSARIF(outFiles, outputFile, *severityFilter, *strictSeverity)
	}
	if mergeErr != nil {
		fmt.Fprintf(os.Stderr, "Merge error: %v\n", mergeErr)
		os.Exit(1)
	}

	// ---- Summary ----
	report.PrintSummary(len(pairs), total, timedOutCount, perQueryCounts, *fancy)

	if *fancy {
		if total > 0 {
			fmt.Printf("%s✅ Done! Results saved to %s%s\n", report.Green, outputFile, report.Reset)
		} else {
			fmt.Printf("%s⚠️  No results. Wrote header to %s%s\n", report.Yellow, outputFile, report.Reset)
		}
	} else {
		fmt.Printf("Results saved to %s\n", outputFile)
	}

	// Cleanup tmp
	cleanTmp(tmpDir)
}

func normalizeQueryDirs(raw []string) []string {
	if len(raw) == 0 {
		home, _ := os.UserHomeDir()
		return []string{filepath.Join(home, "Git", "codeql")}
	}
	var out []string
	for _, entry := range raw {
		for _, tok := range strings.Split(entry, ",") {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				continue
			}
			if strings.HasPrefix(tok, "~") {
				home, _ := os.UserHomeDir()
				tok = filepath.Join(home, tok[1:])
			}
			out = append(out, tok)
		}
	}
	return out
}

func installPacks(fancy, verbose bool) {
	if fancy {
		fmt.Printf("%s[*]%s Installing CodeQL query packs...\n", report.Magenta, report.Reset)
	}
	cmd := exec.Command("codeql", "pack", "install")
	if verbose {
		fmt.Printf("   cmd: %s\n", strings.Join(cmd.Args, " "))
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("%s⚠️  Failed to install query packs%s\n", report.Yellow, report.Reset)
		if verbose {
			fmt.Printf("%s", string(out)[:min(len(out), 800)])
		}
	}
}

func cleanTmp(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
	_ = os.Remove(dir)
}
