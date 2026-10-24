package podruntime

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/build"
)

// runtimePodRequestsList mirrors the chart: limits in tens of GiB, requests of 0.25 CPU /
// 1024Mi per container.
const runtimePodRequestsList = `{
  "items": [
    {
      "metadata": {"name": "erun-devops-7d9f-abcde"},
      "spec": {
        "containers": [
          {
            "name": "erun-devops",
            "resources": {"limits": {"cpu": "14", "memory": "27Gi"}, "requests": {"cpu": "0.25", "memory": "1024Mi"}}
          },
          {
            "name": "erun-dind",
            "resources": {"limits": {"cpu": "8", "memory": "20Gi"}, "requests": {"cpu": "250m", "memory": "1024Mi"}}
          }
        ],
        "initContainers": [
          {"name": "prepare-volumes", "resources": {"requests": {"cpu": "100m", "memory": "64Mi"}}},
          {"name": "install-binfmt", "resources": {"requests": {"cpu": "100m", "memory": "64Mi"}}}
        ]
      },
      "status": {"phase": "Running"}
    }
  ]
}`

func TestUsageRequestsReportThePodsOwnDeclaredRequests(t *testing.T) {
	requests, err := parseRuntimePodRequests([]byte(runtimePodRequestsList))
	if err != nil {
		t.Fatalf("parseRuntimePodRequests: %v", err)
	}
	if requests.Unavailable != "" {
		t.Fatalf("expected a reading, got unavailable: %s", requests.Unavailable)
	}
	runtime, ok := requests.Containers[config.DevopsComponentName]
	if !ok {
		t.Fatalf("expected the runtime container's own request, got %+v", requests.Containers)
	}
	// 0.25 cores and 250m must land on the same number.
	if runtime.CPUMilli != 250 {
		t.Errorf("runtime container CPU request = %d millicores, want 250", runtime.CPUMilli)
	}
	if runtime.MemoryBytes != 1024*(1<<20) {
		t.Errorf("runtime container memory request = %d bytes, want 1024Mi", runtime.MemoryBytes)
	}
	dind := requests.Containers[build.RuntimeDindContainerName]
	if dind.CPUMilli != 250 || dind.MemoryBytes != 1024*(1<<20) {
		t.Errorf("expected the erun-dind sidecar's own 0.25 CPU / 1024Mi request, got %+v", dind)
	}
}

// Kubernetes admits a pod on max(max over init containers, sum over containers).
func TestUsageRequestsTakeTheLargerOfInitPeakAndContainerSum(t *testing.T) {
	pod := `{"items": [{
      "metadata": {"name": "erun-devops-1"},
      "spec": {
        "containers": [{"name": "erun-devops", "resources": {"requests": {"cpu": "250m", "memory": "1024Mi"}}}],
        "initContainers": [{"name": "prepare-volumes", "resources": {"requests": {"cpu": "2", "memory": "4Gi"}}}]
      },
      "status": {"phase": "Running"}
    }]}`
	requests, err := parseRuntimePodRequests([]byte(pod))
	if err != nil {
		t.Fatalf("parseRuntimePodRequests: %v", err)
	}
	if requests.Pod.CPUMilli != 2000 || requests.Pod.MemoryBytes != 4096*(1<<20) {
		t.Fatalf("pod request = %d millicores / %d bytes, want the init container's 2 CPU / 4Gi", requests.Pod.CPUMilli, requests.Pod.MemoryBytes)
	}
}

func TestUsageRequestsPreferTheRunningPodDuringARollout(t *testing.T) {
	pods := `{"items": [
      {
        "metadata": {"name": "erun-devops-old"},
        "spec": {"containers": [{"name": "erun-devops", "resources": {"requests": {"cpu": "12", "memory": "16Gi"}}}]},
        "status": {"phase": "Pending"}
      },
      {
        "metadata": {"name": "erun-devops-new"},
        "spec": {"containers": [{"name": "erun-devops", "resources": {"requests": {"cpu": "0.25", "memory": "1024Mi"}}}]},
        "status": {"phase": "Running"}
      }
    ]}`
	requests, err := parseRuntimePodRequests([]byte(pods))
	if err != nil {
		t.Fatalf("parseRuntimePodRequests: %v", err)
	}
	if requests.Pod.CPUMilli != 250 {
		t.Fatalf("pod request = %d millicores, want the Running pod's 0.25 CPU", requests.Pod.CPUMilli)
	}
}

func TestUsageRequestsStateAnUnreadableQuantityRatherThanDroppingIt(t *testing.T) {
	pod := `{"items": [{
      "metadata": {"name": "erun-devops-1"},
      "spec": {"containers": [{"name": "erun-devops", "resources": {"requests": {"cpu": "lots"}}}]},
      "status": {"phase": "Running"}
    }]}`
	raw, err := parseRuntimePodRequests([]byte(pod))
	if err == nil {
		t.Fatalf("expected the unreadable CPU request to be refused, got %+v", raw)
	}
	if !strings.Contains(err.Error(), "lots") {
		t.Errorf("expected the error to quote the value it could not read, got %v", err)
	}
}

func TestRuntimeUsageCarriesThePodsRequestsAndSurvivesAnUnreadablePodSpec(t *testing.T) {
	reading := strings.Join([]string{
		"cgroup_type=cgroup2fs",
		"memory_current=104857600",
		"memory_max=28991029248",
		"memory_peak=104857600",
		"memory_oom_kill=0",
		"cpu_max=1400000 100000",
		"cpu_usage_before=1000000",
		"cpu_usage_after=1003000",
		"cpu_time_before_ns=1000000000",
		"cpu_time_after_ns=2000000000",
		"disk_workspace=overlay 198234112 89006592 99117056 45% /home/erun",
	}, "\n")
	req := config.ShellLaunchParams{Tenant: "erun", Environment: "code1", Type: config.EnvironmentTypeLocalAgent}
	containerRunner := func(_ config.ShellLaunchParams, _, _ string) (kube.RemoteCommandResult, error) {
		return kube.RemoteCommandResult{Stdout: reading}, nil
	}

	t.Run("unreadable pod spec", func(t *testing.T) {
		usage, err := RunRuntimeUsage(core.Context{}, containerRunner, func([]string) ([]byte, error) {
			return nil, errors.New("kubectl get pods: connection refused")
		}, req, RuntimeUsageParams{Interval: time.Second})
		if err != nil {
			t.Fatalf("an unreadable pod spec must not fail the usage call, got: %v", err)
		}
		if usage.Memory.CurrentBytes != 104857600 {
			t.Fatalf("expected the cgroup reading to survive an unreadable pod spec, got %+v", usage.Memory)
		}
		if usage.Requests == nil || usage.Requests.Unavailable == "" {
			t.Fatalf("expected the reservation to report its own unavailability, got %+v", usage.Requests)
		}
	})
}

func TestEffectiveKubernetesPodRequestsIsTheOneAdmissionRule(t *testing.T) {
	requests, err := EffectiveKubernetesPodRequests(
		[]KubernetesContainerResources{
			{Name: "erun-devops", Requests: map[string]string{"cpu": "250m", "memory": "1024Mi"}},
			{Name: "erun-dind", Requests: map[string]string{"cpu": "250m", "memory": "1024Mi"}},
		},
		[]KubernetesContainerResources{
			{Name: "prepare-volumes", Requests: map[string]string{"cpu": "100m", "memory": "64Mi"}},
			{Name: "install-binfmt", Requests: map[string]string{"cpu": "100m", "memory": "64Mi"}},
			{Name: "adopt-worktree", Requests: map[string]string{"cpu": "100m", "memory": "64Mi"}},
		},
	)
	if err != nil {
		t.Fatalf("EffectiveKubernetesPodRequests: %v", err)
	}
	if requests.CPUMilli != 500 || requests.MemoryBytes != 2048*(1<<20) {
		t.Fatalf("pod request = %d millicores / %d bytes, want the containers' 500m / 2GiB", requests.CPUMilli, requests.MemoryBytes)
	}

	raised, err := EffectiveKubernetesPodRequests(
		[]KubernetesContainerResources{{Name: "erun-devops", Requests: map[string]string{"cpu": "250m"}}},
		[]KubernetesContainerResources{{Name: "prepare-volumes", Requests: map[string]string{"cpu": "2"}}},
	)
	if err != nil {
		t.Fatalf("EffectiveKubernetesPodRequests: %v", err)
	}
	if raised.CPUMilli != 2000 {
		t.Fatalf("pod request = %d millicores, want the init phase's 2 CPU", raised.CPUMilli)
	}

	if _, err := EffectiveKubernetesPodRequests(
		[]KubernetesContainerResources{{Name: "erun-devops", Requests: map[string]string{"cpu": "plenty"}}},
		nil,
	); err == nil {
		t.Fatal("expected an unreadable request quantity to be an error")
	}
}
