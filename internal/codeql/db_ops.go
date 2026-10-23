package codeql

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/gbiagomba/hydraql/v2/internal/report"
)

func FinalizeDB(dbDir string, verbose bool) bool {
	cmd := exec.Command("codeql", "database", "finalize", dbDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		stderr := string(out)
		if strings.Contains(stderr, "already finalized") || strings.Contains(stderr, "already been finalized") {
			return true
		}
		if verbose {
			fmt.Printf("%s", stderr[:min(len(stderr), 800)])
		}
		fmt.Printf("%s⚠️  Failed to finalize DB %s%s\n", report.Yellow, dbDir, report.Reset)
		return false
	}
	return true
}

// InitDB creates a single-language database. sourceRoot is expected to be
// already resolved by ResolveSourceRoot; callers must not pass a raw symlink,
// or CodeQL will widen the extraction to the link targets' common ancestor.
func InitDB(dbDir, lang, sourceRoot string, opts CreateOptions, verbose bool) bool {
	args := append([]string{"database", "create", dbDir,
		"--language=" + lang, "--source-root=" + sourceRoot}, opts.args([]string{lang})...)
	cmd := exec.Command("codeql", args...)
	if verbose {
		fmt.Printf("   cmd: %s\n", strings.Join(cmd.Args, " "))
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if verbose {
			stderr := string(out)
			fmt.Printf("%s", stderr[:min(len(stderr), 800)])
		}
		fmt.Printf("%s⚠️  Failed to create DB %s for %s%s\n", report.Yellow, dbDir, lang, report.Reset)
		return false
	}
	return true
}
