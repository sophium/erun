package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// frontendWorkspaces are the three npm workspaces test-frontend's own fan-out
// gates. Which of them actually runs a vitest binary is read from each
// package.json rather than listed here, so a workspace that switches its test
// runner has to be re-bounded rather than silently dropping out of this gate.
var frontendWorkspaces = []string{"erun-kit", "erun-ui/frontend", "erun-console"}

// vitest sizes its own worker pool from `os.availableParallelism()`, which
// reads the container's CPU quota -- so one `vitest run` claims the whole
// quota, and test-frontend dispatches the workspaces that use it as separate
// concurrent jobs in the same scripts/parallel-gate.sh fan-out. Two of them
// side by side therefore demand twice the quota before any of the other
// frontend jobs (build, lint, typecheck) or any sibling check-gate target
// takes a share, and the oversubscription is spent as cgroup throttling --
// the mechanism the in-image gate is starved by, where a starved process
// fails on a socket timeout that reads as a network fault.
//
// This is the same defect class GO_TEST_GOMAXPROCS bounds for the Go test
// targets and LINT_GOMAXPROCS bounds for golangci-lint, and it is the
// remaining one the issue itself named ("golangci-lint and vitest are each
// internally parallel, so the real demand is a multiple of the job count").
// Like those, this test asserts the wiring -- that each vitest job's command
// really names the quota-derived bound -- and deliberately does not assert
// the numeric width, which is derived from the environment's own quota at run
// time.
func TestFrontendVitestJobsBoundTheirCPUFanOut(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	makefileText := readMakefile(t, root)

	if !strings.Contains(makefileText, "FRONTEND_VITEST_WORKERS ?=") {
		t.Error("the Makefile no longer defines FRONTEND_VITEST_WORKERS as an overridable (\"?=\") variable -- " +
			"without it nothing bounds the vitest jobs' internal worker fan-out")
	}
	if !strings.Contains(makefileText, "parallel-gate.sh cpu-quota") ||
		!strings.Contains(makefileText, "FRONTEND_VITEST_JOB_COUNT") {
		t.Error("FRONTEND_VITEST_WORKERS no longer derives its width from the environment's real CPU quota " +
			"(parallel-gate.sh cpu-quota divided by FRONTEND_VITEST_JOB_COUNT) -- a constant here would " +
			"oversubscribe a smaller environment and underuse a larger one")
	}

	recipe := makeTargetRecipe(t, makefileText, "test-frontend")
	vitestJobs := 0

	for _, workspace := range frontendWorkspaces {
		name, script := workspaceTestScript(t, root, workspace)
		if !strings.Contains(script, "vitest") {
			continue
		}
		vitestJobs++

		line := jobLine(t, recipe, workspace+" test")
		if line == "" {
			t.Errorf("test-frontend no longer dispatches a job named %q -- the workspace list in this gate "+
				"has gone stale", workspace+" test")
			continue
		}
		if !strings.Contains(line, "--maxWorkers=$(FRONTEND_VITEST_WORKERS)") {
			t.Errorf("%s runs `%s` without --maxWorkers=$(FRONTEND_VITEST_WORKERS): vitest sizes its worker "+
				"pool from the container's whole CPU quota, and test-frontend runs the vitest workspaces as "+
				"concurrent jobs, so together they demand a multiple of that quota and spend CPU periods "+
				"throttled", name, script)
		}
	}

	if vitestJobs == 0 {
		t.Error("no frontend workspace declares a vitest test script -- either the runner changed everywhere " +
			"or this gate's workspace list is stale, and in both cases the bound above is no longer being " +
			"checked against anything")
	}
}

// workspaceTestScript returns a workspace's package.json "name" and its "test"
// script, the two fields the bound above is keyed on.
func workspaceTestScript(t testing.TB, root, workspace string) (name, script string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, workspace, "package.json"))
	if err != nil {
		t.Fatalf("read %s/package.json: %v", workspace, err)
	}
	var pkg struct {
		Name    string            `json:"name"`
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatalf("parse %s/package.json: %v", workspace, err)
	}
	return pkg.Name, pkg.Scripts["test"]
}

// jobLine returns the printf-dispatched job line whose second tab-separated
// field is label, e.g. "erun-ui/frontend test".
func jobLine(t testing.TB, recipe, label string) string {
	t.Helper()
	for _, line := range strings.Split(recipe, "\n") {
		fields := strings.Split(strings.TrimSpace(line), `\t`)
		if len(fields) < 3 {
			continue
		}
		if fields[1] == label {
			return line
		}
	}
	return ""
}
