package codeql

import (
	"os"
	"path/filepath"
	"testing"
)

// fixture builds a source tree shaped like the real reference case: a couple of
// real project directories, a symlink pointing into a separate tree, a stray
// file, a previously generated CodeQL database, and a dot-directory.
func fixture(t *testing.T) (buildRoot, deepTarget string) {
	t.Helper()
	tmp := t.TempDir()

	buildRoot = filepath.Join(tmp, "src")
	for _, name := range []string{"a", "b"} {
		dir := filepath.Join(buildRoot, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("//\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	deepTarget = filepath.Join(tmp, "elsewhere", "deep", "c")
	if err := os.MkdirAll(deepTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(deepTarget, filepath.Join(buildRoot, "c")); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(buildRoot, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	dbDir := filepath.Join(buildRoot, "cqlDB_x")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dbDir, "codeql-database.yml"), []byte("primaryLanguage: javascript\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(buildRoot, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	return buildRoot, deepTarget
}

func TestResolveSourceRootFollowsSymlink(t *testing.T) {
	buildRoot, deepTarget := fixture(t)

	want, err := filepath.EvalSymlinks(deepTarget)
	if err != nil {
		t.Fatal(err)
	}

	got, err := ResolveSourceRoot(filepath.Join(buildRoot, "c"), true)
	if err != nil {
		t.Fatalf("ResolveSourceRoot: %v", err)
	}
	if got != want {
		t.Errorf("resolved source root = %q, want %q", got, want)
	}
}

func TestResolveSourceRootDisabledKeepsLinkPath(t *testing.T) {
	buildRoot, deepTarget := fixture(t)

	got, err := ResolveSourceRoot(filepath.Join(buildRoot, "c"), false)
	if err != nil {
		t.Fatalf("ResolveSourceRoot: %v", err)
	}
	if got == deepTarget {
		t.Errorf("resolution was disabled but path was still resolved to %q", got)
	}
	if filepath.Base(got) != "c" {
		t.Errorf("unresolved path = %q, want it to end in /c", got)
	}
}

func TestResolveSourceRootRejectsFile(t *testing.T) {
	buildRoot, _ := fixture(t)
	if _, err := ResolveSourceRoot(filepath.Join(buildRoot, "build.sh"), true); err == nil {
		t.Error("expected an error for a regular file, got nil")
	}
}

func TestDiscoverProjects(t *testing.T) {
	buildRoot, deepTarget := fixture(t)

	wantDeep, err := filepath.EvalSymlinks(deepTarget)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &BuildConfig{
		BuildRoot:       buildRoot,
		DBRoot:          filepath.Join(t.TempDir(), "cqlDB"),
		Langs:           []string{"javascript"},
		ResolveSymlinks: true,
	}

	projects, err := DiscoverProjects(cfg)
	if err != nil {
		t.Fatalf("DiscoverProjects: %v", err)
	}

	var names []string
	byName := map[string]Project{}
	for _, p := range projects {
		names = append(names, p.Name)
		byName[p.Name] = p
	}
	if len(projects) != 3 {
		t.Fatalf("discovered %v, want exactly [a b c]", names)
	}
	for _, want := range []string{"a", "b", "c"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("project %q missing from %v", want, names)
		}
	}
	for _, unwanted := range []string{"build.sh", "cqlDB_x", ".hidden"} {
		if _, ok := byName[unwanted]; ok {
			t.Errorf("%q should not have been discovered as a project", unwanted)
		}
	}

	if got := byName["c"].SourceRoot; got != wantDeep {
		t.Errorf("c.SourceRoot = %q, want %q", got, wantDeep)
	}
	if got := byName["c"].DBPath; got != filepath.Join(cfg.DBRoot, "c") {
		t.Errorf("c.DBPath = %q, want %q", got, filepath.Join(cfg.DBRoot, "c"))
	}
}

func TestDiscoverProjectsExplicitSourceDirIsNotFiltered(t *testing.T) {
	buildRoot, _ := fixture(t)

	cfg := &BuildConfig{
		SourceDirs:      []string{filepath.Join(buildRoot, "cqlDB_x")},
		DBRoot:          "cqlDB",
		Langs:           []string{"javascript"},
		ResolveSymlinks: true,
	}
	projects, err := DiscoverProjects(cfg)
	if err != nil {
		t.Fatalf("DiscoverProjects: %v", err)
	}
	if len(projects) != 1 || projects[0].Name != "cqlDB_x" {
		t.Fatalf("explicit --source-dir was filtered out: %+v", projects)
	}
}

func TestDiscoverProjectsDBPrefix(t *testing.T) {
	buildRoot, _ := fixture(t)

	cfg := &BuildConfig{
		BuildRoot:       buildRoot,
		DBRoot:          "cqlDB",
		DBPrefix:        "cqlDB_eac-",
		Langs:           []string{"javascript"},
		ResolveSymlinks: true,
	}
	projects, err := DiscoverProjects(cfg)
	if err != nil {
		t.Fatalf("DiscoverProjects: %v", err)
	}
	for _, p := range projects {
		want := filepath.Join("cqlDB", "cqlDB_eac-"+p.Name)
		if p.DBPath != want {
			t.Errorf("%s DBPath = %q, want %q", p.Name, p.DBPath, want)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"accessibility-checker": "accessibility-checker",
		"my project":            "my-project",
		"weird/../name":         "weird-..-name",
		"...":                   "project",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClusterLangDirsSkipsBookkeeping(t *testing.T) {
	dbPath := t.TempDir()
	for _, name := range []string{"javascript", "java", "diagnostic", "log", "working", "results"} {
		if err := os.MkdirAll(filepath.Join(dbPath, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dirs := clusterLangDirs(dbPath)
	if len(dirs) != 2 {
		t.Fatalf("clusterLangDirs returned %v, want only the two language dirs", dirs)
	}
}

func TestAliasLangsDedupes(t *testing.T) {
	got := AliasLangs([]string{"javascript", "typescript", " java ", "kotlin", ""})
	want := []string{"javascript", "java"}
	if len(got) != len(want) {
		t.Fatalf("AliasLangs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AliasLangs = %v, want %v", got, want)
		}
	}
}

func TestCodeQLFormat(t *testing.T) {
	// CodeQL rejects "sarif" and has no JSON writer at all; both must be
	// requested as sarif-latest.
	cases := map[string]string{
		"csv":   "csv",
		"sarif": "sarif-latest",
		"json":  "sarif-latest",
	}
	for in, want := range cases {
		if got := CodeQLFormat(in); got != want {
			t.Errorf("CodeQLFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidOutputFormat(t *testing.T) {
	for _, ok := range []string{"csv", "json", "sarif"} {
		if !ValidOutputFormat(ok) {
			t.Errorf("ValidOutputFormat(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "xml", "sarif-latest", "CSV"} {
		if ValidOutputFormat(bad) {
			t.Errorf("ValidOutputFormat(%q) = true, want false", bad)
		}
	}
}
