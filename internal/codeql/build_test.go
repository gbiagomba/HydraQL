package codeql

import (
	"os"
	"path/filepath"
	"runtime"
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
	// Windows only permits unprivileged symlink creation with Developer Mode
	// on. GitHub's runners have it, but a contributor's machine may not, and a
	// test that cannot create its own fixture proves nothing either way.
	if err := os.Symlink(deepTarget, filepath.Join(buildRoot, "c")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("cannot create symlinks on this host: %v", err)
		}
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

func TestCreateOptionsArgs(t *testing.T) {
	cases := []struct {
		name string
		opts CreateOptions
		want []string
	}{
		{"empty", CreateOptions{}, nil},
		{"mode only", CreateOptions{BuildMode: "autobuild"}, []string{"--build-mode=autobuild"}},
		{
			"single command",
			CreateOptions{BuildCommands: []string{"./build.sh"}},
			[]string{"--command=./build.sh"},
		},
		{
			// A command containing a comma must survive intact, which is why
			// the flag is a string array rather than a comma-split slice.
			"command with comma and spaces",
			CreateOptions{BuildCommands: []string{"make a,b -j4"}},
			[]string{"--command=make a,b -j4"},
		},
		{
			"several commands keep their order",
			CreateOptions{BuildCommands: []string{"./configure", "make"}},
			[]string{"--command=./configure", "--command=make"},
		},
		{
			"manual mode with command",
			CreateOptions{BuildMode: "manual", BuildCommands: []string{"./build.sh"}},
			[]string{"--build-mode=manual", "--command=./build.sh"},
		},
	}
	for _, tc := range cases {
		got := tc.opts.args()
		if len(got) != len(tc.want) {
			t.Errorf("%s: args() = %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("%s: args() = %v, want %v", tc.name, got, tc.want)
				break
			}
		}
	}
}

func TestCreateOptionsValidate(t *testing.T) {
	ok := []CreateOptions{
		{},
		{BuildMode: "none"},
		{BuildMode: "autobuild"},
		{BuildMode: "manual", BuildCommands: []string{"./build.sh"}},
		{BuildCommands: []string{"./build.sh"}}, // a command implies manual
	}
	for _, o := range ok {
		if err := o.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v, want nil", o, err)
		}
	}

	bad := []CreateOptions{
		{BuildMode: "autobuilt"},
		{BuildMode: "NONE"}, // CodeQL's values are lowercase
		// Build mode none does not build the source root, so a build command
		// could never run.
		{BuildMode: "none", BuildCommands: []string{"./build.sh"}},
	}
	for _, o := range bad {
		if err := o.Validate(); err == nil {
			t.Errorf("Validate(%+v) = nil, want an error", o)
		}
	}
}

// The languages in the user's real invocation: typescript collapses into
// javascript and kotlin into java, both of which match CodeQL's own extractor
// set, and the trailing empty entry from a dangling comma is dropped.
func TestAliasLangsMixedCompiledAndInterpreted(t *testing.T) {
	got := AliasLangs([]string{"javascript", "typescript", "python", "kotlin", "cpp", "swift", ""})
	want := []string{"javascript", "python", "java", "cpp", "swift"}
	if len(got) != len(want) {
		t.Fatalf("AliasLangs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AliasLangs = %v, want %v", got, want)
		}
	}
}

// "--source-dir ." must be named after the directory it points at. Taking the
// basename of the literal "." yields nothing and used to fall back to the
// generic "project".
func TestDiscoverProjectsNamesDotSourceDirAfterItsDirectory(t *testing.T) {
	buildRoot, _ := fixture(t)
	target := filepath.Join(buildRoot, "a")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(target); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	cfg := &BuildConfig{
		SourceDirs:      []string{"."},
		DBRoot:          "cqlDB",
		Langs:           []string{"javascript"},
		ResolveSymlinks: true,
	}
	projects, err := DiscoverProjects(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("discovered %d projects, want 1", len(projects))
	}
	if projects[0].Name != "a" {
		t.Errorf("Name = %q, want %q", projects[0].Name, "a")
	}
	if projects[0].DBPath != filepath.Join("cqlDB", "a") {
		t.Errorf("DBPath = %q, want %q", projects[0].DBPath, filepath.Join("cqlDB", "a"))
	}
}
