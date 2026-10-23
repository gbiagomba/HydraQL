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

// One group is a single cluster create. With several groups the largest
// clusterable group still shares one source scan, and only the command-bound
// languages are created individually. The cluster job must run first: a cluster
// --overwrite destroys sibling language directories, while a per-language
// --overwrite leaves them intact.
func TestPlanCreates(t *testing.T) {
	p := Project{Name: "src", SourceRoot: "/src", DBPath: "cqlDB_src"}

	t.Run("single group is one cluster", func(t *testing.T) {
		opts := CreateOptions{BuildCommands: []string{"./build.sh"}}
		langs := []string{"javascript", "java"}
		cfg := &BuildConfig{Langs: langs, CreateOptions: opts, Rebuild: true}
		jobs := planCreates(p, groupLangs(langs, opts), cfg)
		if len(jobs) != 1 || !jobs[0].cluster {
			t.Fatalf("want one cluster job, got %+v", jobs)
		}
		joined := strings.Join(jobs[0].args, " ")
		for _, want := range []string{"--db-cluster", "--language=javascript,java", "--command=./build.sh", "--overwrite"} {
			if !strings.Contains(joined, want) {
				t.Errorf("missing %q in %s", want, joined)
			}
		}
	})

	t.Run("mixed groups still cluster the shared ones", func(t *testing.T) {
		opts := CreateOptions{PerLang: map[string][]string{
			"java":  {"./build.sh build-android"},
			"swift": {"./build.sh build-ios"},
		}}
		langs := []string{"javascript", "python", "java", "swift"}
		cfg := &BuildConfig{Langs: langs, CreateOptions: opts, Rebuild: true}
		jobs := planCreates(p, groupLangs(langs, opts), cfg)

		// Four languages, three scans: javascript and python share one.
		if len(jobs) != 3 {
			t.Fatalf("want 3 create calls, got %d: %+v", len(jobs), jobs)
		}
		if !jobs[0].cluster {
			t.Error("the cluster job must run before any per-language --overwrite")
		}
		if strings.Join(jobs[0].langs, ",") != "javascript,python" {
			t.Errorf("cluster job langs = %v, want javascript,python", jobs[0].langs)
		}
		for _, j := range jobs[1:] {
			if j.cluster {
				t.Errorf("job %v should not be a cluster", j.langs)
			}
			lang := j.langs[0]
			if !strings.Contains(strings.Join(j.args, " "), filepath.Join("cqlDB_src", lang)) {
				t.Errorf("job for %s should target cqlDB_src/%s", lang, lang)
			}
		}
	})

	t.Run("all groups single means no cluster", func(t *testing.T) {
		opts := CreateOptions{PerLang: map[string][]string{"java": {"a"}, "swift": {"b"}}}
		langs := []string{"java", "swift"}
		cfg := &BuildConfig{Langs: langs, CreateOptions: opts}
		jobs := planCreates(p, groupLangs(langs, opts), cfg)
		if len(jobs) != 2 {
			t.Fatalf("want 2 jobs, got %d", len(jobs))
		}
		for _, j := range jobs {
			if j.cluster {
				t.Errorf("clustering a single language saves nothing; %v should be single", j.langs)
			}
		}
	})
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

// CodeQL does not follow symlinks found inside a source root and reports no
// error for them, so a tree whose code sits behind nested links yields a
// database that looks fine and is missing most of the source. Detecting them
// is the only way the gap becomes visible.
func TestFindNestedSymlinks(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "elsewhere", "pkg")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(root, "src")
	if err := os.MkdirAll(filepath.Join(src, "hz"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(src, "hz", "linked-pkg")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("cannot create symlinks on this host: %v", err)
		}
		t.Fatal(err)
	}
	// A symlink to a file hides no source tree and must be ignored.
	plainFile := filepath.Join(root, "elsewhere", "note.txt")
	if err := os.WriteFile(plainFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(plainFile, filepath.Join(src, "hz", "linked-file")); err != nil {
		t.Fatal(err)
	}
	// An excluded directory's contents must not be reported.
	if err := os.MkdirAll(filepath.Join(src, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(src, "node_modules", "dep")); err != nil {
		t.Fatal(err)
	}

	found, total := findNestedSymlinks(src, DefaultExcludes)
	if total != 1 {
		t.Fatalf("total = %d, want 1 (the directory symlink only): %v", total, found)
	}
	if len(found) != 1 || !strings.Contains(found[0], "linked-pkg") {
		t.Errorf("found = %v, want the linked-pkg entry", found)
	}
	if !strings.Contains(found[0], "->") {
		t.Errorf("found = %v, want it to name the resolved target", found)
	}
}

func TestFindNestedSymlinksCleanTree(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "b", "x.js"), []byte("//"), 0o644); err != nil {
		t.Fatal(err)
	}
	found, total := findNestedSymlinks(root, DefaultExcludes)
	if total != 0 || len(found) != 0 {
		t.Errorf("a tree with no symlinks reported %d: %v", total, found)
	}
}

// A cluster create exits non-zero when any single language fails, so "every
// create failed" must not be reported while a language was still salvaged.
// Doing so printed "javascript ready" and "every create failed" together.
func TestClassifyDoesNotClaimTotalFailureWhenSomethingSurvived(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "cqlDB_proj")
	writeDB(t, filepath.Join(dbPath, "javascript"), "javascript")
	// python got a directory but nothing extracted, as CodeQL leaves behind.
	if err := os.MkdirAll(filepath.Join(dbPath, "python"), 0o755); err != nil {
		t.Fatal(err)
	}

	p := Project{Name: "proj", DBPath: dbPath}
	cfg := &BuildConfig{Langs: []string{"javascript", "python"}}
	out := classify(p, cfg)

	if len(out.Usable) != 1 || out.Usable[0] != "javascript" {
		t.Fatalf("Usable = %v, want [javascript]", out.Usable)
	}
	if len(out.Empty) != 1 || out.Empty[0] != "python" {
		t.Fatalf("Empty = %v, want [python]", out.Empty)
	}

	// The same condition buildOne applies: one create, one failure, but a
	// usable language, so this is partial success rather than total failure.
	creates, failures := 1, 1
	out.AllCreatesFailed = creates > 0 && failures == creates && len(out.Usable) == 0
	if out.AllCreatesFailed {
		t.Error("AllCreatesFailed must be false when a language was salvaged")
	}
	if !out.OK() {
		t.Error("OK() should be true with one usable language")
	}
}

// An interpreted language with no database means the tree has no such code,
// which is a different thing from a compiled language whose build produced
// nothing, and the advice differs.
func TestNeedsBuild(t *testing.T) {
	for _, lang := range []string{"java", "kotlin", "cpp", "csharp", "go", "swift", "rust"} {
		if !NeedsBuild(lang) {
			t.Errorf("NeedsBuild(%q) = false, want true", lang)
		}
	}
	for _, lang := range []string{"javascript", "typescript", "python", "ruby", "actions"} {
		if NeedsBuild(lang) {
			t.Errorf("NeedsBuild(%q) = true, want false", lang)
		}
	}
}
