package observe

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/orchestration/clusterread"
)

// computeObserveDrift is pure (no subprocess, no filesystem), the same shape
// as fetchObservedHelmRelease's parse layer in observe_helm_release_test.go —
// so it gets a focused unit test here rather than relying only on the
// integration goldens, which can only lock the couple of shapes the observe
// command's own fixtures happen to produce. The branch matrix this function
// needs (found / genuinely absent / unreadable, each crossed with image and
// pod drift) is cheaper and more exhaustive to hit directly than by threading
// distinct helm/kubectl stub output through a real `erun observe` subprocess
// for each case.

func TestComputeObserveDriftNilReleaseReturnsNil(t *testing.T) {
	if got := computeObserveDrift(config.ShellLaunchParams{RuntimeVersion: "1.0.0"}, nil, nil); got != nil {
		t.Fatalf("drift = %v, want nil", got)
	}
}

func TestComputeObserveDriftUnreadableReleaseWithNoRecordedVersionReportsNothing(t *testing.T) {
	req := config.ShellLaunchParams{Namespace: "team-dev"}
	release := &clusterread.ObservedHelmRelease{Name: "team-devops", Error: "observe: helm is not installed or not on PATH"}

	got := computeObserveDrift(req, release, nil)
	if len(got) != 0 {
		t.Fatalf("drift = %v, want no findings when the env config never recorded a runtimeversion", got)
	}
	// The run read, so it still reports the verdict the read produced: an
	// empty list, never nil. nil is reserved for a dry run, where nothing was
	// read and the serialized field says so.
	if got == nil {
		t.Fatal("drift is nil on a run that read; want a non-nil empty list so the JSON carries the verdict")
	}
}

func TestComputeObserveDriftEvaluatesImageDriftWhenReleaseUnreadableButPodsAvailable(t *testing.T) {
	// release.Found is false with Error set (an unreadable release), yet
	// ImageOverrides still carries an entry — a shape fetchObservedHelmRelease
	// cannot produce today (a failed `helm status` returns before any config
	// is parsed) but the drift computation must not assume that of its input;
	// it evaluates whatever runningImageDrift's own inputs allow.
	req := config.ShellLaunchParams{Namespace: "team-dev"}
	release := &clusterread.ObservedHelmRelease{
		Name:           "team-devops",
		Error:          "observe: not authorized to read helm release",
		ImageOverrides: map[string]string{"erun-devops": "registry.example/erun-devops:1.0.0"},
	}
	pods := []ObservedPod{{
		Name: "team-devops-abc123",
		Containers: []ObservedContainer{{
			Name:  "erun-devops",
			Image: "registry.example/erun-devops:1.0.1-hotfix",
		}},
	}}

	got := computeObserveDrift(req, release, pods)

	found := false
	for _, finding := range got {
		if strings.Contains(finding, "imageOverrides.erun-devops") && strings.Contains(finding, "1.0.1-hotfix") {
			found = true
		}
	}
	if !found {
		t.Fatalf("drift = %v, want an image drift finding even though the release read failed", got)
	}
}

func TestComputeObserveDriftEvaluatesPodDriftWhenReleaseUnreadableButRuntimePodAvailable(t *testing.T) {
	req := config.ShellLaunchParams{Namespace: "team-dev", RuntimePod: config.RuntimePodResources{CPU: "4", Memory: "8916Mi"}}
	release := &clusterread.ObservedHelmRelease{
		Name:       "team-devops",
		Error:      "observe: not authorized to read helm release",
		RuntimePod: config.RuntimePodResources{CPU: "2", Memory: "4096Mi"},
	}

	got := computeObserveDrift(req, release, nil)

	found := false
	for _, finding := range got {
		if strings.Contains(finding, "runtime.resources.limits.cpu") {
			found = true
		}
	}
	if !found {
		t.Fatalf("drift = %v, want a runtimepod drift finding even though the release read failed", got)
	}
}

func TestComputeObserveDriftFoundReleaseKeepsExistingChecks(t *testing.T) {
	req := config.ShellLaunchParams{RuntimeVersion: "1.0.0", RuntimeImage: "registry.example/erun-devops:1.0.0", Namespace: "team-dev"}
	release := &clusterread.ObservedHelmRelease{
		Name:           "team-devops",
		Found:          true,
		AppVersion:     "1.0.1",
		ImageOverrides: map[string]string{"erun-devops": "registry.example/erun-devops:1.0.1"},
	}

	got := computeObserveDrift(req, release, nil)

	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "does not match the release's app version") {
		t.Fatalf("drift = %v, want the recordedVersion/AppVersion mismatch finding", got)
	}
	if !strings.Contains(joined, "does not match the release's imageOverrides.erun-devops") {
		t.Fatalf("drift = %v, want the runtimeimage/imageOverrides mismatch finding", got)
	}
}
