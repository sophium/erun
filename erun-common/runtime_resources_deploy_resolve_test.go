package eruncommon

import (
	"testing"
)

// inPodEnv builds an env lookup matching what the runtime chart actually
// injects into a pod: ERUN_ENV_TYPE plus the ERUN_TENANT/ERUN_ENVIRONMENT
// identity pair.
func inPodEnv(envType, tenant, environment string) func(string) string {
	return func(key string) string {
		switch key {
		case "ERUN_ENV_TYPE":
			return envType
		case "ERUN_TENANT":
			return tenant
		case "ERUN_ENVIRONMENT":
			return environment
		default:
			return ""
		}
	}
}

// TestResolveRuntimePodResourcesForDeployReadsCgroupWhenUnconfigured pins the
// fix for an in-pod deploy of this environment's own runtime pod, with no
// RuntimePod recorded in the (deliberately partial) in-pod env config, must
// resolve the pod's own live cgroup memory/CPU limit rather than
// NormalizeRuntimePodResources' package default (DefaultRuntimePodMemory /
// DefaultRuntimePodCPU). Without the fix this returns an empty
// RuntimePodResources, which the caller then normalizes to 8916Mi/4 — masking
// this environment's real, deliberately-set 8192Mi/4 limit.
func TestResolveRuntimePodResourcesForDeployReadsCgroupWhenUnconfigured(t *testing.T) {
	root := t.TempDir()
	writeCgroupFixture(t, root, map[string]string{
		"cpu.max":        "4000000 1000000\n", // 4 cores
		"memory.max":     "8589934592\n",      // 8192Mi in bytes
		"memory.current": "1073741824\n",
	})
	env := inPodEnv("remote-agent", "erun", "ux")

	got := resolveRuntimePodResourcesForDeploy(RuntimePodResources{}, "erun", "ux", env, root)

	if got.Memory != "8192Mi" {
		t.Fatalf("resolved memory = %q, want the live cgroup limit 8192Mi, not the package default %q", got.Memory, DefaultRuntimePodMemory)
	}
	if got.CPU != "4" {
		t.Fatalf("resolved cpu = %q, want the live cgroup quota 4", got.CPU)
	}
	if got.Memory == DefaultRuntimePodMemory {
		t.Fatal("resolved memory must not be the package default when a live cgroup limit was readable")
	}
}

// TestResolveRuntimePodResourcesForDeployPrefersConfiguredValue proves a
// configured value always wins: the cgroup fixture here reports a different
// size than the configured one, and the configured one must survive
// untouched — this fix stands in only for a field the config never carried,
// never overrides one it did.
func TestResolveRuntimePodResourcesForDeployPrefersConfiguredValue(t *testing.T) {
	root := t.TempDir()
	writeCgroupFixture(t, root, map[string]string{
		"cpu.max":    "8000000 1000000\n",
		"memory.max": "17179869184\n", // 16384Mi
	})
	env := inPodEnv("remote-agent", "erun", "ux")

	configured := RuntimePodResources{CPU: "2", Memory: "8192Mi"}
	got := resolveRuntimePodResourcesForDeploy(configured, "erun", "ux", env, root)

	if got != configured {
		t.Fatalf("resolved = %+v, want the configured value %+v unchanged", got, configured)
	}
}

// TestResolveRuntimePodResourcesForDeployIgnoresCgroupOffPod covers a
// host-side deploy (no injected identity at all): the cgroup fixture must
// never be consulted, since a host process has no runtime pod cgroup of its
// own to read, and letting an unrelated local cgroup leak in would be worse
// than the default it replaces.
func TestResolveRuntimePodResourcesForDeployIgnoresCgroupOffPod(t *testing.T) {
	root := t.TempDir()
	writeCgroupFixture(t, root, map[string]string{
		"cpu.max":    "4000000 1000000\n",
		"memory.max": "8589934592\n",
	})
	env := func(string) string { return "" }

	got := resolveRuntimePodResourcesForDeploy(RuntimePodResources{}, "erun", "ux", env, root)

	if got != (RuntimePodResources{}) {
		t.Fatalf("resolved = %+v, want unchanged (empty) when not running in a runtime pod", got)
	}
}

// TestResolveRuntimePodResourcesForDeployIgnoresCgroupOfOtherEnvironment
// covers an in-pod deploy whose target tenant/environment does not match the
// identity the chart injected into this pod — the cgroup limits visible here
// belong to a different environment's pod and must never be attributed to
// the one being deployed.
func TestResolveRuntimePodResourcesForDeployIgnoresCgroupOfOtherEnvironment(t *testing.T) {
	root := t.TempDir()
	writeCgroupFixture(t, root, map[string]string{
		"cpu.max":    "4000000 1000000\n",
		"memory.max": "8589934592\n",
	})
	env := inPodEnv("remote-agent", "erun", "build")

	got := resolveRuntimePodResourcesForDeploy(RuntimePodResources{}, "erun", "ux", env, root)

	if got != (RuntimePodResources{}) {
		t.Fatalf("resolved = %+v, want unchanged (empty) for a mismatched tenant/environment", got)
	}
}

// TestResolveRuntimePodResourcesForDeployRequiresRealPodIdentity pins the
// regression this fix's first cut produced: erun-integration's own
// in-pod-shaped deploy scenarios inject ERUN_TENANT/ERUN_ENVIRONMENT to
// simulate pod identity, exactly like a real chart-rendered pod does, but
// never set ERUN_ENV_TYPE — because there is no real pod, and so no real
// cgroup, behind that simulation. Gating the cgroup read on the
// tenant/environment pair alone made the resolved value depend on whatever
// unrelated cgroup happened to exist on the machine running the test
// (TestDeploy/in_pod_local_agent_component_deploy_allowed's golden started
// reporting this environment's own live limits instead of the fixed
// defaults the golden pins). IsInRuntimeEnvironment's ERUN_ENV_TYPE check is
// what a real pod always carries and a same-machine simulation does not, so
// it must gate the read even when the tenant/environment pair matches.
func TestResolveRuntimePodResourcesForDeployRequiresRealPodIdentity(t *testing.T) {
	root := t.TempDir()
	writeCgroupFixture(t, root, map[string]string{
		"cpu.max":        "6000000 1000000\n",
		"memory.max":     "6442450944\n", // 6144Mi
		"memory.current": "1073741824\n",
	})
	env := func(key string) string {
		switch key {
		case "ERUN_TENANT":
			return "erun"
		case "ERUN_ENVIRONMENT":
			return "ux"
		default:
			return ""
		}
	}

	got := resolveRuntimePodResourcesForDeploy(RuntimePodResources{}, "erun", "ux", env, root)

	if got != (RuntimePodResources{}) {
		t.Fatalf("resolved = %+v, want unchanged (empty) without a real ERUN_ENV_TYPE-carrying pod identity", got)
	}
}
