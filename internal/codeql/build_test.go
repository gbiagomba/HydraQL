package codeql

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
		got := tc.opts.args(nil)
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

func TestParseBuildCommands(t *testing.T) {
	cases := []struct {
		name    string
		in      []string
		global  []string
		perLang map[string][]string
	}{
		{
			"plain command stays global",
			[]string{"./build.sh"},
			[]string{"./build.sh"}, nil,
		},
		{
			// "make CC" contains a space and is not a language, so the whole
			// value is a global command rather than a per-language binding.
			"command containing equals stays global",
			[]string{"make CC=gcc"},
			[]string{"make CC=gcc"}, nil,
		},
		{
			"language prefix binds to that language",
			[]string{`java=./build.sh build-android`},
			nil, map[string][]string{"java": {"./build.sh build-android"}},
		},
		{
			// kotlin resolves to the java extractor.
			"language alias is resolved",
			[]string{`kotlin=./build.sh build-android`},
			nil, map[string][]string{"java": {"./build.sh build-android"}},
		},
		{
			"global and per-language can be mixed",
			[]string{"./common.sh", `swift=./build.sh build-ios`},
			[]string{"./common.sh"}, map[string][]string{"swift": {"./build.sh build-ios"}},
		},
		{
			"several commands for one language keep their order",
			[]string{`java=./configure`, `java=make`},
			nil, map[string][]string{"java": {"./configure", "make"}},
		},
		{
			"an unknown prefix is not a language",
			[]string{"gradlew=assembleDebug"},
			[]string{"gradlew=assembleDebug"}, nil,
		},
	}

	for _, tc := range cases {
		global, perLang := ParseBuildCommands(tc.in)
		if strings.Join(global, "|") != strings.Join(tc.global, "|") {
			t.Errorf("%s: global = %v, want %v", tc.name, global, tc.global)
		}
		if len(perLang) != len(tc.perLang) {
			t.Errorf("%s: perLang = %v, want %v", tc.name, perLang, tc.perLang)
			continue
		}
		for lang, want := range tc.perLang {
			if strings.Join(perLang[lang], "|") != strings.Join(want, "|") {
				t.Errorf("%s: perLang[%s] = %v, want %v", tc.name, lang, perLang[lang], want)
			}
		}
	}
}

func TestCreateOptionsCommandsFor(t *testing.T) {
	opts := CreateOptions{
		BuildCommands: []string{"./global.sh"},
		PerLang:       map[string][]string{"java": {"./build.sh build-android"}},
	}
	if got := opts.CommandsFor("java"); strings.Join(got, "|") != "./build.sh build-android" {
		t.Errorf("CommandsFor(java) = %v", got)
	}
	// kotlin aliases to java, so it picks up java's command.
	if got := opts.CommandsFor("kotlin"); strings.Join(got, "|") != "./build.sh build-android" {
		t.Errorf("CommandsFor(kotlin) = %v", got)
	}
	// Anything not bound falls back to the global list.
	if got := opts.CommandsFor("swift"); strings.Join(got, "|") != "./global.sh" {
		t.Errorf("CommandsFor(swift) = %v", got)
	}
	if !opts.HasPerLang() {
		t.Error("HasPerLang() = false")
	}
}

// Languages sharing a command list build together; a language with its own
// command needs its own invocation.
func TestGroupLangs(t *testing.T) {
	langs := []string{"javascript", "python", "java", "swift"}
	opts := CreateOptions{
		PerLang: map[string][]string{
			"java":  {"./build.sh build-android"},
			"swift": {"./build.sh build-ios"},
		},
	}
	groups := groupLangs(langs, opts)
	if len(groups) != 3 {
		t.Fatalf("got %d groups, want 3: %+v", len(groups), groups)
	}
	if strings.Join(groups[0].langs, ",") != "javascript,python" {
		t.Errorf("group 0 = %v, want javascript,python sharing no command", groups[0].langs)
	}
	if strings.Join(groups[1].langs, ",") != "java" {
		t.Errorf("group 1 = %v, want java alone", groups[1].langs)
	}
	if strings.Join(groups[2].langs, ",") != "swift" {
		t.Errorf("group 2 = %v, want swift alone", groups[2].langs)
	}

	// One shared command means one group, which is the existing behavior.
	single := groupLangs(langs, CreateOptions{BuildCommands: []string{"./build.sh"}})
	if len(single) != 1 || len(single[0].langs) != 4 {
		t.Errorf("a single global command must yield one group of all languages, got %+v", single)
	}
	if len(groupLangs(langs, CreateOptions{})) != 1 {
		t.Error("no commands at all must also yield one group")
	}
}

// One group uses --db-cluster; several groups create each language separately,
// because --overwrite against a cluster root guts its sibling languages.
func TestCreateArgsShapePerGroupCount(t *testing.T) {
	p := Project{Name: "src", SourceRoot: "/src", DBPath: "cqlDB_src"}

	single := CreateOptions{BuildCommands: []string{"./build.sh"}}
	groups := groupLangs([]string{"javascript", "java"}, single)
	cfg := &BuildConfig{Langs: []string{"javascript", "java"}, CreateOptions: single, Rebuild: true}
	args := createArgs(p, groups[0], groups, cfg)
	if len(args) != 1 {
		t.Fatalf("one group should yield one invocation, got %d", len(args))
	}
	joined := strings.Join(args[0], " ")
	for _, want := range []string{"--db-cluster", "--language=javascript,java", "--command=./build.sh", "--overwrite", "cqlDB_src"} {
		if !strings.Contains(joined, want) {
			t.Errorf("cluster args missing %q: %s", want, joined)
		}
	}

	multi := CreateOptions{PerLang: map[string][]string{"java": {"./build.sh build-android"}}}
	mgroups := groupLangs([]string{"javascript", "java"}, multi)
	mcfg := &BuildConfig{Langs: []string{"javascript", "java"}, CreateOptions: multi, Rebuild: true}
	if len(mgroups) != 2 {
		t.Fatalf("got %d groups, want 2", len(mgroups))
	}
	for i, g := range mgroups {
		got := createArgs(p, g, mgroups, mcfg)
		if len(got) != 1 {
			t.Fatalf("group %d yielded %d invocations, want 1", i, len(got))
		}
		joined := strings.Join(got[0], " ")
		if strings.Contains(joined, "--db-cluster") {
			t.Errorf("group %d must not use --db-cluster: %s", i, joined)
		}
		lang := g.langs[0]
		if !strings.Contains(joined, filepath.Join("cqlDB_src", lang)) {
			t.Errorf("group %d should target cqlDB_src/%s: %s", i, lang, joined)
		}
	}
}

// A per-language create targets <DBPath>/<lang>, so <DBPath> itself must exist
// first. CodeQL refuses with "Cannot create database at ... because ... does
// not exist", which is how every language in a multi-group build once failed.
func TestBuildOneCreatesDBRootForPerLanguageBuilds(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(root, "cqlDB_src")
	p := Project{Name: "src", SourceRoot: src, DBPath: dbPath}
	cfg := &BuildConfig{
		Langs: []string{"javascript", "java"},
		CreateOptions: CreateOptions{
			PerLang: map[string][]string{"java": {"./build.sh build-android"}},
		},
		// DryRun would return before the directory work; a real run needs
		// codeql, so this asserts on the directory alone by letting the
		// creates fail afterwards.
		BuildTimeout: 1,
	}

	if len(groupLangs(cfg.Langs, cfg.CreateOptions)) < 2 {
		t.Fatal("fixture should produce more than one build group")
	}

	_ = buildOne(context.Background(), p, cfg)

	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("db-root %s was not created: %v", dbPath, err)
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory", dbPath)
	}
}

// A single group is a cluster create, which creates its own root, so only the
// parent needs to exist.
func TestBuildOneCreatesParentForClusterBuilds(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(root, "nested", "cqlDB_src")
	p := Project{Name: "src", SourceRoot: src, DBPath: dbPath}
	cfg := &BuildConfig{Langs: []string{"javascript"}, BuildTimeout: 1}

	_ = buildOne(context.Background(), p, cfg)

	if _, err := os.Stat(filepath.Dir(dbPath)); err != nil {
		t.Fatalf("parent of db-root was not created: %v", err)
	}
}

func TestBuildOutcomeCauseAttribution(t *testing.T) {
	// Nothing created at all must not be reported as a build-command problem.
	failed := BuildOutcome{
		Project:          Project{Name: "src"},
		Empty:            []string{"javascript"},
		AllCreatesFailed: true,
	}
	if failed.OK() {
		t.Error("OK() should be false with no usable languages")
	}

	partial := BuildOutcome{
		Project: Project{Name: "src"},
		Usable:  []string{"javascript"},
		Empty:   []string{"cpp"},
	}
	if !partial.OK() {
		t.Error("OK() should be true when at least one language is usable")
	}
	if partial.AllCreatesFailed {
		t.Error("a partial build did not have all creates fail")
	}
}
