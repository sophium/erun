package eruncommon

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

var errBoom = errors.New("boom")

func fakePodJSONRunner(stdout string, err error) openKubectlRunnerFunc {
	return func(args []string, out, errOut io.Writer) error {
		if err != nil {
			return err
		}
		_, writeErr := io.Copy(out, bytes.NewBufferString(stdout))
		return writeErr
	}
}

func TestDockerBuildResourceExhaustionDiagnosisDetectsAnOOMKilledCompilerRenderedAsATypecheckIssue(t *testing.T) {
	// An OOM-killed compiler renders as a specific typecheck issue naming a
	// real, innocent import, with "signal: killed" the only tell -- and it is
	// inside a parenthetical.
	output := `>> golangci-lint erun-backend/erun-backend-api
internal/provision/awssdk.go:18:2: could not import github.com/aws/aws-sdk-go-v2/service/ec2
        (-: /usr/local/go/pkg/tool/linux_arm64/compile: signal: killed) (typecheck)
        "github.com/aws/aws-sdk-go-v2/service/ec2"
        ^
1 issues:
* typecheck: 1
lint failed in: erun-backend/erun-backend-api
... 25 lines later ...
ERROR: failed to solve: ResourceExhausted: process "/bin/sh -c make check && touch /test-ok" did not complete successfully: cannot allocate memory`

	getenvOffPod := func(string) string { return "" }
	diagnosis, ok := dockerBuildResourceExhaustionDiagnosisWithRunner(output, getenvOffPod, nil)
	if !ok {
		t.Fatal("expected the OOM markers in this output to be detected")
	}
	if !strings.Contains(diagnosis, "resource exhaustion") {
		t.Errorf("diagnosis = %q, want it to name resource exhaustion as the cause", diagnosis)
	}
	if strings.Contains(diagnosis, "could not import") {
		t.Errorf("diagnosis = %q, must not repeat the misleading typecheck framing as if it were the cause", diagnosis)
	}
}

func TestDockerBuildResourceExhaustionDiagnosisIgnoresAnUnrelatedFailure(t *testing.T) {
	output := "internal/provision/awssdk.go:42:2: undefined: someRealTypo\n1 issues:\n* typecheck: 1"
	if _, ok := dockerBuildResourceExhaustionDiagnosisWithRunner(output, func(string) string { return "" }, nil); ok {
		t.Fatal("a plain compile error with no OOM marker must not be reported as resource exhaustion")
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
	diagnosis, ok := dockerBuildResourceExhaustionDiagnosisWithRunner("cannot allocate memory", getenvInPod, runner)
	if !ok {
		t.Fatal("expected the marker to be detected")
	}
	if !strings.Contains(diagnosis, "erun-dind") || !strings.Contains(diagnosis, "8916Mi") {
		t.Errorf("diagnosis = %q, want it to name the erun-dind container and its 8916Mi limit", diagnosis)
	}
}

func TestDockerBuildResourceExhaustionDiagnosisOmitsContainerWhenNotInPod(t *testing.T) {
	getenvOffPod := func(string) string { return "" }
	diagnosis, ok := dockerBuildResourceExhaustionDiagnosisWithRunner("signal: killed", getenvOffPod, fakePodJSONRunner(`{}`, nil))
	if !ok {
		t.Fatal("expected the marker to be detected")
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
