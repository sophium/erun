package build

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared/opentarget"
)

var errBoom = errors.New("boom")

var errExitedNormally = errors.New("exit status 2")

func fakePodJSONRunner(stdout string, err error) opentarget.OpenKubectlRunnerFunc {
	return func(args []string, out, errOut io.Writer) error {
		if err != nil {
			return err
		}
		_, writeErr := io.Copy(out, bytes.NewBufferString(stdout))
		return writeErr
	}
}

func TestDockerBuildResourceExhaustionDiagnosisDetectsAnOOMKilledCompilerRenderedAsATypecheckIssue(t *testing.T) {
	// The OOM-killed compiler's own output looks innocent; BuildKit's solve error is the evidence.
	output := `>> golangci-lint erun-backend/erun-backend-api
internal/provision/awssdk.go:18:2: could not import github.com/aws/aws-sdk-go-v2/service/ec2
        (-: /usr/local/go/pkg/tool/linux_arm64/compile: signal: killed) (typecheck)
        "github.com/aws/aws-sdk-go-v2/service/ec2"
        ^
1 issues:
* typecheck: 1
lint failed in: erun-backend/erun-backend-api
... 25 lines later ...
ERROR: failed to solve: ResourceExhausted: process "/bin/sh -c make check" did not complete successfully: cannot allocate memory`

	getenvOffPod := func(string) string { return "" }
	diagnosis, ok := dockerBuildResourceExhaustionDiagnosisWithRunner(output, errExitedNormally, getenvOffPod, nil)
	if !ok {
		t.Fatal("expected BuildKit's own memory error for the failing step to be detected")
	}
	if !strings.Contains(diagnosis, "resource exhaustion") {
		t.Errorf("diagnosis = %q, want it to name resource exhaustion as the cause", diagnosis)
	}
	if !strings.Contains(diagnosis, "cannot allocate memory") {
		t.Errorf("diagnosis = %q, want it to name the BuildKit evidence it acted on", diagnosis)
	}
	if strings.Contains(diagnosis, "could not import") {
		t.Errorf("diagnosis = %q, must not repeat the misleading typecheck framing as if it were the cause", diagnosis)
	}
}

func TestDockerBuildResourceExhaustionDiagnosisIgnoresAnUnrelatedFailure(t *testing.T) {
	output := "internal/provision/awssdk.go:42:2: undefined: someRealTypo\n1 issues:\n* typecheck: 1"
	if _, ok := dockerBuildResourceExhaustionDiagnosisWithRunner(output, errExitedNormally, func(string) string { return "" }, nil); ok {
		t.Fatal("a plain compile error with no OOM marker must not be reported as resource exhaustion")
	}
}

func TestDockerBuildResourceExhaustionDiagnosisDeclinesWhenSignalKilledIsTheCodesOwnOutput(t *testing.T) {
	output := `2026/09/18 06:14:15 erun: workspace sync /workspace -> /tmp/TestSyncWorkspaceOnceLeavesNoDebrisWhenTheContextIsCancelled3065967408/001:
  notGitRepo=false remote=1 ... fetchError=create remote archive: signal: killed: error=list remote outputs: context canceled
#68 96.59 >> running integration suite (cover dir: /tmp/erun-integration-cover.PPczQS, parallel: 8) [92s]
#68 96.74 make[1]: *** [Makefile:617: integration-test-gate] Error 1
#68 96.74 make: *** [Makefile:674: check] Error 2
#68 ERROR: process "/bin/sh -c make check" did not complete successfully: exit code: 2`

	diagnosis, ok := dockerBuildResourceExhaustionDiagnosisWithRunner(output, errExitedNormally, func(string) string { return "" }, nil)
	if ok {
		t.Fatalf("diagnosis = %q, want the classifier to decline: the build exited normally with code 2, so nothing here establishes a resource kill", diagnosis)
	}
}

func TestDockerBuildResourceExhaustionDiagnosisDeclinesWhenAMemoryMarkerIsTheCodesOwnOutput(t *testing.T) {
	output := `#68 12.34 client_test.go:40: got code = ResourceExhausted, want OK; cannot allocate memory in test fixture
#68 ERROR: process "/bin/sh -c make check" did not complete successfully: exit code: 2`

	if diagnosis, ok := dockerBuildResourceExhaustionDiagnosisWithRunner(output, errExitedNormally, func(string) string { return "" }, nil); ok {
		t.Fatalf("diagnosis = %q, want the classifier to decline: the step's own output is not BuildKit's word for a memory fault", diagnosis)
	}
}

func TestDockerBuildResourceExhaustionDiagnosisTrustsBuildKitReportingASignalledStep(t *testing.T) {
	output := `#12 30.10 /usr/local/go/pkg/tool/linux_amd64/compile: signal: killed
#12 ERROR: process "/bin/sh -c make check" did not complete successfully: exit code: 137`

	diagnosis, ok := dockerBuildResourceExhaustionDiagnosisWithRunner(output, errExitedNormally, func(string) string { return "" }, nil)
	if !ok {
		t.Fatal("expected BuildKit's exit code 137 for the failing step to be read as a signal kill")
	}
	if !strings.Contains(diagnosis, "137") {
		t.Errorf("diagnosis = %q, want it to name the evidence it acted on", diagnosis)
	}
}

func TestDockerBuildResourceExhaustionDiagnosisNamesTheContainerAndLimitWhenInPod(t *testing.T) {
	getenvInPod := func(key string) string {
		if key == "KUBERNETES_SERVICE_HOST" {
			return "10.0.0.1"
		}
		return ""
	}
	runner := fakePodJSONRunner(`{"spec":{"containers":[{"name":"erun-devops"},{"name":"erun-dind","resources":{"limits":{"memory":"8916Mi"}}}]}}`, nil)
	output := `ERROR: failed to solve: process "/bin/sh -c make check" did not complete successfully: cannot allocate memory`
	diagnosis, ok := dockerBuildResourceExhaustionDiagnosisWithRunner(output, errExitedNormally, getenvInPod, runner)
	if !ok {
		t.Fatal("expected BuildKit's memory framing to be detected")
	}
	if !strings.Contains(diagnosis, "erun-dind") || !strings.Contains(diagnosis, "8916Mi") {
		t.Errorf("diagnosis = %q, want it to name the erun-dind container and its 8916Mi limit", diagnosis)
	}
}

func TestDockerBuildResourceExhaustionDiagnosisOmitsContainerWhenNotInPod(t *testing.T) {
	getenvOffPod := func(string) string { return "" }
	output := `ERROR: failed to solve: process "/bin/sh -c make check" did not complete successfully: cannot allocate memory`
	diagnosis, ok := dockerBuildResourceExhaustionDiagnosisWithRunner(output, errExitedNormally, getenvOffPod, fakePodJSONRunner(`{}`, nil))
	if !ok {
		t.Fatal("expected BuildKit's memory framing to be detected")
	}
	if strings.Contains(diagnosis, "erun-dind") {
		t.Errorf("diagnosis = %q, must not name a container it never checked off-pod", diagnosis)
	}
}

func TestDockerBuildContainerMemoryLimitIsHonestWhenTheAnswerCannotBeTrusted(t *testing.T) {
	t.Run("kubectl unavailable", func(t *testing.T) {
		if _, found := dockerBuildContainerMemoryLimit("pod-a", "erun-dind", nil); found {
			t.Fatal("expected found=false with no runner")
		}
	})

	t.Run("kubectl errors", func(t *testing.T) {
		if _, found := dockerBuildContainerMemoryLimit("pod-a", "erun-dind", fakePodJSONRunner("", errBoom)); found {
			t.Fatal("expected found=false when kubectl fails")
		}
	})

	t.Run("container missing from the response", func(t *testing.T) {
		runner := fakePodJSONRunner(`{"spec":{"containers":[{"name":"erun-devops"}]}}`, nil)
		if _, found := dockerBuildContainerMemoryLimit("pod-a", "erun-dind", runner); found {
			t.Fatal("expected found=false when the named container is absent")
		}
	})

	t.Run("container present with a limit", func(t *testing.T) {
		runner := fakePodJSONRunner(`{"spec":{"containers":[{"name":"erun-dind","resources":{"limits":{"memory":"8916Mi"}}}]}}`, nil)
		limit, found := dockerBuildContainerMemoryLimit("pod-a", "erun-dind", runner)
		if !found || limit != "8916Mi" {
			t.Fatalf("limit = %q, found = %v, want 8916Mi, true", limit, found)
		}
	})
}

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
	_, err := runDockerBuildOnce([]string{"build"}, ".", "tag", false, core.VerbosityInfo, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected the stub's non-zero exit to fail the build")
	}

	var exhausted DockerBuildResourceExhaustionError
	if errors.As(err, &exhausted) {
		t.Fatalf("err = %q, want the failed test reported; the only \"signal: killed\" in this log came from a test that cancelled its own context", err)
	}
	if strings.Contains(err.Error(), "resource exhaustion") {
		t.Fatalf("err = %q, must not attribute a normal exit to resource exhaustion", err)
	}

	var stepErr DockerBuildStepError
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
	_, err := runDockerBuildOnce([]string{"build"}, ".", "tag", false, core.VerbosityInfo, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected a SIGKILLed build process to fail the build")
	}

	var exhausted DockerBuildResourceExhaustionError
	if !errors.As(err, &exhausted) {
		t.Fatalf("err = %T (%q), want a SIGKILLed build process reported as a kill", err, err)
	}
	if !strings.Contains(err.Error(), "terminated the docker build process itself") {
		t.Errorf("diagnosis = %q, want it to name the evidence it acted on", err)
	}
}
