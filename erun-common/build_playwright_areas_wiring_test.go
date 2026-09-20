package eruncommon

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The three tests below pin the wiring the erun#2580 report found unverified:
// the resolver computes a selection, the gate runs a selection, and nothing
// checked that the value which crosses between them is the resolver's own.
// They read the real code path -- the real Dockerfile, the real resolver, the
// real docker argv -- rather than recomputing an expected value, because a
// recomputed expectation agrees with a broken wiring exactly as well as with a
// working one.

// devopsDockerfileForWiringTest is the real Dockerfile `erun build` resolves a
// selection for. Reading it rather than a fixture is the point: a fixture
// cannot notice the real ARG declaration or its threading into `make check`
// being changed or dropped.
func devopsDockerfileForWiringTest(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repoRootForDockerignoreTest(t), "erun-devops", "docker", "erun-devops", "Dockerfile")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return path
}

// dockerfileRunBlockContaining returns the RUN instruction whose body contains
// marker, so an assertion about how that step is invoked cannot be satisfied by
// a stray occurrence of the same text elsewhere in the Dockerfile.
func dockerfileRunBlockContaining(t *testing.T, data, marker string) string {
	t.Helper()
	lines := strings.Split(data, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "RUN ") {
			continue
		}
		block := []string{line}
		for strings.HasSuffix(strings.TrimSpace(block[len(block)-1]), "\\") && i+len(block) < len(lines) {
			block = append(block, lines[i+len(block)])
		}
		if strings.Contains(strings.Join(block, "\n"), marker) {
			return strings.Join(block, "\n")
		}
	}
	return ""
}

// TestErunDevopsDockerfileThreadsTheResolvedPlaywrightSelectionIntoTheGate
// locks both halves of the crossing: the Dockerfile must declare the ARG the
// build passes, and it must hand that value to the `make check` step that runs
// the gate. Either half dropped means the resolver's answer never reaches
// run.sh, and the gate silently runs the full suite -- the failure the report
// described, which is invisible precisely when it widens.
func TestErunDevopsDockerfileThreadsTheResolvedPlaywrightSelectionIntoTheGate(t *testing.T) {
	path := devopsDockerfileForWiringTest(t)
	if !dockerfileConsumesPlaywrightTestAreas(path) {
		t.Fatalf("%s no longer declares ARG PLAYWRIGHT_TEST_AREAS, so applyPlaywrightAreaBuildArgs leaves every selection empty and the gate always runs the full suite", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	run := dockerfileRunBlockContaining(t, string(data), "make check")
	if run == "" {
		t.Fatalf("%s has no RUN step invoking `make check`; the gate's own entrypoint moved and this test checked nothing", path)
	}
	if !strings.Contains(run, `PLAYWRIGHT_TEST_AREAS="${PLAYWRIGHT_TEST_AREAS}"`) {
		t.Fatalf("the `make check` RUN step no longer exports the PLAYWRIGHT_TEST_AREAS build arg into the gate's environment, so run.sh reads no selection and runs the full suite:\n%s", run)
	}
}

// TestDockerBuildArgsCarryTheResolversOwnSelection is the reproduction of the
// reported divergence: it drives the real resolver over a real tree, threads
// its answer through the real applyPlaywrightAreaBuildArgs, and reads the value
// back out of the real docker argv. The assertion is equality with what the
// resolver returned from that same code path, never with a literal, so a
// computation that drifts from the threading (or a threading that drops the
// value) fails here instead of only being visible with `-vv`.
func TestDockerBuildArgsCarryTheResolversOwnSelection(t *testing.T) {
	dir := newPlaywrightAreaTestRepo(t)
	writeFileForTest(t, dir, "erun-ui/playwright/tests/areas/manage/manage.spec.ts", "// manage changed\n")
	runGitForTest(t, dir, "add", ".")
	runGitForTest(t, dir, "commit", "-q", "-m", "touch one area spec")

	want, ok := ResolvePlaywrightTestAreaSelection(testContext(), dir)
	if !ok {
		t.Fatal("expected the fixture tree to resolve a selection")
	}
	if want == "smoke" || want == "all" {
		t.Fatalf("fixture resolves to %q, which cannot tell a narrow selection from the full suite; the test would pass on a wiring that drops the value", want)
	}

	build := DockerBuildSpec{
		DockerfilePath: devopsDockerfileForWiringTest(t),
		Image:          DockerImageReference{Tag: "erun-devops:test"},
	}
	applyPlaywrightAreaBuildArgs(testContext(), dir, &build)

	if build.PlaywrightTestAreas != want {
		t.Fatalf("applyPlaywrightAreaBuildArgs threaded %q, but the resolver it called returned %q", build.PlaywrightTestAreas, want)
	}

	argv := dockerBuildArgs(build, "linux/amd64")
	got := buildArgValueInArgv(argv, "PLAYWRIGHT_TEST_AREAS")
	if got != want {
		t.Fatalf("the docker build argv carries PLAYWRIGHT_TEST_AREAS=%q while the resolver returned %q (argv: %s)", got, want, strings.Join(argv, " "))
	}
}

// TestPlaywrightGateSelectionIsReportedAtDefaultVerbosity covers the half of
// the report that is not about agreement but about legibility: the gate must
// say which selection it ran, at default verbosity, on both branches. The
// full-suite branch matters most -- unset, "" and a genuine "all" all mean the
// full suite, so silence there is what made an unresolved selection
// indistinguishable from a resolved one.
func TestPlaywrightGateSelectionIsReportedAtDefaultVerbosity(t *testing.T) {
	reported := func(t *testing.T, build DockerBuildSpec, dryRun, promote bool) string {
		t.Helper()
		var buf bytes.Buffer
		ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, &buf, &buf), DryRun: dryRun}
		build.Promote = promote
		tracePlaywrightGateSelection(ctx, build)
		return buf.String()
	}
	base := DockerBuildSpec{DockerfilePath: devopsDockerfileForWiringTest(t)}

	narrowed := base
	narrowed.PlaywrightTestAreas = "smoke,manage"
	out := reported(t, narrowed, false, false)
	if !strings.Contains(out, "playwright gate selection: smoke,manage") {
		t.Fatalf("the resolved selection is not reported at default verbosity, so a narrowed gate run is invisible:\n%s", out)
	}

	unresolved := reported(t, base, false, false)
	if !strings.Contains(unresolved, "playwright gate selection: all") {
		t.Fatalf("an unresolved selection does not report the full suite it falls back to:\n%s", unresolved)
	}
	if !strings.Contains(unresolved, "merge base") {
		t.Fatalf("the full-suite report does not name why it is the full suite, so an unresolved selection still reads as a deliberate one:\n%s", unresolved)
	}

	if dryRun := reported(t, narrowed, true, false); dryRun != "" {
		t.Fatalf("a dry run must not emit the line (the dry-run goldens are a frozen contract), got:\n%s", dryRun)
	}
	if promoted := reported(t, narrowed, false, true); promoted != "" {
		t.Fatalf("a promoted build never runs the gate stage, so it must not claim a selection, got:\n%s", promoted)
	}
}

// buildArgValueInArgv reads a --build-arg value out of a docker argv, the same
// argv runDockerBuildOnce executes -- not a re-derivation of what the threading
// should have produced.
func buildArgValueInArgv(argv []string, name string) string {
	prefix := name + "="
	for i, arg := range argv {
		if arg == "--build-arg" && i+1 < len(argv) && strings.HasPrefix(argv[i+1], prefix) {
			return strings.TrimPrefix(argv[i+1], prefix)
		}
	}
	return ""
}

// TestClassifyPlaywrightChangedFilesNeverReturnsAnEmptySelection pins the
// invariant tracePlaywrightGateSelection reads the threaded value through: an
// empty PlaywrightTestAreas means "could not be resolved", so a successful
// classification that returned "" for the full suite would make the build
// report an unresolved selection over a real answer -- a wrong statement about
// which selection the gate runs, in the one line that exists to prevent one.
func TestClassifyPlaywrightChangedFilesNeverReturnsAnEmptySelection(t *testing.T) {
	cases := []struct {
		name    string
		changed []string
	}{
		{"no change at all", nil},
		{"unrelated backend change", []string{"erun-common/foo.go"}},
		{"one area spec", []string{"erun-ui/playwright/tests/areas/manage/manage.spec.ts"}},
		{"smoke spec", []string{"erun-ui/playwright/tests/smoke/smoke.spec.ts"}},
		{"desktop source", []string{"erun-ui/frontend/src/App.tsx"}},
		{"harness file", []string{"erun-ui/playwright/run.sh"}},
		{"a path under the areas root with no area segment", []string{"erun-ui/playwright/tests/areas/loose.spec.ts"}},
	}
	for _, tc := range cases {
		if got := classifyPlaywrightChangedFiles(tc.changed); got == "" {
			t.Fatalf("%s: classification returned an empty selection, which the build reports as 'could not be resolved'", tc.name)
		}
	}
}
