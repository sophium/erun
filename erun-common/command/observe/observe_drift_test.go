package observe

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

func TestComputeObserveDriftNilReleaseReturnsNil(t *testing.T) {
	if got := computeObserveDrift(config.ShellLaunchParams{RuntimeVersion: "1.0.0"}, nil, nil); got != nil {
		t.Fatalf("drift = %v, want nil", got)
	}
}

func TestComputeObserveDriftUnreadableReleaseWithNoRecordedVersionReportsNothing(t *testing.T) {
	req := config.ShellLaunchParams{Namespace: "team-dev"}
	release := &model.ObservedHelmRelease{Name: "team-devops", Error: "observe: helm is not installed or not on PATH"}

	got := computeObserveDrift(req, release, nil)
	if len(got) != 0 {
		t.Fatalf("drift = %v, want no findings when the env config never recorded a runtimeversion", got)
	}
	// The run read, so the verdict is an empty list, never nil.
	if got == nil {
		t.Fatal("drift is nil on a run that read; want a non-nil empty list so the JSON carries the verdict")
	}
}

func TestComputeObserveDriftEvaluatesImageDriftWhenReleaseUnreadableButPodsAvailable(t *testing.T) {
	// An unreadable release carrying ImageOverrides cannot occur today, but drift must not assume that.
	req := config.ShellLaunchParams{Namespace: "team-dev"}
	release := &model.ObservedHelmRelease{
		Name:           "team-devops",
		Error:          "observe: not authorized to read helm release",
		ImageOverrides: map[string]string{"erun-devops": "registry.example/erun-devops:1.0.0"},
	}
	pods := []model.ObservedPod{{
		Name: "team-devops-abc123",
		Containers: []model.ObservedContainer{{
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
	req := config.ShellLaunchParams{Namespace: "team-dev", RuntimePod: model.RuntimePodResources{CPU: "4", Memory: "8916Mi"}}
	release := &model.ObservedHelmRelease{
		Name:       "team-devops",
		Error:      "observe: not authorized to read helm release",
		RuntimePod: model.RuntimePodResources{CPU: "2", Memory: "4096Mi"},
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
