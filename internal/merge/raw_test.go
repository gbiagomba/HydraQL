package merge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCollectRaw(t *testing.T) {
	src := t.TempDir()
	a := writeFile(t, src, "Query1_proj_javascript.dot", "digraph { a -> b }")
	b := writeFile(t, src, "Query2_proj_javascript.dot", "digraph { c -> d }")

	dir := filepath.Join(t.TempDir(), "HydraQL_output-ts-dot")
	n, err := CollectRaw([]Input{
		{Path: a, Project: "proj"},
		{Path: b, Project: "proj"},
		{Path: "", Project: "proj"}, // a failed query contributes no file
	}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("collected %d files, want 2", n)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("directory holds %d files, want 2", len(entries))
	}
	// Contents must survive untouched: the point of raw output is that it is
	// exactly what CodeQL wrote.
	got, err := os.ReadFile(filepath.Join(dir, "Query1_proj_javascript.dot"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "digraph { a -> b }" {
		t.Errorf("content = %q, want it unmodified", got)
	}
}

// With nothing to collect, no directory should be left behind.
func TestCollectRawEmptyCreatesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "unused")
	n, err := CollectRaw(nil, dir)
	if err != nil || n != 0 {
		t.Fatalf("CollectRaw(nil) = %d, %v", n, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("an empty collection must not create the directory")
	}
}

// A graph pass's per-query output is a directory, namespaced by rule id
// (CodeQL writes probe/js/print-ast.dot). The whole tree must survive the
// collection step.
func TestCollectRawCopiesDirectories(t *testing.T) {
	src := t.TempDir()
	queryOut := filepath.Join(src, "printAst_proj_javascript")
	if err := os.MkdirAll(filepath.Join(queryOut, "js"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(queryOut, "js", "print-ast.dot"),
		[]byte("digraph { a -> b }"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(t.TempDir(), "HydraQL_output-ts-dot")
	n, err := CollectRaw([]Input{{Path: queryOut, Project: "proj"}}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("collected %d files, want 1", n)
	}

	got, err := os.ReadFile(filepath.Join(dir, "printAst_proj_javascript", "js", "print-ast.dot"))
	if err != nil {
		t.Fatalf("rule namespacing was not preserved: %v", err)
	}
	if string(got) != "digraph { a -> b }" {
		t.Errorf("content = %q, want it unmodified", got)
	}
}

// A path that no longer exists means the query failed and wrote nothing.
func TestCollectRawSkipsMissingPaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	n, err := CollectRaw([]Input{{Path: "/nonexistent/path", Project: "p"}}, dir)
	if err != nil {
		t.Fatalf("CollectRaw = %v, want nil error", err)
	}
	if n != 0 {
		t.Errorf("collected %d, want 0", n)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("nothing to collect must not create the directory")
	}
}
