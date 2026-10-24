package codeql

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeSrc(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanSourceRootDetectsLanguages(t *testing.T) {
	root := t.TempDir()
	writeSrc(t, filepath.Join(root, "a.ts"), "const a = 1")
	writeSrc(t, filepath.Join(root, "b.tsx"), "const b = 2")
	writeSrc(t, filepath.Join(root, "sub", "c.js"), "//")
	writeSrc(t, filepath.Join(root, "d.py"), "x = 1")
	writeSrc(t, filepath.Join(root, "e.swift"), "let e = 1")
	writeSrc(t, filepath.Join(root, "README.md"), "docs")
	// Excluded directories must not contribute.
	writeSrc(t, filepath.Join(root, "node_modules", "dep", "x.js"), "//")
	writeSrc(t, filepath.Join(root, ".hidden", "y.py"), "y = 1")

	scan := ScanSourceRoot(root, DefaultExcludes)

	// TypeScript counts toward javascript, which is the extractor CodeQL uses.
	if got := scan.Languages["javascript"]; got != 3 {
		t.Errorf("javascript files = %d, want 3 (a.ts, b.tsx, sub/c.js)", got)
	}
	if got := scan.Languages["python"]; got != 1 {
		t.Errorf("python files = %d, want 1; node_modules and dot-dirs must be skipped", got)
	}
	if got := scan.Languages["swift"]; got != 1 {
		t.Errorf("swift files = %d, want 1", got)
	}
	if scan.Has("java") {
		t.Error("java should not be detected")
	}
	if scan.Truncated {
		t.Error("a small tree should not report truncation")
	}
}

func TestScanSourceRootFindsNestedSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "elsewhere", "pkg")
	writeSrc(t, filepath.Join(target, "x.ts"), "const x = 1")

	src := filepath.Join(root, "src")
	if err := os.MkdirAll(filepath.Join(src, "hz"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(src, "hz", "linked")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("cannot create symlinks here: %v", err)
		}
		t.Fatal(err)
	}

	scan := ScanSourceRoot(src, DefaultExcludes)
	if len(scan.Nested) != 1 {
		t.Fatalf("Nested = %+v, want one entry", scan.Nested)
	}
	if scan.Nested[0].Rel != filepath.Join("hz", "linked") {
		t.Errorf("Rel = %q", scan.Nested[0].Rel)
	}
	resolved, _ := filepath.EvalSymlinks(target)
	if scan.Nested[0].Target != resolved {
		t.Errorf("Target = %q, want %q", scan.Nested[0].Target, resolved)
	}
	// The linked file must not count toward the parent's languages, because
	// CodeQL will not see it from this root.
	if scan.Has("javascript") {
		t.Error("source behind a nested symlink must not count as present in the parent")
	}
}

func TestPresentLangs(t *testing.T) {
	scan := SourceScan{Languages: map[string]int{"javascript": 5, "swift": 2}}
	present, absent := scan.PresentLangs([]string{"javascript", "typescript", "python", "swift"})

	// typescript aliases to javascript, so it counts as present.
	if len(present) != 3 {
		t.Errorf("present = %v, want javascript, typescript, swift", present)
	}
	if len(absent) != 1 || absent[0] != "python" {
		t.Errorf("absent = %v, want [python]", absent)
	}
}

// A truncated scan never established absence, so nothing may be skipped.
func TestPresentLangsTruncatedKeepsEverything(t *testing.T) {
	scan := SourceScan{Languages: map[string]int{}, Truncated: true}
	present, absent := scan.PresentLangs([]string{"javascript", "java"})
	if len(present) != 2 || len(absent) != 0 {
		t.Errorf("present = %v, absent = %v; a truncated scan must keep all languages", present, absent)
	}
}
