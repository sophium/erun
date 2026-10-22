package eruncommon

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
)

// unschedulablePod builds a pod that never got past scheduling: no container
// statuses at all, only the PodScheduled=False condition the real kubelet
// would report.
func unschedulablePod(name, message string) podStatusItem {
	pod := podStatusItem{}
	pod.Metadata.Name = name
	pod.Status.Phase = "Pending"
	pod.Status.Conditions = []podConditionEntry{
		{Type: "PodScheduled", Status: "False", Reason: "Unschedulable", Message: message},
	}
	return pod
}

func scheduledPod(name string) podStatusItem {
	pod := podStatusItem{}
	pod.Metadata.Name = name
	pod.Status.Phase = "Running"
	pod.Status.Conditions = []podConditionEntry{
		{Type: "PodScheduled", Status: "True"},
	}
	pod.Status.ContainerStatuses = []containerStatusEntry{
		{Name: "app", Ready: true, State: containerState{Running: &containerStateRunning{}}},
	}
	return pod
}

// fakeClock lets a test move time forward in arbitrary jumps instead of
// sleeping real wall-clock time, so the grace period's boundary is exercised
// deterministically.
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

	if failure := classifyTerminalFailure([]podStatusItem{pod}, params, unscheduledSince); failure != nil {
		t.Fatalf("first observation must not fail immediately, got: %v", failure)
	}

	clock.advance(defaultUnscheduledGracePeriod - time.Second)
	if failure := classifyTerminalFailure([]podStatusItem{pod}, params, unscheduledSince); failure != nil {
		t.Fatalf("must stay within grace right up to the boundary, got: %v", failure)
	}

	clock.advance(2 * time.Second)
	failure := classifyTerminalFailure([]podStatusItem{pod}, params, unscheduledSince)
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
	// A pod that gets scheduled before the grace elapses must not be treated as
	// terminal later just because it was briefly unschedulable earlier — the
	// timer resets rather than accumulating across a resolved gap.
	clock := &fakeClock{t: time.Unix(0, 0)}
	params := podWatchParams{ReleaseName: "team-devops", Namespace: "team-dev", Now: clock.now}
	unscheduledSince := map[string]time.Time{}
	name := "team-devops-0"

	if failure := classifyTerminalFailure([]podStatusItem{unschedulablePod(name, "waiting")}, params, unscheduledSince); failure != nil {
		t.Fatalf("first observation must not fail immediately, got: %v", failure)
	}

	clock.advance(defaultUnscheduledGracePeriod + time.Second)
	if failure := classifyTerminalFailure([]podStatusItem{scheduledPod(name)}, params, unscheduledSince); failure != nil {
		t.Fatalf("a scheduled pod must never be reported as unschedulable, got: %v", failure)
	}
	if _, tracked := unscheduledSince[name]; tracked {
		t.Fatalf("a resolved pod must clear its tracked unscheduled-since time")
	}

	// Unschedulable again later: the grace period must restart from now, not
	// from the original observation over a minute ago.
	if failure := classifyTerminalFailure([]podStatusItem{unschedulablePod(name, "waiting again")}, params, unscheduledSince); failure != nil {
		t.Fatalf("re-observing unschedulable must restart the grace period, got: %v", failure)
	}
}

func TestClassifyTerminalFailurePrefersContainerFailureOverUnschedulable(t *testing.T) {
	// A pod tracked as unschedulable that later reports a real container
	// failure should surface the more specific container reason, not the
	// scheduling one — and must stop being tracked as unschedulable.
	clock := &fakeClock{t: time.Unix(0, 0)}
	params := podWatchParams{ReleaseName: "team-devops", Namespace: "team-dev", Now: clock.now}
	unscheduledSince := map[string]time.Time{"team-devops-0": clock.now()}

	pod := podStatusItem{}
	pod.Metadata.Name = "team-devops-0"
	pod.Status.ContainerStatuses = []containerStatusEntry{
		{Name: "app", State: containerState{Waiting: &containerStateWaiting{Reason: "InvalidImageName", Message: "bad ref"}}},
	}

	failure := classifyTerminalFailure([]podStatusItem{pod}, params, unscheduledSince)
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

	if _, ok := podUnschedulableMessage(podStatusItem{}); ok {
		t.Fatalf("a pod with no conditions must not report as unschedulable")
	}
}

// podListFoundHandler serves a fixed pods List response at the exact path
// client-go's List call hits, the same shape namespaceFoundHandler
// (kubernetes_client_go_test.go) uses for a single-resource Get.
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

// libraryPodWatchFixtureJSON is a PodList with one release-owned, cleanly
// running pod -- the same shape deploy_test.go's cleanRolloutPodJSON pins for
// the subprocess path, kept as its own copy here since the integration
// module and this one are separate Go modules.
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

// TestLibraryListReleasePodsMatchesSubprocessObservableResult pins the same
// equivalence property kubernetes_client_go_test.go's other
// Library*MatchesSubprocessObservableResult tests do: the typed List result,
// re-marshaled and fed through the shared parse/filter/summarize pipeline,
// produces the exact status line the subprocess path's raw kubectl JSON does.
func TestLibraryListReleasePodsMatchesSubprocessObservableResult(t *testing.T) {
	fakeKubernetesAPIServer(t, podListFoundHandler("team-dev", libraryPodWatchFixtureJSON))

	raw, err := libraryListReleasePods(context.Background(), podWatchParams{Namespace: "team-dev"})
	if err != nil {
		t.Fatalf("libraryListReleasePods: %v", err)
	}
	list, ok := parsePodStatusList(raw)
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

// TestLibraryListReleasePodsHonorsContextOverride proves the context-name
// field actually selects the kubeconfig context, the same way `kubectl
// --context X` does, rather than always following current-context.
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
	if got := ExecutionModeFor(ERunConfig{}, kubectlPodWatchExecutionOperation); got != ExecutionModeSubprocess {
		t.Fatalf("mode = %q, want %q", got, ExecutionModeSubprocess)
	}
}

func TestExecutionModeReportListsKubectlPodWatchOperation(t *testing.T) {
	report := ExecutionModeReport(ERunConfig{})
	for _, status := range report {
		if status.Operation == kubectlPodWatchExecutionOperation {
			if status.Mode != ExecutionModeSubprocess {
				t.Fatalf("mode = %q, want %q", status.Mode, ExecutionModeSubprocess)
			}
			return
		}
	}
	t.Fatalf("kubectl-pod-watch not found in report: %+v", report)
}

// pullingPod builds a pod whose container is still fetching its image, the way
// a cold node reports a large one: kubelet alternates between ErrImagePull and
// ImagePullBackOff while it retries.
func pullingPod(name, container, reason, message string) podStatusItem {
	return pullingPodOfImage(name, container, "", reason, message)
}

// pullingPodOfImage is pullingPod with the image kubelet reports the container
// is using, which `kubectl get pods -o json` carries in
// status.containerStatuses[].image for a container still waiting on its pull.
func pullingPodOfImage(name, container, image, reason, message string) podStatusItem {
	pod := podStatusItem{}
	pod.Metadata.Name = name
	pod.Status.Phase = "Pending"
	pod.Status.Conditions = []podConditionEntry{{Type: "PodScheduled", Status: "True"}}
	pod.Status.ContainerStatuses = []containerStatusEntry{
		{Name: container, Image: image, State: containerState{Waiting: &containerStateWaiting{Reason: reason, Message: message}}},
	}
	return pod
}

// TestPullingContainersNamesTheContainersStillFetchingTheirImage covers the one
// observation helm cannot make: its rollout deadline is a fixed duration and
// expires identically whether the image finished downloading or not, so a
// deploy that ran out its wait mid-pull reports "Progress deadline exceeded"
// for a rollout that was working exactly as intended.
//
// The terminal image-pull rejection is the case that must NOT be reported as
// progress: the watcher aborts on it and carries the registry's own message,
// and describing a refused image as a slow one would misstate the failure.
//
// Each entry also names the image being pulled, because the two reasons a wait
// can expire mid-pull need opposite answers and the container name alone does
// not separate them: a legitimately slow cold pull is answered by a longer
// deploy.timeout, an unpublished tag by deploying the right one. A container
// status that carries no image keeps the bare locator rather than an empty
// parenthesis.
func TestPullingContainersNamesTheContainersStillFetchingTheirImage(t *testing.T) {
	rejected := pullingPod("team-devops-ghi", "erun-devops", "ErrImagePull", "manifest unknown: manifest unknown")
	pods := []podStatusItem{
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

// TestClassifyHelmDeployResultReportsAWaitThatExpiredMidPull is the second half
// of the same contract: the observation has to reach the deploy's error, or the
// operator still reads only helm's words for a rollout that was progressing.
func TestClassifyHelmDeployResultReportsAWaitThatExpiredMidPull(t *testing.T) {
	stderr := new(strings.Builder)
	stderr.WriteString("Error: UPGRADE FAILED: resource Deployment/team-dev/team-devops not ready. status: InProgress, message: Available: 0/1\n")

	err := classifyHelmDeployResult(HelmDeployParams{}, podWatchOutcome{Pulling: []string{"team-devops-abc/erun-devops"}},
		errors.New("exit status 1"), &helmOutputCapture{stdout: new(bytes.Buffer), stderr: new(bytes.Buffer)}, stderr)

	if err == nil {
		t.Fatal("expected the failed rollout to report an error")
	}
	for _, want := range []string{
		"team-devops-abc/erun-devops was still pulling its image",
		"this is the deploy's own timeout ending the rollout, not a container failure",
		"the previous pod was already torn down and this environment is running no pod",
		"exit status 1",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected %q in the deploy error, got:\n%s", want, err.Error())
		}
	}
}

// TestClassifyHelmDeployResultLeavesAFailureWithNothingPullingAlone is the
// negative case: a rollout that failed with nothing in flight keeps reporting
// exactly what helm said, with no pull narrative attached to it.
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

// pullingPodListJSON is a PodList with one release-owned pod whose container is
// still fetching its image -- the observation a rollout that ran out its wait
// mid-pull has to carry back to the deploy.
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

// TestWatchReleasePodsKeepsThePullStateAnUnreadFinalPollWouldErase reproduces
// the intermittent half of the mid-pull report: the poll that would have
// reported a download still in flight is the one the watcher's own cancellation
// interrupts, because cancelling the context aborts the `get pods` in flight
// and the loop returns on the next iteration. An unread poll reads nothing, so
// letting it replace the previous poll's state blanks the observation at the
// exact moment it is handed to the deploy -- which then falls back to helm's
// "not ready", the outcome the whole watcher exists to prevent. It surfaced as
// the deploy integration scenario failing only under the gate's load, where the
// window between the last readable poll and the cancellation is wide enough to
// land in.
//
// The overlap is forced rather than waited for: the first poll answers with a
// pod still pulling, the second is held inside the fake API server until this
// test cancels the context underneath it, so the unread final poll happens
// every run instead of whenever the scheduler allows it.
func TestWatchReleasePodsKeepsThePullStateAnUnreadFinalPollWouldErase(t *testing.T) {
	redirectConfigHomeForTest(t)
	if err := SaveERunConfig(ERunConfig{Execution: ExecutionConfig{Modes: map[string]string{
		kubectlPodWatchExecutionOperation: ExecutionModeLibrary,
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
		// Held until the watcher's cancellation aborts this request, so the
		// poll that fails to read is the one in flight when the context ends.
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
