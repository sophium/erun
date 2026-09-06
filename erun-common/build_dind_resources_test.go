package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// fakeDockerStoreForDindResources is the minimal DockerStore double
// resolveDockerBuildDindPodResources needs: one tenant with one environment
// whose LocalRepoPath matches the project root under test.
type fakeDockerStoreForDindResources struct {
	tenant TenantConfig
	envs   []EnvConfig
}

func (f fakeDockerStoreForDindResources) ListTenantConfigs() ([]TenantConfig, error) {
	return []TenantConfig{f.tenant}, nil
}

func (f fakeDockerStoreForDindResources) LoadTenantConfig(name string) (TenantConfig, string, error) {
	if name == f.tenant.Name {
		return f.tenant, "", nil
	}
	return TenantConfig{}, "", fmt.Errorf("tenant %q not found", name)
}

func (f fakeDockerStoreForDindResources) ListEnvConfigs(tenant string) ([]EnvConfig, error) {
	if tenant != f.tenant.Name {
		return nil, nil
	}
	return f.envs, nil
}

// storeWithDindConfig builds a store whose one environment matches projectRoot
// and carries the given RuntimeDindPod, so resolveDockerBuildEnvConfigForProject
// resolves it as the "config present" case.
func storeWithDindConfig(t *testing.T, projectRoot string, resources RuntimePodResources) DockerStore {
	t.Helper()
	return fakeDockerStoreForDindResources{
		tenant: TenantConfig{Name: "acme"},
		envs: []EnvConfig{{
			Name:           "dev",
			LocalRepoPath:  filepath.Clean(projectRoot),
			RuntimeDindPod: resources,
		}},
	}
}

// clearInjectedRuntimeEnv strips the real process's own ERUN_TENANT/
// ERUN_ENVIRONMENT so resolveDockerBuildDindPodResources' config-store
// resolution is deterministic under `go test` regardless of whether the test
// binary happens to run inside a real runtime pod (which sets both).
func clearInjectedRuntimeEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ERUN_TENANT", "")
	t.Setenv("ERUN_ENVIRONMENT", "")
}

// Both env vars set and valid: they win even though the config store also
// resolves a real environment with different values, because they reflect the
// sidecar's live limit and the config store's projection can be stale or (in
// the in-pod case this exists to fix) simply absent.
func TestResolveDockerBuildDindPodResourcesPrefersEnvVarsOverConfig(t *testing.T) {
	clearInjectedRuntimeEnv(t)
	t.Setenv(DindCPULimitEnvVar, "8")
	t.Setenv(DindMemoryLimitMiBEnvVar, "8192")

	dir := t.TempDir()
	store := storeWithDindConfig(t, dir, RuntimePodResources{CPU: "2", Memory: "4096Mi"})

	got := resolveDockerBuildDindPodResources(store, dir, "dev")
	want := RuntimePodResources{CPU: "8", Memory: "8192Mi"}
	if got != want {
		t.Fatalf("expected env vars to win, got %+v want %+v", got, want)
	}
}

// No env vars: falls back to the config store's resolved environment, exactly
// the host-driven-build behavior that must keep working unchanged.
func TestResolveDockerBuildDindPodResourcesFallsBackToConfigWhenEnvVarsAbsent(t *testing.T) {
	clearInjectedRuntimeEnv(t)

	dir := t.TempDir()
	store := storeWithDindConfig(t, dir, RuntimePodResources{CPU: "2", Memory: "4096Mi"})

	got := resolveDockerBuildDindPodResources(store, dir, "dev")
	want := RuntimePodResources{CPU: "2", Memory: "4096Mi"}
	if got != want {
		t.Fatalf("expected the config-store value, got %+v want %+v", got, want)
	}
}

// Neither env vars nor a matching environment: the conservative hardcoded
// defaults, never the host node's raw capacity.
func TestResolveDockerBuildDindPodResourcesFallsBackToDefaultsWhenNeitherEnvNorConfig(t *testing.T) {
	clearInjectedRuntimeEnv(t)

	dir := t.TempDir()
	got := resolveDockerBuildDindPodResources(ConfigStore{}, dir, "dev")
	want := RuntimePodResources{CPU: DefaultRuntimeDindCPU, Memory: DefaultRuntimeDindMemory}
	if got != want {
		t.Fatalf("expected the hardcoded defaults, got %+v want %+v", got, want)
	}
}

// A malformed env var must not mask a real config-store value with a bad
// literal, and the two fields resolve independently: CPU falls back to the
// config store while memory still takes the (valid) env var.
func TestResolveDockerBuildDindPodResourcesIgnoresMalformedEnvVarPerField(t *testing.T) {
	clearInjectedRuntimeEnv(t)
	t.Setenv(DindCPULimitEnvVar, "not-a-cpu-quantity")
	t.Setenv(DindMemoryLimitMiBEnvVar, "8192")

	dir := t.TempDir()
	store := storeWithDindConfig(t, dir, RuntimePodResources{CPU: "2", Memory: "4096Mi"})

	got := resolveDockerBuildDindPodResources(store, dir, "dev")
	want := RuntimePodResources{CPU: "2", Memory: "8192Mi"}
	if got != want {
		t.Fatalf("expected malformed CPU to fall back to config and memory to take the env var, got %+v want %+v", got, want)
	}
}

// A malformed env var with no config-store environment to fall back to lands
// on the hardcoded default for that field only.
func TestResolveDockerBuildDindPodResourcesIgnoresMalformedEnvVarWithNoConfig(t *testing.T) {
	clearInjectedRuntimeEnv(t)
	t.Setenv(DindCPULimitEnvVar, "8")
	t.Setenv(DindMemoryLimitMiBEnvVar, "not-a-memory-quantity")

	dir := t.TempDir()
	got := resolveDockerBuildDindPodResources(ConfigStore{}, dir, "dev")
	want := RuntimePodResources{CPU: "8", Memory: DefaultRuntimeDindMemory}
	if got != want {
		t.Fatalf("expected valid CPU env var and default memory, got %+v want %+v", got, want)
	}
}

// applyDindResourceBuildArgs is the caller that actually threads the resolved
// resources into the real --build-arg values docker build receives; prove the
// env-var path reaches it end to end through a Dockerfile that declares both
// ARGs, the same shape as erun-devops/docker/erun-devops/Dockerfile.
func TestApplyDindResourceBuildArgsUsesEnvVarsInPod(t *testing.T) {
	clearInjectedRuntimeEnv(t)
	t.Setenv(DindCPULimitEnvVar, "8")
	t.Setenv(DindMemoryLimitMiBEnvVar, "8192")

	dir := t.TempDir()
	dockerfilePath := filepath.Join(dir, "Dockerfile")
	dockerfile := "FROM debian:bookworm\nARG DIND_MEMORY_LIMIT_MIB=20480\nARG DIND_CPU_LIMIT=4\n"
	if err := os.WriteFile(dockerfilePath, []byte(dockerfile), 0o644); err != nil {
		t.Fatalf("write dockerfile: %v", err)
	}

	build := &DockerBuildSpec{DockerfilePath: dockerfilePath}
	applyDindResourceBuildArgs(ConfigStore{}, dir, "dev", build)

	if build.DindCPULimit != "8" {
		t.Fatalf("expected DindCPULimit 8 (from %s), got %q", DindCPULimitEnvVar, build.DindCPULimit)
	}
	if build.DindMemoryLimitMiB != "8192" {
		t.Fatalf("expected DindMemoryLimitMiB 8192 (from %s), got %q", DindMemoryLimitMiBEnvVar, build.DindMemoryLimitMiB)
	}
}
