package integration

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestInit(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"init", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "init/help", normalize.Apply(result.Combined))
	})

	t.Run("remote_dry_run", func(t *testing.T) {
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
	})

	t.Run("remote_with_runtime_image_override", func(t *testing.T) {
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
	})

	t.Run("remote_with_runtime_resources", func(t *testing.T) {
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
	})

	t.Run("remote_without_bootstrap", func(t *testing.T) {
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
	})

	t.Run("yes_flag_replaces_confirms", func(t *testing.T) {
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
	})

	t.Run("remote_requires_environment", func(t *testing.T) {
		// Exercises init.go --remote validation: passing --remote without
		// an environment must fail with the standard error message
		// before any side effect runs.
		setup := env.New(t)
		result := erun.Run(t, []string{"init", "--remote", "--tenant", "frs", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
		}
		if !strings.Contains(result.Combined, "environment is required with --remote") {
			t.Errorf("expected remote-environment-required error, got:\n%s", result.Combined)
		}
		golden.Equal(t, "init/remote_requires_environment", normalize.Apply(result.Combined))
	})

	t.Run("real_run_via_stubs", func(t *testing.T) {
		// Run init for real (without --dry-run) but route every external
		// call through stubs. Covers the kubectl namespace check/create
		// branches in kubernetes_namespace.go and the helm-runner code in
		// deploy.go that the dry-run path traces but never executes.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")
		fixture.StubBinary(t, stubs, "git", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker", "git")...)
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
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "init/real_run_via_stubs", normalize.Apply(result.Combined))
	})
}
