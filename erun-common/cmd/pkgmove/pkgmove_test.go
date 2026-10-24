package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixture lays out a module whose root package is about to be split.
func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files["go.mod"] = "module example.com/fixture\n\ngo 1.26\n"
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runPkgmove(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := run(append(args, "-dir", dir, "-module", "example.com/fixture"), &out)
	return out.String(), err
}

func goIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}

// TestApplyMovesAFileAndExportsWhatCrossesTheBoundary moves one file into its
// own package: the helper the root still calls is exported, its doc comment
// follows the new name, the root refers to it through an import, and a root
// test's helper that a moved test needs is copied rather than exported -- a
// _test.go declaration cannot be. The result has to build and pass its tests.
func TestApplyMovesAFileAndExportsWhatCrossesTheBoundary(t *testing.T) {
	dir := writeFixture(t, map[string]string{
		"root.go":        "package fixture\n\n// Run adds one to the helper.\nfunc Run() int { return helper() + 1 }\n",
		"helper.go":      "package fixture\n\n// helper doubles Value.\nfunc helper() int { return Value * 2 }\n\n// Value is what helper doubles.\nvar Value = 2\n",
		"helper_test.go": "package fixture\n\nimport \"testing\"\n\nfunc TestHelper(t *testing.T) {\n\tif helper() != want() {\n\t\tt.Fatal(helper())\n\t}\n}\n",
		"root_test.go":   "package fixture\n\nimport \"testing\"\n\nfunc want() int { return 4 }\n\nfunc TestRun(t *testing.T) {\n\tif Run() != want()+1 {\n\t\tt.Fatal(Run())\n\t}\n}\n",
		"packages.map":   "base/sub helper.go\n",
	})
	if out, err := runPkgmove(t, dir, "apply", "-map", filepath.Join(dir, "packages.map")); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}

	moved := readFile(t, filepath.Join(dir, "base", "sub", "helper.go"))
	for _, want := range []string{"package sub", "// Helper doubles Value.", "func Helper() int"} {
		if !strings.Contains(moved, want) {
			t.Errorf("moved file is missing %q:\n%s", want, moved)
		}
	}
	root := readFile(t, filepath.Join(dir, "root.go"))
	for _, want := range []string{`"example.com/fixture/base/sub"`, "sub.Helper()"} {
		if !strings.Contains(root, want) {
			t.Errorf("root file is missing %q:\n%s", want, root)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "helper.go")); !os.IsNotExist(err) {
		t.Errorf("the moved file is still in the root package (err=%v)", err)
	}
	copied := readFile(t, filepath.Join(dir, "base", "sub", "root_test.go"))
	if !strings.Contains(copied, "func want() int") || strings.Contains(copied, "func TestRun") {
		t.Errorf("the moved test needs the root's want helper copied, and only it:\n%s", copied)
	}
	goIn(t, dir, "vet", "./...")
	goIn(t, dir, "test", "./...")

	requireReapplyChangesNothing(t, dir)
}

// requireReapplyChangesNothing re-runs a map on the tree it already moved: a
// branch that predates a move re-runs that map after rebasing onto it, so on
// a moved tree it has to change nothing.
func requireReapplyChangesNothing(t *testing.T, dir string) {
	t.Helper()
	out, err := runPkgmove(t, dir, "apply", "-map", filepath.Join(dir, "packages.map"))
	if err != nil || !strings.Contains(out, "rewrote 0 files") {
		t.Fatalf("re-applying the map to the moved tree must change nothing: %v\n%s", err, out)
	}
}

// TestPlanRefusesAMapThatMakesAnImportCycle pins the gate apply relies on:
// two files that call each other cannot be put in different packages.
func TestPlanRefusesAMapThatMakesAnImportCycle(t *testing.T) {
	dir := writeFixture(t, map[string]string{
		"a.go":         "package fixture\n\nfunc A() int { return B() }\n\nfunc C() int { return 1 }\n",
		"b.go":         "package fixture\n\nfunc B() int { return C() }\n",
		"packages.map": "one a.go\ntwo b.go\n",
	})
	out, err := runPkgmove(t, dir, "plan", "-map", filepath.Join(dir, "packages.map"))
	if err == nil {
		t.Fatalf("plan accepted a map that makes an import cycle:\n%s", out)
	}
	if !strings.Contains(out, "cycle: one, two") {
		t.Fatalf("plan must name the cycle, got:\n%s", out)
	}
}

func TestFileDirFollowsTheFirstMatchingRuleAndTestsFollowTheirSource(t *testing.T) {
	m, err := parseMap(strings.NewReader("# comment\nbase/core exec.go\nbase/core exec_*.go\ncmd/x x.go  # trailing\nbase/model a.go:Name\nrename old New\n"))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"exec.go":              "base/core",
		"exec_windows.go":      "base/core",
		"exec_test.go":         "base/core",
		"x_linux_test.go":      "cmd/x",
		"other.go":             ".",
		"base/core/context.go": "base/core",
	}
	for file, want := range cases {
		if got := m.fileDir(file); got != want {
			t.Errorf("fileDir(%q) = %q, want %q", file, got, want)
		}
	}
	if got := m.declDir("a.go", "Name"); got != "base/model" {
		t.Errorf("a declaration override must win over its file's rule, got %q", got)
	}
	if got := m.declDir("infra/x/a.go", "Name"); got != "infra/x" {
		t.Errorf("an override naming a root file must not reach a same-named file elsewhere, got %q", got)
	}
	if m.renames["old"] != "New" {
		t.Errorf("rename not parsed: %v", m.renames)
	}
}

// TestApplyKeepsAMovedTestBesideTheHelpersCopiedWithIt is the reproduction of
// a move that lost tests: a test moved back into its file's own package and a
// helper copied into that package both wrote that one path, so the second
// write erased the test. Both have to survive, in source order.
func TestApplyKeepsAMovedTestBesideTheHelpersCopiedWithIt(t *testing.T) {
	dir := writeFixture(t, map[string]string{
		"root.go":       "package fixture\n\nfunc Root() int { return 1 }\n",
		"thing.go":      "package fixture\n\nfunc Thing() int { return 2 }\n",
		"thing_test.go": "package fixture\n\nimport \"testing\"\n\nfunc fixtureValue() int { return 2 }\n\nfunc TestThing(t *testing.T) {\n\tif Thing() != fixtureValue() {\n\t\tt.Fatal()\n\t}\n}\n\nfunc TestThingBesideRoot(t *testing.T) {\n\tif Root()+Thing() != fixtureValue()+1 {\n\t\tt.Fatal()\n\t}\n}\n",
		"packages.map":  "sub thing.go\n. thing_test.go:TestThingBesideRoot\n",
	})
	if out, err := runPkgmove(t, dir, "apply", "-map", filepath.Join(dir, "packages.map")); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	root := readFile(t, filepath.Join(dir, "thing_test.go"))
	for _, want := range []string{"func fixtureValue() int", "func TestThingBesideRoot"} {
		if !strings.Contains(root, want) {
			t.Errorf("the root package's thing_test.go lost %q:\n%s", want, root)
		}
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "sub", "thing_test.go")), "func TestThing(") {
		t.Error("the moved package lost TestThing")
	}
	goIn(t, dir, "test", "./...")
}

// TestApplySplitsAGroupedDeclarationBetweenPackages pins the split a grouped
// declaration needs: one `type ( ... )` block whose specs go to two packages
// is reopened in the second, and an iota const group stays whole because
// splitting it would renumber the specs after the cut.
func TestApplySplitsAGroupedDeclarationBetweenPackages(t *testing.T) {
	dir := writeFixture(t, map[string]string{
		"types.go":     "package fixture\n\n// Shared types.\ntype (\n\t// Kept stays.\n\tKept int\n\t// Moved goes.\n\tMoved string\n)\n\nconst (\n\tFirst = iota\n\tSecond\n)\n",
		"use.go":       "package fixture\n\nfunc Use() (Kept, Moved, int) { return 1, \"x\", Second }\n",
		"packages.map": "sub types.go:Moved\nsub types.go:Second\n",
	})
	if out, err := runPkgmove(t, dir, "apply", "-map", filepath.Join(dir, "packages.map")); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	root := readFile(t, filepath.Join(dir, "types.go"))
	sub := readFile(t, filepath.Join(dir, "sub", "types.go"))
	if !strings.Contains(root, "Kept int") || strings.Contains(root, "Moved string") {
		t.Errorf("the root keeps Kept and loses Moved:\n%s", root)
	}
	if !strings.Contains(sub, "type (") || !strings.Contains(sub, "// Moved goes.") || !strings.Contains(sub, "Moved string") {
		t.Errorf("the second package reopens the group around Moved, with its comment:\n%s", sub)
	}
	if !strings.Contains(root, "Second") || strings.Contains(sub, "Second") {
		t.Errorf("an iota group stays whole where its first spec is:\nroot:\n%s\nsub:\n%s", root, sub)
	}
	goIn(t, dir, "vet", "./...")
}

// TestApplyAppendsToAFileAPackageAlreadyHas is the shape every step after the
// first takes: declarations pulled out of a root file into a package that
// already holds a file of that name are added to it, keeping what was there.
func TestApplyAppendsToAFileAPackageAlreadyHas(t *testing.T) {
	dir := writeFixture(t, map[string]string{
		"sub/cloud.go": "package sub\n\nimport (\n\t\"strings\"\n\n\t\"example.com/fixture/base\"\n)\n\n// Earlier came first.\nfunc Earlier() string { return strings.ToUpper(base.A) }\n",
		"base/base.go": "package base\n\nconst A = \"a\"\n",
		"cloud.go":     "package fixture\n\nimport \"strconv\"\n\nfunc Later() string { return strconv.Itoa(1) }\n\nfunc Stays() string { return Later() }\n",
		"packages.map": "sub cloud.go:Later\n",
	})
	if out, err := runPkgmove(t, dir, "apply", "-map", filepath.Join(dir, "packages.map")); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	merged := readFile(t, filepath.Join(dir, "sub", "cloud.go"))
	for _, want := range []string{"// Earlier came first.", "func Earlier()", "func Later()", `"strings"`, `"strconv"`, `"example.com/fixture/base"`} {
		if !strings.Contains(merged, want) {
			t.Errorf("sub/cloud.go is missing %q:\n%s", want, merged)
		}
	}
	if strings.Index(merged, "func Earlier") > strings.Index(merged, "func Later") {
		t.Errorf("what the file already held stays first:\n%s", merged)
	}
	goIn(t, dir, "vet", "./...")
}

// TestApplyDropsAnImportOnlyAShadowingLocalStillSpells is the reproduction of
// a moved file that kept an import it no longer used: a parameter named like
// the package it shadows is not a use of that package, so once the one real
// use moves away, the import goes with it.
func TestApplyDropsAnImportOnlyAShadowingLocalStillSpells(t *testing.T) {
	dir := writeFixture(t, map[string]string{
		"files.go":     "package fixture\n\nimport (\n\t\"io\"\n\t\"strings\"\n)\n\ntype FileIO struct{ Write func(string) }\n\nfunc Save(io FileIO) { io.Write(\"x\") }\n\nfunc AsWriter(b *strings.Builder) io.Writer { return b }\n",
		"packages.map": "sub files.go\n. files.go:AsWriter\n",
	})
	if out, err := runPkgmove(t, dir, "apply", "-map", filepath.Join(dir, "packages.map")); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if moved := readFile(t, filepath.Join(dir, "sub", "files.go")); strings.Contains(moved, `"io"`) {
		t.Errorf("the moved file must not import io for a parameter named io:\n%s", moved)
	}
	goIn(t, dir, "vet", "./...")
}

// TestApplyMovesAHelperTypeWithItsMethods is the reproduction of a copied stub
// that lost its methods: a test-only type used both at home and by a moved
// test is copied, and its methods have to be copied with it -- Go keeps
// methods with their type, so a copy without them stops implementing the
// interface its users pass it as.
func TestApplyMovesAHelperTypeWithItsMethods(t *testing.T) {
	dir := writeFixture(t, map[string]string{
		"store.go":      "package fixture\n\ntype Store interface{ Load() int }\n\nfunc Read(s Store) int { return s.Load() }\n",
		"stub_test.go":  "package fixture\n\ntype stub struct{}\n\nfunc (stub) Load() int { return 3 }\n",
		"store_test.go": "package fixture\n\nimport \"testing\"\n\nfunc TestRead(t *testing.T) {\n\tif Read(stub{}) != 3 {\n\t\tt.Fatal()\n\t}\n}\n",
		"root_test.go":  "package fixture\n\nimport \"testing\"\n\nfunc TestRootStub(t *testing.T) {\n\tif (stub{}).Load() != 3 {\n\t\tt.Fatal()\n\t}\n}\n",
		"packages.map":  "sub store.go\nsub store_test.go\nsub stub_test.go\n. stub_test.go:stub\n",
	})
	if out, err := runPkgmove(t, dir, "apply", "-map", filepath.Join(dir, "packages.map")); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	goIn(t, dir, "vet", "./...")
	goIn(t, dir, "test", "./...")
}

// TestApplyGivesEverySplitPackageWithTestsAnIsolatingTestMain pins the guard
// the root package's TestMain gives its tests -- no runtime pod identity, no
// real timing history, no browser, no git maintenance -- for every package the
// split creates: tests moved out of the root package must not lose it.
func TestApplyGivesEverySplitPackageWithTestsAnIsolatingTestMain(t *testing.T) {
	dir := writeFixture(t, map[string]string{
		"base/core/timing.go":      "package core\n\nconst TimingRecordDirEnv = \"FIXTURE_TIMING_DIR\"\n",
		"base/core/timing_test.go": "package core\n\nimport \"testing\"\n\nfunc TestSeam(t *testing.T) {\n\tif TimingRecordDirEnv == \"\" {\n\t\tt.Fatal()\n\t}\n}\n",
		"infra/x/x.go":             "package x\n\nfunc X() int { return 1 }\n",
		"infra/x/x_test.go":        "package x\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestIsolated(t *testing.T) {\n\tif os.Getenv(\"ERUN_TENANT\") != \"\" || os.Getenv(\"FIXTURE_TIMING_DIR\") == \"\" {\n\t\tt.Fatal(\"the suite is not isolated\")\n\t}\n}\n",
		"root.go":                  "package fixture\n",
		"root_test.go":             "package fixture\n\nimport \"testing\"\n\nfunc TestRoot(t *testing.T) {}\n",
		"packages.map":             "# nothing moves\n",
	})
	if out, err := runPkgmove(t, dir, "apply", "-map", filepath.Join(dir, "packages.map")); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	for _, rel := range []string{"base/core/suite_main_test.go", "infra/x/suite_main_test.go"} {
		if !strings.Contains(readFile(t, filepath.Join(dir, filepath.FromSlash(rel))), "func TestMain(") {
			t.Errorf("%s has no TestMain", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "suite_main_test.go")); !os.IsNotExist(err) {
		t.Error("the root package keeps its own TestMain; none is generated for it")
	}
	t.Setenv("ERUN_TENANT", "leaked")
	goIn(t, dir, "test", "./...")
}
