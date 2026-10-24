package build

import (
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

const testStageDockerfile = "FROM golang:1.26.0 AS test\nRUN --network=host go test ./...\n\nFROM scratch AS runtime\nCOPY --from=test /out/app /app\n"

func stubBuildDaemon(t *testing.T, answers bool, running string) *[]string {
	t.Helper()
	return stubBuildDaemonWithOwner(t, answers, running, "", false)
}

// stubBuildDaemonWithOwner also stages an existing forwarder: owner is its pid
// label ("" for none) and ownerAlive whether that build still runs.
func stubBuildDaemonWithOwner(t *testing.T, answers bool, running, owner string, ownerAlive bool) *[]string {
	t.Helper()
	var calls []string
	previousAnswers, previousRun, previousAlive := buildDaemonEndpointAnswers, runBuildDaemonCommand, buildDaemonOwnerAlive
	buildDaemonEndpointAnswers = func() bool { return answers }
	buildDaemonOwnerAlive = func(string) bool { return ownerAlive }
	runBuildDaemonCommand = func(args ...string) (string, error) {
		joined := strings.Join(args, " ")
		calls = append(calls, joined)
		if len(args) > 0 && args[0] == "inspect" {
			if strings.Contains(joined, "Labels") {
				if owner == "" {
					return "", errors.New("no such container")
				}
				return owner + " true", nil
			}
			return running, nil
		}
		return "", nil
	}
	t.Cleanup(func() {
		buildDaemonEndpointAnswers, runBuildDaemonCommand, buildDaemonOwnerAlive = previousAnswers, previousRun, previousAlive
	})
	return &calls
}

func callsStartingWith(calls []string, prefix string) []string {
	var out []string
	for _, call := range calls {
		if strings.HasPrefix(call, prefix) {
			out = append(out, call)
		}
	}
	return out
}

func testStageBuild(t *testing.T) []shared.DockerBuildSpec {
	t.Helper()
	return []shared.DockerBuildSpec{{DockerfilePath: writeTestDockerfile(t, t.TempDir(), testStageDockerfile)}}
}

// TestEnsureBuildDaemonEndpointForwardsWhereNothingAnswers is the reported
// failure: on a host, nothing listens where a test stage reaches its daemon, so
// erun-backend-api's test stage failed with "Cannot connect to the Docker daemon
// at tcp://127.0.0.1:2375" minutes into the build. The build now forwards its
// own daemon there and removes the forwarder when it ends.
func TestEnsureBuildDaemonEndpointForwardsWhereNothingAnswers(t *testing.T) {
	calls := stubBuildDaemon(t, false, "true")
	stop, err := ensureBuildDaemonEndpoint(core.Context{}, testStageBuild(t))
	if err != nil {
		t.Fatal(err)
	}
	runs := callsStartingWith(*calls, "run -d --name erun-build-daemon ")
	if len(runs) != 1 || !strings.Contains(runs[0], "--network host") ||
		!strings.Contains(runs[0], "TCP-LISTEN:2375,bind=127.0.0.1") {
		t.Fatalf("calls = %v, want one loopback-only forwarder on the daemon host's network", *calls)
	}
	stop()
	if last := (*calls)[len(*calls)-1]; last != "rm -f erun-build-daemon" {
		t.Fatalf("last call = %q, want the forwarder removed when the build ends", last)
	}
}

// A leftover forwarder answers the probe too (Docker Desktop and OrbStack publish
// host-network ports on the Mac's loopback); it must be replaced, not used and
// left behind.
func TestEnsureBuildDaemonEndpointReplacesALeftoverThatAnswersTheProbe(t *testing.T) {
	calls := stubBuildDaemonWithOwner(t, true, "true", "4242", false)
	stop, err := ensureBuildDaemonEndpoint(core.Context{}, testStageBuild(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(callsStartingWith(*calls, "run -d")) != 1 {
		t.Fatalf("calls = %v, want the leftover replaced by this build's own forwarder", *calls)
	}
	stop()
	if last := (*calls)[len(*calls)-1]; last != "rm -f erun-build-daemon" {
		t.Fatalf("last call = %q, want this build's forwarder removed when it ends", last)
	}
}

// Inside an environment pod the dind sidecar already answers, so nothing runs.
func TestEnsureBuildDaemonEndpointUsesAnEndpointThatAnswers(t *testing.T) {
	calls := stubBuildDaemon(t, true, "true")
	if _, err := ensureBuildDaemonEndpoint(core.Context{}, testStageBuild(t)); err != nil {
		t.Fatal(err)
	}
	if len(callsStartingWith(*calls, "run ")) != 0 || len(callsStartingWith(*calls, "rm ")) != 0 {
		t.Fatalf("started or removed something although the endpoint answers: %v", *calls)
	}
}

// A forwarder that exits at once fails the build here, naming why.
func TestEnsureBuildDaemonEndpointReportsAForwarderThatExited(t *testing.T) {
	calls := stubBuildDaemon(t, false, "false")
	_, err := ensureBuildDaemonEndpoint(core.Context{}, testStageBuild(t))
	if err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("err = %v, want the exited forwarder reported", err)
	}
	if last := (*calls)[len(*calls)-1]; last != "rm -f erun-build-daemon" {
		t.Fatalf("last call = %q, want the exited forwarder removed", last)
	}
}

// A forwarder another running build started is shared, and left to that build.
func TestEnsureBuildDaemonEndpointSharesARunningBuildsForwarder(t *testing.T) {
	calls := stubBuildDaemonWithOwner(t, false, "true", "4242", true)
	stop, err := ensureBuildDaemonEndpoint(core.Context{}, testStageBuild(t))
	if err != nil {
		t.Fatal(err)
	}
	stop()
	if len(callsStartingWith(*calls, "run ")) != 0 || len(callsStartingWith(*calls, "rm ")) != 0 {
		t.Fatalf("calls = %v, want the live owner's forwarder reused and not removed", *calls)
	}
}

// A forwarder left by a build that was killed before it could clean up holds
// the address; it is replaced instead of making the next build fail to bind.
func TestEnsureBuildDaemonEndpointReplacesAForwarderWhoseBuildIsGone(t *testing.T) {
	calls := stubBuildDaemonWithOwner(t, false, "true", "4242", false)
	if _, err := ensureBuildDaemonEndpoint(core.Context{}, testStageBuild(t)); err != nil {
		t.Fatal(err)
	}
	rms, runs := callsStartingWith(*calls, "rm -f erun-build-daemon"), callsStartingWith(*calls, "run -d")
	if len(rms) == 0 || len(runs) != 1 {
		t.Fatalf("calls = %v, want the orphaned forwarder removed and a fresh one started", *calls)
	}
}
