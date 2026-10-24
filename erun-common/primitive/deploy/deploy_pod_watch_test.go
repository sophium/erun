package deploy

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
)

func unschedulablePod(name, message string) PodStatusItem {
	pod := PodStatusItem{}
	pod.Metadata.Name = name
	pod.Status.Phase = "Pending"
	pod.Status.Conditions = []PodConditionEntry{
		{Type: "PodScheduled", Status: "False", Reason: "Unschedulable", Message: message},
	}
	return pod
}

func scheduledPod(name string) PodStatusItem {
	pod := PodStatusItem{}
	pod.Metadata.Name = name
	pod.Status.Phase = "Running"
	pod.Status.Conditions = []PodConditionEntry{
		{Type: "PodScheduled", Status: "True"},
	}
	pod.Status.ContainerStatuses = []ContainerStatusEntry{
		{Name: "app", Ready: true, State: containerState{Running: &containerStateRunning{}}},
	}
	return pod
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.t = c.t.Add(d)
}

func TestClassifyTerminalFailureUnschedulablePod(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	params := podWatchParams{ReleaseName: "team-devops", Namespace: "team-dev", Now: clock.now}
	unscheduledSince := map[string]time.Time{}
	pod := unschedulablePod("team-devops-0", "0/1 nodes are available: 1 Insufficient cpu, 1 Insufficient memory")

	if failure := classifyTerminalFailure([]PodStatusItem{pod}, params, unscheduledSince); failure != nil {
		t.Fatalf("first observation must not fail immediately, got: %v", failure)
	}

	clock.advance(defaultUnscheduledGracePeriod - time.Second)
	if failure := classifyTerminalFailure([]PodStatusItem{pod}, params, unscheduledSince); failure != nil {
		t.Fatalf("must stay within grace right up to the boundary, got: %v", failure)
	}

	clock.advance(2 * time.Second)
	failure := classifyTerminalFailure([]PodStatusItem{pod}, params, unscheduledSince)
	if failure == nil {
		t.Fatalf("expected a terminal failure once the grace period elapses")
	}
	if failure.Reason != "Unschedulable" {
		t.Errorf("reason = %q, want Unschedulable", failure.Reason)
	}
	if failure.Container != "" {
		t.Errorf("container = %q, want empty: a pod that never scheduled has no container", failure.Container)
	}
	if failure.Message != "0/1 nodes are available: 1 Insufficient cpu, 1 Insufficient memory" {
		t.Errorf("message = %q, want the scheduler's message verbatim", failure.Message)
	}
}

func TestClassifyTerminalFailureUnschedulablePodRecoversResetsGrace(t *testing.T) {
	// The grace timer resets once the pod schedules.
	clock := &fakeClock{t: time.Unix(0, 0)}
	params := podWatchParams{ReleaseName: "team-devops", Namespace: "team-dev", Now: clock.now}
	unscheduledSince := map[string]time.Time{}
	name := "team-devops-0"

	if failure := classifyTerminalFailure([]PodStatusItem{unschedulablePod(name, "waiting")}, params, unscheduledSince); failure != nil {
		t.Fatalf("first observation must not fail immediately, got: %v", failure)
	}

	clock.advance(defaultUnscheduledGracePeriod + time.Second)
	if failure := classifyTerminalFailure([]PodStatusItem{scheduledPod(name)}, params, unscheduledSince); failure != nil {
		t.Fatalf("a scheduled pod must never be reported as unschedulable, got: %v", failure)
	}
	if _, tracked := unscheduledSince[name]; tracked {
		t.Fatalf("a resolved pod must clear its tracked unscheduled-since time")
	}

	if failure := classifyTerminalFailure([]PodStatusItem{unschedulablePod(name, "waiting again")}, params, unscheduledSince); failure != nil {
		t.Fatalf("re-observing unschedulable must restart the grace period, got: %v", failure)
	}
}

func TestClassifyTerminalFailurePrefersContainerFailureOverUnschedulable(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	params := podWatchParams{ReleaseName: "team-devops", Namespace: "team-dev", Now: clock.now}
	unscheduledSince := map[string]time.Time{"team-devops-0": clock.now()}

	pod := PodStatusItem{}
	pod.Metadata.Name = "team-devops-0"
	pod.Status.ContainerStatuses = []ContainerStatusEntry{
		{Name: "app", State: containerState{Waiting: &containerStateWaiting{Reason: "InvalidImageName", Message: "bad ref"}}},
	}

	failure := classifyTerminalFailure([]PodStatusItem{pod}, params, unscheduledSince)
	if failure == nil || failure.Reason != "InvalidImageName" {
		t.Fatalf("expected the container failure to take precedence, got: %v", failure)
	}
	if _, tracked := unscheduledSince["team-devops-0"]; tracked {
		t.Errorf("a pod reporting a container failure must not stay tracked as unscheduled")
	}
}

func TestPodUnschedulableMessage(t *testing.T) {
	pod := unschedulablePod("x", "0/1 nodes are available: 1 Insufficient cpu")
	message, ok := podUnschedulableMessage(pod)
	if !ok || message != "0/1 nodes are available: 1 Insufficient cpu" {
		t.Fatalf("got (%q, %v), want the verbatim scheduler message", message, ok)
	}

	if _, ok := podUnschedulableMessage(scheduledPod("y")); ok {
		t.Fatalf("a scheduled pod must not report as unschedulable")
	}

	if _, ok := podUnschedulableMessage(PodStatusItem{}); ok {
		t.Fatalf("a pod with no conditions must not report as unschedulable")
	}
}

func podListFoundHandler(namespace, body string) http.HandlerFunc {
	path := "/api/v1/namespaces/" + namespace + "/pods"
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// Duplicated from the integration module, a separate Go module.
const libraryPodWatchFixtureJSON = `{
  "apiVersion": "v1",
  "kind": "PodList",
  "items": [
    {
      "metadata": {
        "name": "team-devops-aaaaaa",
        "annotations": {"meta.helm.sh/release-name": "team-devops"}
      },
      "status": {
        "phase": "Running",
        "containerStatuses": [
          {"name": "erun-devops", "ready": true, "restartCount": 0, "state": {"running": {"startedAt": "2026-05-09T12:00:00Z"}}}
        ]
      }
    }
  ]
}`

func TestLibraryListReleasePodsMatchesSubprocessObservableResult(t *testing.T) {
	fakeKubernetesAPIServer(t, podListFoundHandler("team-dev", libraryPodWatchFixtureJSON))

	raw, err := libraryListReleasePods(context.Background(), podWatchParams{Namespace: "team-dev"})
	if err != nil {
		t.Fatalf("libraryListReleasePods: %v", err)
	}
	list, ok := ParsePodStatusList(raw)
	if !ok {
		t.Fatalf("parsePodStatusList: could not parse %s", raw)
	}
	summaries := summarizePods(filterReleasePods(list, "team-devops"))
	if len(summaries) != 1 || summaries[0].Line != "pod team-devops-aaaaaa: erun-devops Running (Ready)" {
		t.Fatalf("summaries = %+v, want a single clean-rollout line", summaries)
	}
}

func TestLibraryListReleasePodsPropagatesErrors(t *testing.T) {
	fakeKubernetesAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
			`"message":"pods is forbidden","reason":"Forbidden","code":403}`))
	})

	if _, err := libraryListReleasePods(context.Background(), podWatchParams{Namespace: "team-dev"}); err == nil {
		t.Fatalf("err = nil, want a forbidden error")
	}
}

func TestLibraryListReleasePodsHonorsContextOverride(t *testing.T) {
	fakeKubernetesAPIServer(t, podListFoundHandler("team-dev", libraryPodWatchFixtureJSON))

	if _, err := libraryListReleasePods(context.Background(), podWatchParams{Namespace: "team-dev", KubernetesContext: "test-context"}); err != nil {
		t.Fatalf("libraryListReleasePods: %v", err)
	}
	if _, err := libraryListReleasePods(context.Background(), podWatchParams{Namespace: "team-dev", KubernetesContext: "unknown-context"}); err == nil {
		t.Fatalf("err = nil, want an error for an unknown context")
	}
}

func TestExecutionModeForKubectlPodWatchDefaultsToSubprocess(t *testing.T) {
	if got := config.ExecutionModeFor(config.ERunConfig{}, kubectlPodWatchExecutionOperation); got != config.ExecutionModeSubprocess {
		t.Fatalf("mode = %q, want %q", got, config.ExecutionModeSubprocess)
	}
}

func TestExecutionModeReportListsKubectlPodWatchOperation(t *testing.T) {
	report := config.ExecutionModeReport(config.ERunConfig{})
	for _, status := range report {
		if status.Operation == kubectlPodWatchExecutionOperation {
			if status.Mode != config.ExecutionModeSubprocess {
				t.Fatalf("mode = %q, want %q", status.Mode, config.ExecutionModeSubprocess)
			}
			return
		}
	}
	t.Fatalf("kubectl-pod-watch not found in report: %+v", report)
}

func pullingPod(name, container, reason, message string) PodStatusItem {
	return pullingPodOfImage(name, container, "", reason, message)
}

func pullingPodOfImage(name, container, image, reason, message string) PodStatusItem {
	pod := PodStatusItem{}
	pod.Metadata.Name = name
	pod.Status.Phase = "Pending"
	pod.Status.Conditions = []PodConditionEntry{{Type: "PodScheduled", Status: "True"}}
	pod.Status.ContainerStatuses = []ContainerStatusEntry{
		{Name: container, Image: image, State: containerState{Waiting: &containerStateWaiting{Reason: reason, Message: message}}},
	}
	return pod
}

// A permanently rejected pull is terminal, not progress, so it is not listed.
func TestPullingContainersNamesTheContainersStillFetchingTheirImage(t *testing.T) {
	rejected := pullingPod("team-devops-ghi", "erun-devops", "ErrImagePull", "manifest unknown: manifest unknown")
	pods := []PodStatusItem{
		pullingPodOfImage("team-devops-abc", "erun-devops", "ghcr.io/sophium/erun-devops:1.0.296", "ImagePullBackOff", `Back-off pulling image "ghcr.io/sophium/erun-devops:1.0.296"`),
		pullingPodOfImage("team-devops-def", "erun-dind", "ghcr.io/sophium/erun-dind:1.0.296", "ErrImagePull", "rpc error: code = DeadlineExceeded"),
		rejected,
		pullingPod("team-devops-mno", "erun-devops", "ImagePullBackOff", "Back-off pulling image"),
		scheduledPod("team-devops-jkl"),
	}

	got := strings.Join(pullingContainers(pods), ",")
	want := "team-devops-abc/erun-devops (ghcr.io/sophium/erun-devops:1.0.296)," +
		"team-devops-def/erun-dind (ghcr.io/sophium/erun-dind:1.0.296)," +
		"team-devops-mno/erun-devops"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestClassifyHelmDeployResultLeavesAFailureWithNothingPullingAlone(t *testing.T) {
	stderr := new(strings.Builder)
	stderr.WriteString("Error: UPGRADE FAILED: values don't meet the specifications\n")

	err := classifyHelmDeployResult(HelmDeployParams{}, podWatchOutcome{},
		errors.New("exit status 1"), &helmOutputCapture{stdout: new(bytes.Buffer), stderr: new(bytes.Buffer)}, stderr)

	if err == nil {
		t.Fatal("expected the failed rollout to report an error")
	}
	if strings.Contains(err.Error(), "still pulling") {
		t.Fatalf("a failure with nothing pulling must not be described as a slow image, got:\n%s", err.Error())
	}
}

const pullingPodListJSON = `{
  "apiVersion": "v1",
  "kind": "PodList",
  "items": [
    {
      "metadata": {
        "name": "team-devops-abc",
        "annotations": {"meta.helm.sh/release-name": "team-devops"}
      },
      "status": {
        "phase": "Pending",
        "conditions": [{"type": "PodScheduled", "status": "True"}],
        "containerStatuses": [
          {"name": "erun-devops", "ready": false, "restartCount": 0,
           "state": {"waiting": {"reason": "ImagePullBackOff", "message": "Back-off pulling image \"ghcr.io/sophium/erun-devops:1.0.296\""}}}
        ]
      }
    }
  ]
}`

// The second poll is held until the context is cancelled, forcing the unread
// final poll every run instead of only under load.
func TestWatchReleasePodsKeepsThePullStateAnUnreadFinalPollWouldErase(t *testing.T) {
	redirectConfigHomeForTest(t)
	if err := config.SaveERunConfig(config.ERunConfig{Execution: config.ExecutionConfig{Modes: map[string]string{
		kubectlPodWatchExecutionOperation: config.ExecutionModeLibrary,
	}}}); err != nil {
		t.Fatalf("SaveERunConfig: %v", err)
	}
	t.Setenv("ERUN_DEPLOY_POD_WATCH_INTERVAL", "100ms")

	var polls int32
	secondPoll := make(chan struct{})
	var secondOnce sync.Once
	fakeKubernetesAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/team-dev/pods" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if atomic.AddInt32(&polls, 1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(pullingPodListJSON))
			return
		}
		secondOnce.Do(func() { close(secondPoll) })
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
			t.Errorf("the watcher never cancelled the poll in flight")
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan podWatchOutcome, 1)
	go func() {
		done <- watchReleasePods(ctx, podWatchParams{ReleaseName: "team-devops", Namespace: "team-dev"})
	}()

	<-secondPoll
	cancel()

	select {
	case outcome := <-done:
		if got := strings.Join(outcome.Pulling, ","); got != "team-devops-abc/erun-devops" {
			t.Fatalf("the pull state the last readable poll saw must survive the unread poll that cancelled it, got Pulling=%q", got)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("watchReleasePods did not return after its context was cancelled")
	}
}
