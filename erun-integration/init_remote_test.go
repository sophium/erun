package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func initRemoteFlagScenarios(t *testing.T) {
	t.Run("remote_dry_run", testInitRemoteDryRun)
	t.Run("remote_with_runtime_image_override", testInitRemoteWithRuntimeImageOverride)
	// A tagged --runtime-image is persisted tagless, so the deploy this
	// same init performs (and every later redeploy) pins the image to the env's
	// own runtime version instead of sticking to the tag the operator happened
	// to type at init time.
	t.Run("remote_with_tagged_runtime_image_persists_tagless", testInitRemoteWithTaggedRuntimeImagePersistsTagless)
	t.Run("remote_with_runtime_resources", testInitRemoteWithRuntimeResources)
	t.Run("remote_with_dind_resources", testInitRemoteWithDindResources)
	t.Run("remote_without_bootstrap", testInitRemoteWithoutBootstrap)
	t.Run("yes_flag_replaces_confirms", testInitYesFlagReplacesConfirms)
	t.Run("remote_requires_environment", testInitRemoteRequiresEnvironment)
}

func testInitRemoteDryRun(t *testing.T) {
	setup := env.New(t)
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--no-git",
		"--bootstrap",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "init/remote_dry_run", normalize.Apply(result.Combined))
}

func testInitRemoteWithRuntimeImageOverride(t *testing.T) {
	setup := env.New(t)
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--runtime-image", "custom-devops",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--no-git",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "init/remote_with_runtime_image_override", normalize.Apply(result.Combined))
}

func testInitRemoteWithTaggedRuntimeImagePersistsTagless(t *testing.T) {
	setup := env.New(t)
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--runtime-image", "custom-devops:stale-tag",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--no-git",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "init/remote_with_tagged_runtime_image_persists_tagless", normalize.Apply(result.Combined))
}

func testInitRemoteWithRuntimeResources(t *testing.T) {
	setup := env.New(t)
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--runtime-cpu", "8",
		"--runtime-memory", "16Gi",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--no-git",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "init/remote_with_runtime_resources", normalize.Apply(result.Combined))
}

func testInitRemoteWithDindResources(t *testing.T) {
	// --dind-cpu/--dind-memory size the erun-dind sidecar independent of
	// --runtime-cpu/--runtime-memory; the deployed helm command must carry
	// both containers' resolved limits on separate --set-string flags.
	setup := env.New(t)
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--dind-cpu", "6",
		"--dind-memory", "16Gi",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--no-git",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "init/remote_with_dind_resources", normalize.Apply(result.Combined))
}

func testInitRemoteWithoutBootstrap(t *testing.T) {
	setup := env.New(t)
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--no-git",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "init/remote_without_bootstrap", normalize.Apply(result.Combined))
}

func testInitYesFlagReplacesConfirms(t *testing.T) {
	setup := env.New(t)
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--no-git",
		"-y",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "init/yes_flag_replaces_confirms", normalize.Apply(result.Combined))
}

func testInitRemoteRequiresEnvironment(t *testing.T) {
	// --remote without an environment must fail before any side effect runs.
	setup := env.New(t)
	result := erun.Run(t, []string{"init", "--remote", "--tenant", "frs", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "init/remote_requires_environment", normalize.Apply(result.Combined))
}
