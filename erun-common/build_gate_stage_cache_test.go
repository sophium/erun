package eruncommon

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// gateStageCacheDockerfile is the gate convention's shape: a `test` stage whose
// marker a later stage copies out of, which is what dockerfileHasGateTestStage
// matches and what makes this build the project's own gate.
const gateStageCacheDockerfile = "FROM --platform=$BUILDPLATFORM alpine:3.22 AS test\n" +
	"RUN echo live > /test-ok\n" +
	"\n" +
	"FROM alpine:3.22 AS builder\n" +
	"COPY --from=test /test-ok /tmp/erun-test-ok\n"

func gateStageCacheArgv(t *testing.T, forceGateTestStage bool) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Dockerfile")
	if err := os.WriteFile(path, []byte(gateStageCacheDockerfile), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	return dockerBuildArgs(
		DockerBuildSpec{
			DockerfilePath:     path,
			Image:              DockerImageReference{Tag: "ghcr.io/acme/devops:1.2.3"},
			GateTestStage:      true,
			ForceGateTestStage: forceGateTestStage,
		},
		"linux/amd64",
	)
}

// A run that declares itself the merge queue's gate has to execute the gate, not
// read a memoized verdict on byte-identical content: BuildKit's layer cache is the
// second door into the test stage -- erun's fingerprint cache is the first, and
// the promotion guard already closes that one -- and it is the door every
// documented gate flow used to leave open, because a replay exits zero exactly
// like a real gate run.
//
// Measured against a real daemon, this argv turns the replayed stream
//
//	#4 [test 1/2] FROM alpine:3.22@sha256:5291...
//	#4 DONE 0.0s
//	#5 [test 2/2] RUN echo live > /test-ok
//	#5 CACHED
//
// into one where the stage's own instruction is DONE and executes, which is the
// only evidence the builder can give that `make check` ran in this run.
func TestGateBuildInvalidatesItsTestStageLayers(t *testing.T) {
	argv := gateStageCacheArgv(t, true)
	at := slices.Index(argv, "--no-cache-filter")
	if at < 0 {
		t.Fatalf("expected a declared gate build to invalidate its test stage's layers, got %v", argv)
	}
	if at+1 >= len(argv) || argv[at+1] != gateStageName {
		t.Fatalf("expected --no-cache-filter to name the stage the Dockerfile declares and erun watches (%q), got %v", gateStageName, argv)
	}
}

// The contrast that keeps this a scoped change rather than a blanket one: an
// ordinary incremental build replaying a cached test stage is a cache working as
// designed, and invalidating the project's whole gate there would charge every
// developer a full `make check` for rebuilding an image nothing changed in. Only
// the run that declares itself a gate asks for the stage to execute.
func TestOrdinaryBuildKeepsItsTestStageLayers(t *testing.T) {
	argv := gateStageCacheArgv(t, false)
	if slices.Contains(argv, "--no-cache-filter") {
		t.Fatalf("expected an ordinary build to leave BuildKit's layer cache alone, got %v", argv)
	}
}
