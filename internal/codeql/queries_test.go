package codeql

import (
	"os"
	"path/filepath"
	"testing"
)

// writeQuery creates a .ql file with the given metadata header.
func writeQuery(t *testing.T, dir, name, kind string) {
	t.Helper()
	body := "/**\n * @name Example\n"
	if kind != "" {
		body += " * @kind " + kind + "\n"
	}
	body += " */\n\nimport javascript\n\nselect 1\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGatherQueriesReadsKind(t *testing.T) {
	dir := t.TempDir()
	writeQuery(t, dir, "problem.ql", "problem")
	writeQuery(t, dir, "path.ql", "path-problem")
	writeQuery(t, dir, "graph.ql", "graph")
	writeQuery(t, dir, "nokind.ql", "")

	pairs := GatherQueries([]string{dir}, []string{"javascript"}, false, false)
	if len(pairs) != 4 {
		t.Fatalf("gathered %d queries, want 4", len(pairs))
	}

	byBase := map[string]QueryPair{}
	for _, p := range pairs {
		byBase[filepath.Base(p.Query)] = p
	}
	for name, want := range map[string]string{
		"problem.ql": "problem",
		"path.ql":    "path-problem",
		"graph.ql":   "graph",
		"nokind.ql":  "",
	} {
		if got := byBase[name].Kind; got != want {
			t.Errorf("%s Kind = %q, want %q", name, got, want)
		}
	}

	if !byBase["graph.ql"].IsGraph() {
		t.Error("graph.ql should report IsGraph")
	}
	if byBase["problem.ql"].IsGraph() {
		t.Error("problem.ql should not report IsGraph")
	}
	if got := CountGraphQueries(pairs); got != 1 {
		t.Errorf("CountGraphQueries = %d, want 1", got)
	}
}
