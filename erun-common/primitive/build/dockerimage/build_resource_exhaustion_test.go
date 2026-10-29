package dockerimage

import (
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/build/diagnose"
)

const gateFailureLogWithSelfCancelledTest = `cat <<'EOF'
2026/09/18 06:14:15 erun: workspace sync /workspace -> /tmp/TestSyncWorkspaceOnceLeavesNoDebrisWhenTheContextIsCancelled3065967408/001: notGitRepo=false remote=1 fetchError=create remote archive: signal: killed: error=list remote outputs: context canceled
#68 96.59 >> running integration suite (cover dir: /tmp/erun-integration-cover.PPczQS, parallel: 8) [92s]
#68 96.74 make[1]: *** [Makefile:617: integration-test-gate] Error 1
#68 96.74 make: *** [Makefile:674: check] Error 2
#68 ERROR: process "/bin/sh -c make check" did not complete successfully: exit code: 2
EOF
exit 2`

func TestRunDockerBuildOnceBlamesTheFailedTestNotResourceExhaustion(t *testing.T) {
	stub := writeExecutableScript(t, gateFailureLogWithSelfCancelledTest)
	t.Setenv("ERUN_DOCKER_BIN", stub)

	var stdout, stderr strings.Builder
	_, err := RunDockerBuildOnce([]string{"build"}, ".", "tag", false, core.VerbosityInfo, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected the stub's non-zero exit to fail the build")
	}

	var exhausted diagnose.DockerBuildResourceExhaustionError
	if errors.As(err, &exhausted) {
		t.Fatalf("err = %q, want the failed test reported; the only \"signal: killed\" in this log came from a test that cancelled its own context", err)
	}
	if strings.Contains(err.Error(), "resource exhaustion") {
		t.Fatalf("err = %q, must not attribute a normal exit to resource exhaustion", err)
	}

	var stepErr diagnose.DockerBuildStepError
	if !errors.As(err, &stepErr) {
		t.Fatalf("err = %T (%q), want the build's own failure reason", err, err)
	}
	if !strings.Contains(stepErr.Reason, "integration-test-gate") {
		t.Errorf("reason = %q, want it to name the target that failed", stepErr.Reason)
	}
	if !strings.Contains(stepErr.Reason, "exit code: 2") {
		t.Errorf("reason = %q, want it to carry the status the build actually exited with", stepErr.Reason)
	}
}

func TestRunDockerBuildOnceReportsAKilledBuildProcess(t *testing.T) {
	stub := writeExecutableScript(t, "kill -9 $$")
	t.Setenv("ERUN_DOCKER_BIN", stub)
	// Off-pod, so the diagnosis does not also resolve a container through kubectl.
	t.Setenv("KUBERNETES_SERVICE_HOST", "")

	var stdout, stderr strings.Builder
	_, err := RunDockerBuildOnce([]string{"build"}, ".", "tag", false, core.VerbosityInfo, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected a SIGKILLed build process to fail the build")
	}

	var exhausted diagnose.DockerBuildResourceExhaustionError
	if !errors.As(err, &exhausted) {
		t.Fatalf("err = %T (%q), want a SIGKILLed build process reported as a kill", err, err)
	}
	if !strings.Contains(err.Error(), "terminated the docker build process itself") {
		t.Errorf("diagnosis = %q, want it to name the evidence it acted on", err)
	}
}
