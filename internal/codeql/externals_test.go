package codeql

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseExternals(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "files.csv")
	if err := os.WriteFile(csvPath, []byte("src/a.js\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ParseExternals([]string{
		"selectedSourceFile=" + csvPath,
		"  otherPred = " + csvPath + "  ", // surrounding space is tolerated
		"",                                // skipped
	})
	if err != nil {
		t.Fatalf("ParseExternals: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %v, want 2 entries", got)
	}
	if got[0] != "selectedSourceFile="+csvPath {
		t.Errorf("got[0] = %q", got[0])
	}
	if got[1] != "otherPred="+csvPath {
		t.Errorf("got[1] = %q, want the trimmed form", got[1])
	}
}

func TestParseExternalsRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "files.csv")
	if err := os.WriteFile(csvPath, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"noEquals":                "predicate=file.csv", // missing separator
		"=" + csvPath:             "predicate=file.csv", // empty predicate
		"pred=":                   "predicate=file.csv", // empty path
		"pred=" + dir:             "is a directory",     // a directory, not a file
		"pred=" + dir + "/absent": "cannot read",        // missing file
	}
	for input, wantFragment := range cases {
		_, err := ParseExternals([]string{input})
		if err == nil {
			t.Errorf("ParseExternals(%q) = nil error, want a rejection", input)
			continue
		}
		if !strings.Contains(err.Error(), wantFragment) {
			t.Errorf("ParseExternals(%q) error %q should mention %q", input, err, wantFragment)
		}
	}
}

func TestParseExternalsExpandsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	// Any file that certainly exists under the home directory.
	probe := filepath.Join(home, ".hydraql_external_probe.csv")
	if err := os.WriteFile(probe, []byte("x\n"), 0o644); err != nil {
		t.Skipf("cannot write probe file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(probe) })

	got, err := ParseExternals([]string{"pred=~/.hydraql_external_probe.csv"})
	if err != nil {
		t.Fatalf("ParseExternals: %v", err)
	}
	if got[0] != "pred="+probe {
		t.Errorf("got %q, want the expanded path %q", got[0], "pred="+probe)
	}
}
