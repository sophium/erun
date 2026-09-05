package eruncommon

import (
	"context"
	"net/http"
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
