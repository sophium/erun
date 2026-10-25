package helmrun

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/deploy/observe"
)

func unschedulablePod(name, message string) observe.PodStatusItem {
	pod := observe.PodStatusItem{}
	pod.Metadata.Name = name
	pod.Status.Phase = "Pending"
	pod.Status.Conditions = []observe.PodConditionEntry{
		{Type: "PodScheduled", Status: "False", Reason: "Unschedulable", Message: message},
	}
	return pod
}

func scheduledPod(name string) observe.PodStatusItem {
	pod := observe.PodStatusItem{}
	pod.Metadata.Name = name
	pod.Status.Phase = "Running"
	pod.Status.Conditions = []observe.PodConditionEntry{
		{Type: "PodScheduled", Status: "True"},
	}
	pod.Status.ContainerStatuses = []observe.ContainerStatusEntry{
		{Name: "app", Ready: true, State: observe.ContainerState{Running: &observe.ContainerStateRunning{}}},
	}
	return pod
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.t = c.t.Add(d)
}

func TestClassifyTerminalFailureUnschedulablePodRecoversResetsGrace(t *testing.T) {
	// The grace timer resets once the pod schedules.
	clock := &fakeClock{t: time.Unix(0, 0)}
	params := podWatchParams{ReleaseName: "team-devops", Namespace: "team-dev", Now: clock.now}
	unscheduledSince := map[string]time.Time{}
	name := "team-devops-0"

	if failure := classifyTerminalFailure([]observe.PodStatusItem{unschedulablePod(name, "waiting")}, params, unscheduledSince); failure != nil {
		t.Fatalf("first observation must not fail immediately, got: %v", failure)
	}

	clock.advance(defaultUnscheduledGracePeriod + time.Second)
	if failure := classifyTerminalFailure([]observe.PodStatusItem{scheduledPod(name)}, params, unscheduledSince); failure != nil {
		t.Fatalf("a scheduled pod must never be reported as unschedulable, got: %v", failure)
	}
	if _, tracked := unscheduledSince[name]; tracked {
		t.Fatalf("a resolved pod must clear its tracked unscheduled-since time")
	}

	if failure := classifyTerminalFailure([]observe.PodStatusItem{unschedulablePod(name, "waiting again")}, params, unscheduledSince); failure != nil {
		t.Fatalf("re-observing unschedulable must restart the grace period, got: %v", failure)
	}
}

func TestClassifyTerminalFailurePrefersContainerFailureOverUnschedulable(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	params := podWatchParams{ReleaseName: "team-devops", Namespace: "team-dev", Now: clock.now}
	unscheduledSince := map[string]time.Time{"team-devops-0": clock.now()}

	pod := observe.PodStatusItem{}
	pod.Metadata.Name = "team-devops-0"
	pod.Status.ContainerStatuses = []observe.ContainerStatusEntry{
		{Name: "app", State: observe.ContainerState{Waiting: &observe.ContainerStateWaiting{Reason: "InvalidImageName", Message: "bad ref"}}},
	}

	failure := classifyTerminalFailure([]observe.PodStatusItem{pod}, params, unscheduledSince)
	if failure == nil || failure.Reason != "InvalidImageName" {
		t.Fatalf("expected the container failure to take precedence, got: %v", failure)
	}
	if _, tracked := unscheduledSince["team-devops-0"]; tracked {
		t.Errorf("a pod reporting a container failure must not stay tracked as unscheduled")
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
