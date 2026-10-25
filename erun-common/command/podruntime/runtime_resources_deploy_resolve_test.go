package podruntime

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config/runtimeresources"
	"github.com/sophium/erun/erun-common/base/model"
)

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

func TestResolveRuntimePodResourcesForDeployReadsCgroupWhenUnconfigured(t *testing.T) {
	root := t.TempDir()
	writeCgroupFixture(t, root, map[string]string{
		"cpu.max":        "4000000 1000000\n", // 4 cores
		"memory.max":     "8589934592\n",      // 8192Mi in bytes
		"memory.current": "1073741824\n",
	})
	env := inPodEnv("remote-agent", "erun", "ux")

	got := runtimeresources.ResolveRuntimePodResourcesForDeploy(model.RuntimePodResources{}, "erun", "ux", env, root)

	if got.Memory != "8192Mi" {
		t.Fatalf("resolved memory = %q, want the live cgroup limit 8192Mi, not the package default %q", got.Memory, runtimeresources.DefaultRuntimePodMemory)
	}
	if got.CPU != "4" {
		t.Fatalf("resolved cpu = %q, want the live cgroup quota 4", got.CPU)
	}
	if got.Memory == runtimeresources.DefaultRuntimePodMemory {
		t.Fatal("resolved memory must not be the package default when a live cgroup limit was readable")
	}
}

func TestResolveRuntimePodResourcesForDeployPrefersConfiguredValue(t *testing.T) {
	root := t.TempDir()
	writeCgroupFixture(t, root, map[string]string{
		"cpu.max":    "8000000 1000000\n",
		"memory.max": "17179869184\n", // 16384Mi
	})
	env := inPodEnv("remote-agent", "erun", "ux")

	configured := model.RuntimePodResources{CPU: "2", Memory: "8192Mi"}
	got := runtimeresources.ResolveRuntimePodResourcesForDeploy(configured, "erun", "ux", env, root)

	if got != configured {
		t.Fatalf("resolved = %+v, want the configured value %+v unchanged", got, configured)
	}
}

func TestResolveRuntimePodResourcesForDeployIgnoresCgroupOffPod(t *testing.T) {
	root := t.TempDir()
	writeCgroupFixture(t, root, map[string]string{
		"cpu.max":    "4000000 1000000\n",
		"memory.max": "8589934592\n",
	})
	env := func(string) string { return "" }

	got := runtimeresources.ResolveRuntimePodResourcesForDeploy(model.RuntimePodResources{}, "erun", "ux", env, root)

	if got != (model.RuntimePodResources{}) {
		t.Fatalf("resolved = %+v, want unchanged (empty) when not running in a runtime pod", got)
	}
}

func TestResolveRuntimePodResourcesForDeployIgnoresCgroupOfOtherEnvironment(t *testing.T) {
	root := t.TempDir()
	writeCgroupFixture(t, root, map[string]string{
		"cpu.max":    "4000000 1000000\n",
		"memory.max": "8589934592\n",
	})
	env := inPodEnv("remote-agent", "erun", "build")

	got := runtimeresources.ResolveRuntimePodResourcesForDeploy(model.RuntimePodResources{}, "erun", "ux", env, root)

	if got != (model.RuntimePodResources{}) {
		t.Fatalf("resolved = %+v, want unchanged (empty) for a mismatched tenant/environment", got)
	}
}

// Integration scenarios simulate pod identity without ERUN_ENV_TYPE and have no real
// cgroup, so ERUN_ENV_TYPE must gate the read.
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

	got := runtimeresources.ResolveRuntimePodResourcesForDeploy(model.RuntimePodResources{}, "erun", "ux", env, root)

	if got != (model.RuntimePodResources{}) {
		t.Fatalf("resolved = %+v, want unchanged (empty) without a real ERUN_ENV_TYPE-carrying pod identity", got)
	}
}
