package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// playwright.config.ts sizes its own worker pool from os.availableParallelism()
// when nothing overrides it, and that reads the container's CPU quota -- which
// in the erun-devops image's test stage is useless: a RUN step there is a
// sibling of the erun-dind sidecar's own limited cgroup, not a descendant, so
// cpu.max reads "max" and the derivation falls through to the node's real core
// count rather than the budget this environment was provisioned. The suite
// then runs more workers (each a Go backend *and* a headless Chromium) than
// the quota can host, concurrently with everything else in `make check`'s
// fan-out, and the throttling that spends is what turns into a spec failing on
// its wait budget (#2081).
//
// What this gate locks is where that number is decided. It used to be computed
// in the Dockerfile as DIND_CPU_LIMIT/2, inline in the RUN line -- which made a
// test-parallelism decision an incidental function of a resource *limit*:
// raising the sidecar's CPU cap silently raised the worker count, and a cap of
// 4 could only ever yield 2 workers however much the gate could afford. It is
// now resolved in the Makefile beside every other quota-derived width
// (LINT_GOMAXPROCS, GO_TEST_GOMAXPROCS, FRONTEND_VITEST_WORKERS), dividing the
// environment's CPU quota by an explicit two-cores-per-worker, so the cap
// reaches it as the quota every one of those widths divides rather than as an
// arithmetic argument of its own.
//
// Like the fan-out gates beside this one, it asserts the wiring and not the
// numeric width: the width is derived from the run's own quota at run time,
// and pinning it here would assert the environment instead of the contract.
func TestPlaywrightWorkersBoundTheirCPUFanOut(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	makefileText := readMakefile(t, root)

	if !strings.Contains(makefileText, "ERUN_PLAYWRIGHT_WORKERS ?=") {
		t.Error("the Makefile no longer defines ERUN_PLAYWRIGHT_WORKERS as an overridable (\"?=\") variable -- " +
			"without it nothing bounds the desktop suite's worker fan-out")
	}
	if !strings.Contains(makefileText, "parallel-gate.sh cpu-quota") ||
		!strings.Contains(makefileText, "PLAYWRIGHT_CPU_PER_WORKER") {
		t.Error("ERUN_PLAYWRIGHT_WORKERS no longer derives its width from the environment's real CPU quota " +
			"(parallel-gate.sh cpu-quota divided by PLAYWRIGHT_CPU_PER_WORKER) -- a constant would " +
			"oversubscribe a smaller environment and underuse a larger one")
	}
	// A resolved value Make never exports reaches nothing: the count is read
	// from the environment by playwright.config.ts, several processes deep.
	if !strings.Contains(makefileText, "export ERUN_PLAYWRIGHT_WORKERS") {
		t.Error("the Makefile resolves ERUN_PLAYWRIGHT_WORKERS but never exports it, so the suite's own " +
			"config never sees it and falls back to the node's core count -- the exact oversubscription this " +
			"bound exists to prevent")
	}

	// The other half of the decoupling: the Dockerfile must not decide this
	// number, in any form. Re-deriving it there -- or passing a value it
	// computed -- would silently win over the Makefile's, since the RUN's
	// environment outranks a Makefile `?=`.
	dockerfileRaw, err := os.ReadFile(filepath.Join(root, "erun-devops", "docker", "erun-devops", "Dockerfile"))
	if err != nil {
		t.Fatalf("read the erun-devops Dockerfile: %v", err)
	}
	for _, line := range strings.Split(string(dockerfileRaw), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "ERUN_PLAYWRIGHT_WORKERS=") {
			continue
		}
		t.Errorf("the erun-devops Dockerfile sets %q: the desktop suite's worker count is decided in the "+
			"Makefile's quota-derived widths, and a value set in this RUN step wins over that one -- "+
			"raising the sidecar's CPU cap must not silently raise test parallelism", trimmed)
	}
}
