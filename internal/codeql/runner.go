package codeql

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gbiagomba/hydraql/v2/internal/merge"
	"github.com/gbiagomba/hydraql/v2/internal/report"
)

// Per-DB serialization to prevent IMB cache lock races.
var (
	dbLocksMu sync.Mutex
	dbLocks   = map[string]chan struct{}{}
)

func getDBLock(dbPath string) chan struct{} {
	dbLocksMu.Lock()
	defer dbLocksMu.Unlock()
	if ch, ok := dbLocks[dbPath]; ok {
		return ch
	}
	ch := make(chan struct{}, 1)
	ch <- struct{}{} // one token = one concurrent user allowed
	dbLocks[dbPath] = ch
	return ch
}

func acquireDB(dbPath string) { <-getDBLock(dbPath) }
func releaseDB(dbPath string) { getDBLock(dbPath) <- struct{}{} }

// startProgress begins a progress line and returns a function that stops it,
// blocking until the line has been cleared. Without that handshake the clear
// sequence races the caller's next Printf and garbles the output.
func startProgress(label string) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		progressTicker(label, stop)
	}()
	return func() {
		close(stop)
		<-done
	}
}

func progressTicker(label string, stop <-chan struct{}) {
	start := time.Now()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			fmt.Fprintf(os.Stderr, "\r%s\r", strings.Repeat(" ", 72))
			return
		case <-ticker.C:
			elapsed := int(time.Since(start).Seconds())
			fmt.Fprintf(os.Stderr, "\r  ⏳ %s … %ds elapsed", label, elapsed)
		}
	}
}

// RunConfig holds per-query execution options.
type RunConfig struct {
	Verbose          bool
	Fancy            bool
	DryRun           bool
	QueryTimeout     int // seconds; 0 = no timeout
	AutoFinalizeDB   bool
	UnlockCache      bool
	CheckLockProcess bool
	KillLockProcess  bool
	SeverityFilter   string
	StrictSeverity   bool
}

// QueryResult is the output of a single RunQuery call.
type QueryResult struct {
	Project      string
	CodeQLFormat string
	OutFile      string
	Findings     int
	TimedOut     bool
	Failed       bool
}

// RunQuery executes one CodeQL query with timeout, progress ticker, and retry.
func RunQuery(ctx context.Context, query string, target ScanTarget, codeqlFormat, tmpDir string, cfg *RunConfig) QueryResult {
	lang, dbPath := target.Lang, target.DBPath

	// The project belongs in the temp filename: without it, two projects'
	// results for the same query would overwrite each other in tmpDir.
	stem := strings.TrimSuffix(filepath.Base(query), filepath.Ext(query))
	parts := []string{stem}
	if target.Project != "" {
		parts = append(parts, sanitizeName(target.Project))
	}
	parts = append(parts, lang)
	outFile := filepath.Join(tmpDir, strings.Join(parts, "_")+"."+TempExt(codeqlFormat))
	label := fmt.Sprintf("[%s] %s", target.Label(), filepath.Base(query))

	if cfg.Fancy {
		fmt.Printf("%s🌐 %s%s\n", report.Cyan, label, report.Reset)
	}

	stopProgress := startProgress(label)
	tStart := time.Now()

	var queryCtx context.Context
	var cancel context.CancelFunc
	if cfg.QueryTimeout > 0 {
		queryCtx, cancel = context.WithTimeout(ctx, time.Duration(cfg.QueryTimeout)*time.Second)
	} else {
		queryCtx, cancel = context.WithCancel(ctx)
	}

	result := func() QueryResult {
		defer cancel()

		acquireDB(dbPath)
		defer releaseDB(dbPath)

		// Pre-query lock sweep
		HandleCacheLocks(dbPath, cfg.UnlockCache, cfg.CheckLockProcess, cfg.KillLockProcess, cfg.Verbose)

		ok, stderr, timedOut := analyzeOnce(queryCtx, query, dbPath, codeqlFormat, outFile, cfg.Verbose)

		if timedOut {
			elapsed := int(time.Since(tStart).Seconds())
			appendLog("hydraql_failures.log", fmt.Sprintf("TIMEOUT after %ds: %s on %s\n", elapsed, query, lang))
			if cfg.Fancy {
				fmt.Printf("\n%s⏱  Timed out after %ds:%s %s\n", report.Yellow, elapsed, report.Reset, filepath.Base(query))
			} else {
				fmt.Printf("TIMEOUT after %ds: %s\n", elapsed, label)
			}
			return QueryResult{TimedOut: true}
		}

		if !ok {
			retried := false
			if strings.Contains(stderr, "needs to be finalized") && cfg.AutoFinalizeDB && !cfg.DryRun {
				fmt.Printf("  ↻ Finalizing %s DB (auto) due to analyze error, then retry…\n", lang)
				FinalizeDB(dbPath, cfg.Verbose)
				HandleCacheLocks(dbPath, cfg.UnlockCache, cfg.CheckLockProcess, cfg.KillLockProcess, cfg.Verbose)
				retried = true
			}
			if strings.Contains(stderr, "cache directory is already locked") ||
				strings.Contains(stderr, "OverlappingFileLockException") {
				fmt.Printf("  ↻ Clearing IMB locks for %s DB, then retry…\n", lang)
				DeleteLocks(dbPath, cfg.Verbose)
				retried = true
			}
			if retried {
				time.Sleep(250 * time.Millisecond)
				ok, stderr, timedOut = analyzeOnce(queryCtx, query, dbPath, codeqlFormat, outFile, cfg.Verbose)
				if timedOut {
					elapsed := int(time.Since(tStart).Seconds())
					appendLog("hydraql_failures.log", fmt.Sprintf("TIMEOUT after %ds: %s on %s\n", elapsed, query, lang))
					if cfg.Fancy {
						fmt.Printf("\n%s⏱  Timed out after %ds:%s %s\n", report.Yellow, elapsed, report.Reset, filepath.Base(query))
					} else {
						fmt.Printf("TIMEOUT after %ds: %s\n", elapsed, label)
					}
					return QueryResult{TimedOut: true}
				}
			}
		}

		if !ok {
			appendLog("hydraql_failures.log", fmt.Sprintf("FAIL %s on %s:\n%s\n", query, lang, stderr))
			if cfg.Verbose {
				fmt.Printf("%s❌ Query failed:%s %s\n", report.Red, report.Reset, query)
				tail := stderr
				if len(tail) > 600 {
					tail = tail[:600] + "..."
				}
				fmt.Println(tail)
			}
			return QueryResult{Failed: true}
		}

		if _, err := os.Stat(outFile); os.IsNotExist(err) {
			return QueryResult{Failed: true}
		}

		findings := countFindings(outFile, codeqlFormat, cfg.SeverityFilter, cfg.StrictSeverity)
		return QueryResult{OutFile: outFile, Findings: findings}
	}()

	stopProgress()
	result.Project = target.Project
	result.CodeQLFormat = codeqlFormat
	return result
}

func analyzeOnce(ctx context.Context, query, dbPath, format, outFile string, verbose bool) (ok bool, stderr string, timedOut bool) {
	cmd := exec.CommandContext(ctx, "codeql", "database", "analyze",
		dbPath, "--format", format, "--output", outFile, query)
	if verbose {
		fmt.Printf("   cmd: %s\n", strings.Join(cmd.Args, " "))
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	err := cmd.Run()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return false, "", true
		}
		return false, stderrBuf.String(), false
	}
	return true, "", false
}

func countFindings(path, codeqlFormat, severityFilter string, strict bool) int {
	if codeqlFormat == "csv" {
		return merge.CountCSV(path, severityFilter, strict)
	}
	return merge.CountSARIF(path, severityFilter, strict)
}

func appendLog(logFile, msg string) {
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(msg)
}
