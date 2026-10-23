// HydraQL — Parallel CodeQL Scanning Engine (Go edition)
// Many-headed like the Hydra: builds CodeQL databases, then runs queries across
// multiple databases and languages in parallel, merging results into CSV, JSON,
// or SARIF.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	pflag "github.com/spf13/pflag"

	"github.com/gbiagomba/hydraql/v2/internal/codeql"
	"github.com/gbiagomba/hydraql/v2/internal/merge"
	"github.com/gbiagomba/hydraql/v2/internal/report"
)

// version is overridable at link time via -X main.version (see Makefile LDFLAGS),
// so it must stay a var: -X cannot patch a const.
var version = "2.8.0"

// stringSliceFlag supports repeated --flag value or --flag v1,v2,v3
type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Type() string   { return "stringSlice" }
func (s *stringSliceFlag) Set(value string) error {
	for _, v := range strings.Split(value, ",") {
		if v = strings.TrimSpace(v); v != "" {
			*s = append(*s, v)
		}
	}
	return nil
}

func main() {
	// ---- Flags (long and short) ----
	var queryDirs stringSliceFlag
	var sourceDirs stringSliceFlag
	var excludes stringSliceFlag
	var buildCommands []string
	var externals []string

	langs := pflag.StringP("langs", "l", "java,javascript,typescript,python",
		"Comma-separated languages to scan")
	dbRoot := pflag.StringP("db-root", "d", "cqlDB",
		"Root of CodeQL databases (<db-root>/<lang>/ or <db-root>/<project>/<lang>/)")
	pflag.VarP(&queryDirs, "query-dir", "q",
		"Query directory (repeat or comma-separate for multiple)")
	threads := pflag.IntP("threads", "t", 6, "Parallel worker goroutines")
	pflag.IntVar(threads, "parallel", 6, "")
	_ = pflag.CommandLine.MarkHidden("parallel")
	outputFormat := pflag.StringP("output-format", "o", "csv",
		"Output formats, comma-separated: all, csv, json, sarif, or a CodeQL format "+
			"(sarif-latest, sarifv2.1.0, dot, dgml, graphtext) passed straight through")
	severityFilter := pflag.StringP("severity", "s", "", "Filter findings by severity (e.g. HIGH, CRITICAL)")
	strictSeverity := pflag.BoolP("strict-severity", "S", false, "Exact severity match only (no loose mapping)")
	fancy := pflag.BoolP("fancy", "f", false, "Colored output and ASCII chart")
	dryRun := pflag.BoolP("dry-run", "n", false, "List actions without running CodeQL")
	packInstall := pflag.BoolP("pack-install", "P", false, "Run `codeql pack install` before scanning")
	allowMissingDB := pflag.BoolP("allow-missing-db", "m", false, "Continue if some DBs are missing/unfinalized")
	verbose := pflag.BoolP("verbose", "v", false, "Verbose command logging and error details")
	suiteOnly := pflag.BoolP("suite-only", "x", false, "Run only .qls suites, skip individual .ql files")

	// Lock handling
	unlockCache := pflag.BoolP("unlock-cache", "u", false, "Delete stale IMB cache .lock files")
	checkLockProc := pflag.BoolP("check-lock-process", "c", false, "Inspect PID inside any cache .lock file")
	killLockProc := pflag.BoolP("kill-lock-process", "k", false, "DANGER: kill -9 the PID found in the cache .lock")

	// DB automation
	autoFinalizeDB := pflag.BoolP("auto-finalize-db", "F", false, "Automatically finalize unfinalized DBs")
	autoInitDB := pflag.BoolP("auto-init-db", "i", false, "Create missing DBs (requires --source-root)")
	sourceRoot := pflag.StringP("source-root", "r", "", "Source root for --auto-init-db")
	forceScamUnready := pflag.BoolP("force-scan-unready", "R", false, "Scan DBs that appear empty/unusable")

	// DB build phase
	buildRoot := pflag.StringP("build-root", "b", "",
		"Parent directory of project source dirs; build a DB per child, then scan")
	pflag.Var(&sourceDirs, "source-dir",
		"Explicit project source dir to build a DB for (repeat or comma-separate)")
	dbPrefix := pflag.String("db-prefix", "", "Prefix for each project's DB directory name (e.g. cqlDB_eac-)")
	pflag.Var(&excludes, "exclude",
		"Glob of --build-root child names to skip (repeat or comma-separate; set to \"\" to disable)")
	buildOnly := pflag.Bool("build-only", false, "Build and finalize databases, then exit without analyzing")
	rebuildDB := pflag.Bool("rebuild-db", false, "Force rebuild of existing databases (codeql database create --overwrite)")
	noResolveSymlinks := pflag.Bool("no-resolve-symlinks", false,
		"Do not resolve symlinked source roots before handing them to CodeQL")
	// StringArrayVar, not the comma-splitting slice: a build command may well
	// contain a comma, and splitting it would silently corrupt the command.
	pflag.StringArrayVar(&buildCommands, "build-command", nil,
		"Build command for compiled languages, as codeql --command. "+
			"Prefix with a language (java=\"./build.sh android\") to bind it to that language. Repeat for several")
	buildMode := pflag.String("build-mode", "",
		"CodeQL build mode: none, autobuild, or manual (default: let CodeQL decide)")
	buildTimeout := pflag.Int("build-timeout", 0, "Per-database creation timeout in seconds (0 = no timeout)")
	buildThreads := pflag.IntP("build-threads", "B", 1, "Concurrent database builds")

	// Timeout
	queryTimeout := pflag.IntP("query-timeout", "T", 600, "Per-query timeout in seconds (0 = no timeout)")
	noTimeout := pflag.BoolP("no-timeout", "N", false, "Disable per-query timeout entirely")

	pflag.StringArrayVar(&externals, "external", nil,
		"External predicate data as predicate=file.csv, for queries that declare one (repeat for several)")

	showVersion := pflag.BoolP("version", "V", false, "Print version and exit")

	pflag.Parse()

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

	// ---- Normalize query dirs and languages ----
	dirs := normalizeQueryDirs([]string(queryDirs))
	langList := codeql.AliasLangs(strings.Split(*langs, ","))
	if len(langList) == 0 {
		fmt.Fprintln(os.Stderr, "No languages requested; exiting.")
		os.Exit(1)
	}

	outputPlan, err := codeql.NewOutputPlan(*outputFormat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	externalArgs, err := codeql.ParseExternals(externals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	globalCommands, perLangCommands := codeql.ParseBuildCommands(buildCommands)
	createOpts := codeql.CreateOptions{
		BuildMode:     *buildMode,
		BuildCommands: globalCommands,
		PerLang:       perLangCommands,
	}
	if err := createOpts.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	resolveSymlinks := !*noResolveSymlinks

	// An unset --exclude gets the built-in list; passing the flag replaces it
	// outright, and --exclude "" disables exclusion entirely.
	excludeList := []string(excludes)
	if !pflag.CommandLine.Changed("exclude") {
		excludeList = codeql.DefaultExcludes
	}

	// The failures log is shared by the build and scan phases, so it is
	// truncated once, before either runs.
	_ = os.WriteFile("hydraql_failures.log", nil, 0644)

	// ---- Pack install ----
	if *packInstall {
		installPacks(*fancy, *verbose)
	}

	// ---- Build phase ----
	buildCfg := &codeql.BuildConfig{
		BuildRoot:        *buildRoot,
		SourceDirs:       []string(sourceDirs),
		DBRoot:           *dbRoot,
		DBPrefix:         *dbPrefix,
		Exclude:          excludeList,
		Langs:            langList,
		CreateOptions:    createOpts,
		ResolveSymlinks:  resolveSymlinks,
		Rebuild:          *rebuildDB,
		AllowMissingDB:   *allowMissingDB,
		BuildTimeout:     *buildTimeout,
		BuildThreads:     *buildThreads,
		UnlockCache:      *unlockCache,
		CheckLockProcess: *checkLockProc,
		KillLockProcess:  *killLockProc,
		DryRun:           *dryRun,
		Verbose:          *verbose,
		Fancy:            *fancy,
	}

	if buildCfg.Requested() {
		fmt.Printf("[*] HydraQL discovering project source trees…\n")
		projects, err := codeql.DiscoverProjects(buildCfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		if len(projects) == 0 {
			fmt.Fprintln(os.Stderr, "No project source directories found; exiting.")
			os.Exit(1)
		}
		fmt.Printf("[*] Building %d database(s) under %s/ for %s\n",
			len(projects), *dbRoot, strings.Join(langList, ","))

		outcomes := codeql.BuildDatabases(context.Background(), projects, buildCfg)

		usable := 0
		for _, o := range outcomes {
			if o.OK() {
				usable++
			}
		}

		if *buildOnly {
			if *dryRun {
				fmt.Printf("Dry run: would build %d project(s).\n", len(projects))
				os.Exit(0)
			}
			fmt.Printf("Built usable databases for %d of %d project(s).\n", usable, len(projects))
			if usable < len(projects) && !*allowMissingDB {
				os.Exit(1)
			}
			os.Exit(0)
		}
		// A partially built project is still worth scanning, which is what
		// --allow-missing-db asks for. Only a total washout is fatal.
		if !*dryRun && usable == 0 {
			fmt.Fprintln(os.Stderr, "No scannable databases were produced; exiting.")
			os.Exit(1)
		}
	}

	// ---- Detect databases ----
	detectCfg := &codeql.DetectConfig{
		AutoFinalizeDB:   *autoFinalizeDB,
		AutoInitDB:       *autoInitDB,
		SourceRoot:       *sourceRoot,
		CreateOptions:    createOpts,
		ResolveSymlinks:  resolveSymlinks,
		AllowMissingDB:   *allowMissingDB,
		ForceScamUnready: *forceScamUnready,
		DryRun:           *dryRun,
		Verbose:          *verbose,
		Fancy:            *fancy,
		UnlockCache:      *unlockCache,
		CheckLockProcess: *checkLockProc,
		KillLockProcess:  *killLockProc,
	}
	targets, ok := codeql.DetectTargets(langList, *dbRoot, detectCfg)
	if !ok {
		// A dry run with a build phase has nothing on disk yet; the printed
		// build plan was the point of the run.
		if *dryRun && buildCfg.Requested() {
			fmt.Println("Dry run complete (databases were not created, so none could be detected).")
			os.Exit(0)
		}
		os.Exit(1)
	}
	if len(targets) == 0 {
		if *dryRun && buildCfg.Requested() {
			fmt.Println("Dry run complete (databases were not created, so none could be detected).")
			os.Exit(0)
		}
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

	graphQueries := codeql.CountGraphQueries(pairs)
	alertQueries := len(pairs) - graphQueries

	// Fail before any analysis rather than once per query: CodeQL's graph
	// writers reject a run with no @kind graph queries selected.
	if outputPlan.NeedsGraphQueries() && graphQueries == 0 {
		fmt.Fprintf(os.Stderr,
			"%s✖ A graph output format was requested, but none of the %d gathered queries is @kind graph.%s\n",
			report.Red, len(pairs), report.Reset)
		fmt.Fprintln(os.Stderr,
			"  CodeQL's dot, dgml, and graphtext writers only accept graph queries.")
		os.Exit(1)
	}
	if outputPlan.NeedsAlertQueries() && alertQueries == 0 {
		fmt.Fprintf(os.Stderr,
			"%s✖ No alert queries found; csv, json, and sarif need @kind problem or path-problem queries.%s\n",
			report.Red, report.Reset)
		os.Exit(1)
	}

	if *fancy {
		fmt.Printf("%s[*]%s Found %d queries (%d alert, %d graph) across %d database(s)\n",
			report.Magenta, report.Reset, len(pairs), alertQueries, graphQueries, len(targets))
		fmt.Printf("%s[*]%s Output: %s\n", report.Magenta, report.Reset, outputPlan.Describe())
	}

	// ---- Parallel execution ----
	tmpDir := "tmp_hydraql_output"
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create tmp dir: %v\n", err)
		os.Exit(1)
	}

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
		Externals:        externalArgs,
	}

	codeqlFormats := outputPlan.CodeQLFormats()
	results := make(chan codeql.QueryResult, len(pairs)*len(targets)*len(codeqlFormats))
	workerSem := make(chan struct{}, *threads)
	ctx := context.Background()

	// Each distinct CodeQL format is its own analyze pass, because
	// `database analyze --format` takes a single value. A pass only runs the
	// queries its writer can consume.
	queriesPerPass := map[string]int{}
	var wg sync.WaitGroup
	for _, cf := range codeqlFormats {
		wantGraph := codeql.IsGraphFormat(cf)
		for _, target := range targets {
			for _, pair := range pairs {
				if pair.Lang != target.Lang || pair.IsGraph() != wantGraph {
					continue
				}
				queriesPerPass[cf]++
				wg.Add(1)
				go func(q codeql.QueryPair, t codeql.ScanTarget, cf string) {
					defer wg.Done()
					workerSem <- struct{}{}
					defer func() { <-workerSem }()
					results <- codeql.RunQuery(ctx, q.Query, t, cf, tmpDir, runCfg)
				}(pair, target, cf)
			}
		}
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	// Group each pass's per-query files so every report merges only the files
	// written in its own format.
	inputsByPass := map[string][]merge.Input{}
	var perQueryCounts []int
	perProject := map[string]int{}
	timedOutCount := 0
	primaryTarget, hasPrimary := outputPlan.Primary()
	primaryPass := primaryTarget.CodeQLFormat
	for r := range results {
		if r.OutFile != "" {
			inputsByPass[r.CodeQLFormat] = append(inputsByPass[r.CodeQLFormat],
				merge.Input{Path: r.OutFile, Project: r.Project})
		}
		// Every pass finds the same things, so the summary counts one of them.
		if r.CodeQLFormat != primaryPass {
			continue
		}
		if r.TimedOut {
			timedOutCount++
		}
		if r.Project != "" {
			perProject[r.Project] += r.Findings
		}
		perQueryCounts = append(perQueryCounts, r.Findings)
	}

	// ---- Merge results, one report per requested format ----
	ts := time.Now().Format("20060102150405")
	totals := map[string]int{}
	var outputFiles []string
	var rawDirs []string

	for _, t := range outputPlan.Targets() {
		inputs := inputsByPass[t.CodeQLFormat]

		// Raw formats cannot be merged, so CodeQL's own per-query files are
		// collected into a directory instead.
		if t.Mode == codeql.OutputRaw {
			dir := fmt.Sprintf("HydraQL_output-%s-%s", ts, t.Name)
			n, err := merge.CollectRaw(inputs, dir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to collect %s output: %v\n", t.Name, err)
				os.Exit(1)
			}
			rawDirs = append(rawDirs, fmt.Sprintf("%s (%d file(s))", dir, n))
			continue
		}

		outputFile := fmt.Sprintf("HydraQL_output-%s.%s", ts, t.Name)
		var total int
		var mergeErr error
		switch {
		case t.Name == "csv" && t.Mode == codeql.OutputDerived:
			total, mergeErr = merge.MergeCSVFromSARIF(inputs, outputFile, *severityFilter, *strictSeverity)
		case t.Name == "csv":
			total, mergeErr = merge.MergeCSV(inputs, outputFile, *severityFilter, *strictSeverity)
		case t.Name == "json":
			total, mergeErr = merge.MergeJSON(inputs, outputFile, *severityFilter, *strictSeverity)
		default:
			total, mergeErr = merge.MergeSARIF(inputs, outputFile, *severityFilter, *strictSeverity)
		}
		if mergeErr != nil {
			fmt.Fprintf(os.Stderr, "Merge error for %s: %v\n", t.Name, mergeErr)
			os.Exit(1)
		}
		totals[t.Name] = total
		outputFiles = append(outputFiles, outputFile)
	}

	// ---- Summary ----
	primaryName := ""
	if hasPrimary {
		primaryName = primaryTarget.Name
	}
	report.PrintSummary(report.Summary{
		QueriesRun: queriesPerPass[primaryPass],
		Findings:   totals[primaryName],
		TimedOut:   timedOutCount,
		Databases:  len(targets),
		PerQuery:   perQueryCounts,
		PerProject: perProject,
	}, *fancy)

	for _, dir := range rawDirs {
		fmt.Printf("Raw CodeQL output in %s\n", dir)
	}
	for _, outputFile := range outputFiles {
		format := outputFile[strings.LastIndex(outputFile, ".")+1:]
		switch {
		case !*fancy:
			fmt.Printf("Results saved to %s (%d)\n", outputFile, totals[format])
		case totals[format] > 0:
			fmt.Printf("%s✅ %d finding(s) saved to %s%s\n",
				report.Green, totals[format], outputFile, report.Reset)
		default:
			fmt.Printf("%s⚠️  No results. Wrote header to %s%s\n",
				report.Yellow, outputFile, report.Reset)
		}
	}

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
			if tok = strings.TrimSpace(tok); tok != "" {
				out = append(out, codeql.ExpandHome(tok))
			}
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
			n := min(len(out), 800)
			fmt.Printf("%s", string(out[:n]))
		}
	}
}

func cleanTmp(dir string) {
	// A graph pass leaves directories behind, not just files.
	_ = os.RemoveAll(dir)
}
