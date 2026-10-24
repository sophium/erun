package build

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

type fakeDockerStoreForDindResources struct {
	tenant model.TenantConfig
	envs   []config.EnvConfig
}

func (f fakeDockerStoreForDindResources) ListTenantConfigs() ([]model.TenantConfig, error) {
	return []model.TenantConfig{f.tenant}, nil
}

func (f fakeDockerStoreForDindResources) LoadTenantConfig(name string) (model.TenantConfig, string, error) {
	if name == f.tenant.Name {
		return f.tenant, "", nil
	}
	return model.TenantConfig{}, "", fmt.Errorf("tenant %q not found", name)
}

func (f fakeDockerStoreForDindResources) ListEnvConfigs(tenant string) ([]config.EnvConfig, error) {
	if tenant != f.tenant.Name {
		return nil, nil
	}
	return f.envs, nil
}

func storeWithDindConfig(t *testing.T, projectRoot string, resources model.RuntimePodResources) shared.DockerStore {
	t.Helper()
	return fakeDockerStoreForDindResources{
		tenant: model.TenantConfig{Name: "acme"},
		envs: []config.EnvConfig{{
			Name:           "dev",
			LocalRepoPath:  filepath.Clean(projectRoot),
			RuntimeDindPod: resources,
		}},
	}
}

// clearInjectedRuntimeEnv makes resolution deterministic when tests run inside a runtime pod.
func clearInjectedRuntimeEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ERUN_TENANT", "")
	t.Setenv("ERUN_ENVIRONMENT", "")
	t.Setenv(shared.DindCPULimitEnvVar, "")
	t.Setenv(shared.DindMemoryLimitMiBEnvVar, "")
}

// Env vars reflect the sidecar's live limit; the config store can be stale or absent.
func TestResolveDockerBuildDindPodResourcesPrefersEnvVarsOverConfig(t *testing.T) {
	clearInjectedRuntimeEnv(t)
	t.Setenv(shared.DindCPULimitEnvVar, "8")
	t.Setenv(shared.DindMemoryLimitMiBEnvVar, "8192")

	dir := t.TempDir()
	store := storeWithDindConfig(t, dir, model.RuntimePodResources{CPU: "2", Memory: "4096Mi"})

	got := shared.ResolveDockerBuildDindPodResources(store, dir, "dev")
	want := model.RuntimePodResources{CPU: "8", Memory: "8192Mi"}
	if got != want {
		t.Fatalf("expected env vars to win, got %+v want %+v", got, want)
	}
}

func TestResolveDockerBuildDindPodResourcesIgnoresMalformedEnvVarPerField(t *testing.T) {
	clearInjectedRuntimeEnv(t)
	t.Setenv(shared.DindCPULimitEnvVar, "not-a-cpu-quantity")
	t.Setenv(shared.DindMemoryLimitMiBEnvVar, "8192")

	dir := t.TempDir()
	store := storeWithDindConfig(t, dir, model.RuntimePodResources{CPU: "2", Memory: "4096Mi"})

	got := shared.ResolveDockerBuildDindPodResources(store, dir, "dev")
	want := model.RuntimePodResources{CPU: "2", Memory: "8192Mi"}
	if got != want {
		t.Fatalf("expected malformed CPU to fall back to config and memory to take the env var, got %+v want %+v", got, want)
	}
}

func TestResolveDockerBuildDindPodResourcesIgnoresMalformedEnvVarWithNoConfig(t *testing.T) {
	clearInjectedRuntimeEnv(t)
	t.Setenv(shared.DindCPULimitEnvVar, "8")
	t.Setenv(shared.DindMemoryLimitMiBEnvVar, "not-a-memory-quantity")

	dir := t.TempDir()
	got := shared.ResolveDockerBuildDindPodResources(config.ConfigStore{}, dir, "dev")
	want := model.RuntimePodResources{CPU: "8", Memory: config.DefaultRuntimeDindMemory}
	if got != want {
		t.Fatalf("expected valid CPU env var and default memory, got %+v want %+v", got, want)
	}
}

func TestApplyDindResourceBuildArgsUsesEnvVarsInPod(t *testing.T) {
	clearInjectedRuntimeEnv(t)
	t.Setenv(shared.DindCPULimitEnvVar, "8")
	t.Setenv(shared.DindMemoryLimitMiBEnvVar, "8192")

	dir := t.TempDir()
	dockerfilePath := filepath.Join(dir, "Dockerfile")
	dockerfile := "FROM debian:bookworm\nARG DIND_MEMORY_LIMIT_MIB=20480\nARG DIND_CPU_LIMIT=4\n"
	if err := os.WriteFile(dockerfilePath, []byte(dockerfile), 0o644); err != nil {
		t.Fatalf("write dockerfile: %v", err)
	}

	build := &shared.DockerBuildSpec{DockerfilePath: dockerfilePath}
	shared.ApplyDindResourceBuildArgs(config.ConfigStore{}, dir, "dev", build)

	if build.DindCPULimit != "8" {
		t.Fatalf("expected DindCPULimit 8 (from %s), got %q", shared.DindCPULimitEnvVar, build.DindCPULimit)
	}
	if build.DindMemoryLimitMiB != "8192" {
		t.Fatalf("expected DindMemoryLimitMiB 8192 (from %s), got %q", shared.DindMemoryLimitMiBEnvVar, build.DindMemoryLimitMiB)
	}
}
