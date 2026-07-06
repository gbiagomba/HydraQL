package codeql

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gbiagomba/hydraql/v2/internal/report"
)

var lockPIDRe = regexp.MustCompile(`(?i)pid\s*=\s*(\d+)|^(\d+)$`)

func findAllLocks(dbDir string) []string {
	var locks []string
	direct := filepath.Join(dbDir, "default", "cache", ".lock")
	if _, err := os.Stat(direct); err == nil {
		locks = append(locks, direct)
	}
	_ = filepath.WalkDir(dbDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Name() == ".lock" && strings.HasSuffix(filepath.Dir(path), filepath.Join("cache")) {
			for _, existing := range locks {
				if existing == path {
					return nil
				}
			}
			locks = append(locks, path)
		}
		return nil
	})
	return locks
}

func readLockPID(lockFile string) int {
	data, err := os.ReadFile(lockFile)
	if err != nil {
		return 0
	}
	m := lockPIDRe.FindStringSubmatch(string(data))
	for _, g := range m[1:] {
		if g != "" {
			pid, _ := strconv.Atoi(g)
			return pid
		}
	}
	return 0
}

func processRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds; signal 0 checks existence.
	err = proc.Signal(os.Signal(nil))
	return err == nil
}

func forceDeleteLock(lockFile string, verbose bool) bool {
	if err := os.Remove(lockFile); err == nil {
		if verbose {
			fmt.Printf("   Removed lock: %s\n", lockFile)
		}
		return true
	}
	// chmod + retry
	_ = os.Chmod(lockFile, 0o666)
	if err := os.Remove(lockFile); err == nil {
		if verbose {
			fmt.Printf("   Chmod+Removed lock: %s\n", lockFile)
		}
		return true
	}
	// rename as fallback
	stale := lockFile + ".stale"
	if err := os.Rename(lockFile, stale); err == nil {
		if verbose {
			fmt.Printf("   Renamed lock to: %s\n", stale)
		}
		return true
	}
	fmt.Printf("%s⚠️  Could not remove lock %s%s\n", report.Yellow, lockFile, report.Reset)
	return false
}

// HandleCacheLocks inspects and optionally removes IMB cache lock files.
func HandleCacheLocks(dbDir string, unlockCache, checkLockProcess, killLockProcess, verbose bool) {
	locks := findAllLocks(dbDir)
	if len(locks) == 0 {
		return
	}
	fmt.Printf("%s⚠️  IMB cache lock(s) detected under:%s %s\n", report.Yellow, report.Reset, dbDir)
	for _, lock := range locks {
		fmt.Printf("   • %s\n", lock)
		pid := readLockPID(lock)
		if checkLockProcess && pid > 0 {
			running := processRunning(pid)
			state := "NOT running"
			if running {
				state = "RUNNING"
			}
			fmt.Printf("     PID=%d is %s\n", pid, state)
		}
		if killLockProcess && pid > 0 {
			fmt.Printf("%s⚠ Killing PID %d from lock...%s\n", report.Red, pid, report.Reset)
			proc, err := os.FindProcess(pid)
			if err == nil {
				_ = proc.Kill()
			}
		}
		if unlockCache {
			forceDeleteLock(lock, verbose)
		}
	}
}

// DeleteLocks is a forced aggressive unlock used on retry.
func DeleteLocks(dbDir string, verbose bool) {
	for _, lock := range findAllLocks(dbDir) {
		forceDeleteLock(lock, verbose)
	}
}
