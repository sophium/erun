package eruncommon

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// runtimePodRequestsList is `kubectl get pods -o json` for a local-agent
// environment as the chart actually shapes it: the runtime container and the
// erun-dind sidecar each declaring the chart's fixed 0.25 CPU / 1024Mi request
// while their limits are the tens of GiB the operator chose, plus the three
// small init containers. This is the state the whole reading exists for -- the
// pod's limits say 27Gi/20Gi and its reservation is 0.5 CPU / 2GiB -- so the
// numbers below are the ones the report has to state.
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

// TestUsageRequestsReportThePodsOwnDeclaredRequests is the reading's own
// subject: the request the scheduler admits the runtime pod on, taken from the
// pod spec where it actually lives. Before this reading existed nothing
// anywhere in erun carried a request at all, so `erun usage` and the desktop
// card could only ever report the cgroup limit -- a ceiling that reserves
// nothing -- as if it were provisioned.
func TestUsageRequestsReportThePodsOwnDeclaredRequests(t *testing.T) {
	requests, err := parseRuntimePodRequests([]byte(runtimePodRequestsList))
	if err != nil {
		t.Fatalf("parseRuntimePodRequests: %v", err)
	}
	if requests.Unavailable != "" {
		t.Fatalf("expected a reading, got unavailable: %s", requests.Unavailable)
	}
	runtime, ok := requests.Containers[DevopsComponentName]
	if !ok {
		t.Fatalf("expected the runtime container's own request, got %+v", requests.Containers)
	}
	// 0.25 declared in cores and 250m declared in millicores must land on the
	// same number: the chart's runtime container and the sidecar declare the
	// same reservation in different spellings, and a renderer that showed them
	// differently would invent a difference that is not there.
	if runtime.CPUMilli != 250 {
		t.Errorf("runtime container CPU request = %d millicores, want 250", runtime.CPUMilli)
	}
	if runtime.MemoryBytes != 1024*(1<<20) {
		t.Errorf("runtime container memory request = %d bytes, want 1024Mi", runtime.MemoryBytes)
	}
	dind := requests.Containers[runtimeDindContainerName]
	if dind.CPUMilli != 250 || dind.MemoryBytes != 1024*(1<<20) {
		t.Errorf("expected the erun-dind sidecar's own 0.25 CPU / 1024Mi request, got %+v", dind)
	}
}

// TestUsageRequestsSumTheContainersIntoThePodsOwnRequest is the other half of
// the reading: the pod's reservation is the scheduler's own number, not any one
// container's. Kubernetes admits a pod on max(max over the init containers, sum
// over the containers), so the chart's two 0.25 CPU / 1024Mi containers reserve
// 0.5 CPU / 2GiB between them -- the figure a node is sized against.
func TestUsageRequestsSumTheContainersIntoThePodsOwnRequest(t *testing.T) {
	requests, err := parseRuntimePodRequests([]byte(runtimePodRequestsList))
	if err != nil {
		t.Fatalf("parseRuntimePodRequests: %v", err)
	}
	if requests.Pod.CPUMilli != 500 {
		t.Errorf("pod CPU request = %d millicores, want 500 (0.25 + 0.25)", requests.Pod.CPUMilli)
	}
	if requests.Pod.MemoryBytes != 2048*(1<<20) {
		t.Errorf("pod memory request = %d bytes, want 2Gi", requests.Pod.MemoryBytes)
	}
}

// TestUsageRequestsTakeTheLargerOfInitPeakAndContainerSum covers the half of
// the scheduler's rule a naive sum gets wrong. Kubernetes admits a pod on
// max(max over init containers, sum over containers), so a pod whose init
// container asks for more than its containers do reserves the init container's
// figure -- reporting the sum would state a reservation the scheduler never
// made, which is the one direction of error this reading cannot afford.
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

// TestUsageRequestsPreferTheRunningPodDuringARollout covers the window a
// Recreate-strategy rollout spends with two pods under the same label. The
// reading has to report the pod the scheduler admitted -- the Running one --
// rather than whichever the API server happened to list first, which on a
// terminating predecessor would report the size this environment just left.
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

// TestUsageRequestsStateAnUnreadableQuantityRatherThanDroppingIt covers the one
// wrong answer that matters here: a declared request this parser cannot read
// must not silently become zero. A smaller-than-declared reservation is
// indistinguishable from a real one on every surface, and it understates the
// exact figure an operator is sizing a node against.
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

// TestRuntimeUsageCarriesThePodsRequestsAndSurvivesAnUnreadablePodSpec is the
// wiring half: the reading reaches RuntimeUsage.Requests, and a pod spec that
// cannot be read costs the reservation figures alone -- the cgroup reading
// beside them is the one thing this call must never lose.
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
	req := ShellLaunchParams{Tenant: "erun", Environment: "code1", Type: EnvironmentTypeLocalAgent}
	containerRunner := func(_ ShellLaunchParams, _, _ string) (RemoteCommandResult, error) {
		return RemoteCommandResult{Stdout: reading}, nil
	}

	t.Run("read", func(t *testing.T) {
		usage, err := RunRuntimeUsage(Context{}, containerRunner, func([]string) ([]byte, error) {
			return []byte(runtimePodRequestsList), nil
		}, req, RuntimeUsageParams{Interval: time.Second})
		if err != nil {
			t.Fatalf("RunRuntimeUsage: %v", err)
		}
		if usage.Requests == nil || usage.Requests.Pod.CPUMilli != 500 {
			t.Fatalf("expected the pod's 0.5 CPU reservation on the reading, got %+v", usage.Requests)
		}
		if usage.Requests.Unavailable != "" {
			t.Errorf("expected a read reservation, got unavailable: %s", usage.Requests.Unavailable)
		}
	})

	t.Run("unreadable pod spec", func(t *testing.T) {
		usage, err := RunRuntimeUsage(Context{}, containerRunner, func([]string) ([]byte, error) {
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

// TestDryRunTracesThePodSpecReadWithoutInventingAReservation covers the
// dry-run contract: every action the command would take is traced, and nothing
// is read. A zero-valued Requests here would be a reservation of nothing,
// which is a claim rather than an absence.
func TestDryRunTracesThePodSpecReadWithoutInventingAReservation(t *testing.T) {
	req := ShellLaunchParams{Tenant: "erun", Environment: "code1"}
	var trace strings.Builder
	ctx := Context{DryRun: true, Logger: NewLoggerWithWriters(VerbosityTrace, &trace, &trace)}
	usage, err := RunRuntimeUsage(ctx, nil, nil, req, RuntimeUsageParams{Interval: time.Second})
	if err != nil {
		t.Fatalf("RunRuntimeUsage: %v", err)
	}
	if usage.Requests != nil {
		t.Fatalf("expected no Requests on a dry run, got %+v", usage.Requests)
	}
	if !strings.Contains(trace.String(), "get pods -l app=erun-devops") {
		t.Fatalf("expected the dry-run trace to name the pod-spec read, got:\n%s", trace.String())
	}
}

// TestEffectiveKubernetesPodRequestsIsTheOneAdmissionRule covers the exported
// entry point the desktop's node-capacity reading consumes. That reading sums
// the rule over every pod on a node to answer what the scheduler can still
// admit there, so it must get the same answer the per-environment usage
// reading does -- a second spelling of "max over the init containers, sum over
// the containers" would report free capacity the scheduler does not have, and
// would do it silently, since both spellings return a plausible number.
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
	// The containers sum to 500m/2GiB and the init phase peaks at 100m/64Mi, so
	// the containers are what the scheduler admits the pod on.
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

	// A declared quantity this parser cannot read is an error, never a dropped
	// zero: an understated reservation reports free capacity that is not there.
	if _, err := EffectiveKubernetesPodRequests(
		[]KubernetesContainerResources{{Name: "erun-devops", Requests: map[string]string{"cpu": "plenty"}}},
		nil,
	); err == nil {
		t.Fatal("expected an unreadable request quantity to be an error")
	}
}
