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
		if strings.Contains(stderr, "already finalized") {
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

func InitDB(dbDir, lang, sourceRoot string, verbose bool) bool {
	cmd := exec.Command("codeql", "database", "create", dbDir,
		"--language="+lang, "--source-root", sourceRoot)
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
