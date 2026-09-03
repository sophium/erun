package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestDevops(t *testing.T) {
	t.Parallel()
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"devops", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "devops/help", normalize.Apply(result.Combined))
	})

	t.Run("container_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"devops", "container", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "devops/container_help", normalize.Apply(result.Combined))
	})

	t.Run("k8s_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"devops", "k8s", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "devops/k8s_help", normalize.Apply(result.Combined))
	})

	t.Run("k8s_deploy_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"devops", "k8s", "deploy", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "devops/k8s_deploy_help", normalize.Apply(result.Combined))
	})
}
