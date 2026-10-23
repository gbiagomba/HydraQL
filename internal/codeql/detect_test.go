package codeql

import (
	"os"
	"path/filepath"
	"testing"
)

// writeDB creates a directory that passes DBStructureOK for the given language.
func writeDB(t *testing.T, dir, lang string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "db-"+lang, "default"), 0o755); err != nil {
		t.Fatal(err)
	}
	yml := "primaryLanguage: " + lang + "\nfinalised: true\n"
	if err := os.WriteFile(filepath.Join(dir, "codeql-database.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
}

// testDetectConfig skips the emptiness heuristic, which would otherwise reject
// these small synthetic databases.
func testDetectConfig() *DetectConfig {
	return &DetectConfig{ForceScamUnready: true, AllowMissingDB: true}
}

func TestDetectTargetsFlatLayout(t *testing.T) {
	root := t.TempDir()
	writeDB(t, filepath.Join(root, "javascript"), "javascript")
	writeDB(t, filepath.Join(root, "java"), "java")

	targets, ok := DetectTargets([]string{"javascript", "typescript", "java"}, root, testDetectConfig())
	if !ok {
		t.Fatal("DetectTargets reported failure")
	}
	if len(targets) != 2 {
		t.Fatalf("got %d targets, want 2: %+v", len(targets), targets)
	}
	for _, tgt := range targets {
		if tgt.Project != "" {
			t.Errorf("flat layout target %+v should have an empty Project", tgt)
		}
		if tgt.DBPath != filepath.Join(root, tgt.Lang) {
			t.Errorf("target %+v has an unexpected DBPath", tgt)
		}
	}
}

func TestDetectTargetsSingleDatabase(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cqlDB_eac")
	writeDB(t, root, "javascript")

	targets, ok := DetectTargets([]string{"javascript", "typescript"}, root, testDetectConfig())
	if !ok {
		t.Fatal("DetectTargets reported failure")
	}
	if len(targets) != 1 {
		t.Fatalf("got %d targets, want 1: %+v", len(targets), targets)
	}
	got := targets[0]
	if got.Project != "cqlDB_eac" || got.Lang != "javascript" || got.DBPath != root {
		t.Errorf("target = %+v, want project cqlDB_eac, lang javascript, db %s", got, root)
	}
}

func TestDetectTargetsSingleDatabaseLanguageNotRequested(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cqlDB_java")
	writeDB(t, root, "java")

	targets, ok := DetectTargets([]string{"javascript"}, root, testDetectConfig())
	if !ok {
		t.Fatal("DetectTargets reported failure")
	}
	if len(targets) != 0 {
		t.Fatalf("got %+v, want no targets for an unrequested language", targets)
	}
}

func TestDetectTargetsNestedProjects(t *testing.T) {
	root := t.TempDir()
	writeDB(t, filepath.Join(root, "accessibility-checker", "javascript"), "javascript")
	writeDB(t, filepath.Join(root, "pdf-export-core", "javascript"), "javascript")
	writeDB(t, filepath.Join(root, "pdf-export-core", "java"), "java")
	// Bookkeeping directories at cluster level must never become projects.
	if err := os.MkdirAll(filepath.Join(root, "log"), 0o755); err != nil {
		t.Fatal(err)
	}

	targets, ok := DetectTargets([]string{"javascript", "typescript", "java"}, root, testDetectConfig())
	if !ok {
		t.Fatal("DetectTargets reported failure")
	}
	if len(targets) != 3 {
		t.Fatalf("got %d targets, want 3: %+v", len(targets), targets)
	}

	byLabel := map[string]ScanTarget{}
	for _, tgt := range targets {
		byLabel[tgt.Label()] = tgt
	}
	for _, want := range []string{
		"accessibility-checker/javascript",
		"pdf-export-core/javascript",
		"pdf-export-core/java",
	} {
		if _, ok := byLabel[want]; !ok {
			t.Errorf("missing target %q; got %v", want, byLabel)
		}
	}
	// accessibility-checker has no java database; that is a soft skip, not a
	// failure, because projects legitimately differ in language.
	if _, ok := byLabel["accessibility-checker/java"]; ok {
		t.Error("accessibility-checker/java should not be a target")
	}
}

func TestDetectTargetsMissingDBFailsWithoutAllowFlag(t *testing.T) {
	root := t.TempDir()
	cfg := &DetectConfig{ForceScamUnready: true}
	if _, ok := DetectTargets([]string{"javascript"}, root, cfg); ok {
		t.Error("expected failure for a missing database without --allow-missing-db")
	}
}

func TestSingleDBLangFallsBackToMetadata(t *testing.T) {
	dir := t.TempDir()
	yml := "primaryLanguage: ruby\nfinalised: true\n"
	if err := os.WriteFile(filepath.Join(dir, "codeql-database.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := singleDBLang(dir); got != "ruby" {
		t.Errorf("singleDBLang = %q, want %q", got, "ruby")
	}
}

func TestDBFinalized(t *testing.T) {
	dir := t.TempDir()
	if dbFinalized(dir) {
		t.Error("a directory with no metadata must not report as finalized")
	}
	if err := os.WriteFile(filepath.Join(dir, "codeql-database.yml"), []byte("finalised: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dbFinalized(dir) {
		t.Error("finalised: false must not report as finalized")
	}
	if err := os.WriteFile(filepath.Join(dir, "codeql-database.yml"), []byte("finalised: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !dbFinalized(dir) {
		t.Error("finalised: true must report as finalized")
	}
}

// A multi-language cluster gets a directory per language even when a language
// has no code: `database create --db-cluster --language=javascript,python`
// against a JavaScript-only tree warns, exits 0, and leaves behind a python
// directory that has codeql-database.yml but no db-* subdirectory. That is not
// a scannable database, and treating it as one made every multi-language build
// depend on --allow-missing-db.
//
// Note the deliberate absence of AutoFinalizeDB: enabling it would make this
// test shell out to the real codeql binary, which is not installed on CI.
func TestDetectTargetsSkipsUnextractedClusterLanguage(t *testing.T) {
	root := t.TempDir()
	writeDB(t, filepath.Join(root, "jsonly", "javascript"), "javascript")

	// The shape CodeQL actually leaves behind: metadata, but nothing extracted.
	pythonDir := filepath.Join(root, "jsonly", "python")
	if err := os.MkdirAll(pythonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pythonDir, "codeql-database.yml"),
		[]byte("primaryLanguage: python\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// ForceScamUnready defeats the emptiness heuristic, so only the missing
	// db-* subdirectory can keep this directory out of the target list. No
	// AllowMissingDB either: the run must succeed on its own.
	cfg := &DetectConfig{ForceScamUnready: true}
	targets, ok := DetectTargets([]string{"javascript", "python"}, root, cfg)
	if !ok {
		t.Fatal("an unextracted cluster language must not fail the run without --allow-missing-db")
	}
	if len(targets) != 1 || targets[0].Label() != "jsonly/javascript" {
		t.Fatalf("targets = %+v, want only jsonly/javascript", targets)
	}
}
