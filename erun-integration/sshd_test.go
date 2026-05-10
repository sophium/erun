package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestSSHD(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"sshd", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "sshd/help", normalize.Apply(result.Combined))
	})

	t.Run("init_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"sshd", "init", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "sshd/init_help", normalize.Apply(result.Combined))
	})

	t.Run("init_real_run_writes_local_ssh_config", func(t *testing.T) {
		// Exercises the non-dry-run sshd init flow: stub kubectl/helm so
		// the deploy succeeds, the remote-exec for syncing authorized_keys
		// succeeds, and writeLocalSSHConfig writes a real entry to
		// $HOME/.ssh/config via internal/sshconfig.UpsertDefaultConfig.
		// Asserts that the resulting file contains the expected host alias.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		publicKey := filepath.Join(setup.Home, "id_ed25519.pub")
		if err := os.WriteFile(publicKey, []byte("ssh-ed25519 AAAATEST user@example\n"), 0o644); err != nil {
			t.Fatalf("write public key: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "kubectl", "")
		fixture.StubBinary(t, stubs, "helm", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm")...)
		args := []string{"sshd", "init", "team", "dev", "--public-key", publicKey, "--local-port", "64022"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		sshConfig := filepath.Join(setup.Home, ".ssh", "config")
		raw, err := os.ReadFile(sshConfig)
		if err != nil {
			t.Fatalf("read ssh config: %v", err)
		}
		body := string(raw)
		for _, want := range []string{
			"Host erun-team-dev",
			"HostName 127.0.0.1",
			"Port 64022",
			"User erun",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("expected ~/.ssh/config to contain %q, got:\n%s", want, body)
			}
		}
	})

	t.Run("init_dry_run_traces_full_flow", func(t *testing.T) {
		// Exercises sshd.go runSSHDInitCommand: --dry-run must trace the
		// env-config save (write-yaml), the helm deploy that flips
		// SSHDEnabled, the remote authorized_keys exec, and the local SSH
		// config write — without performing any side effect.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		publicKey := filepath.Join(setup.Home, "id_ed25519.pub")
		if err := os.WriteFile(publicKey, []byte("ssh-ed25519 AAAATEST user@example\n"), 0o644); err != nil {
			t.Fatalf("write public key: %v", err)
		}
		args := []string{"sshd", "init", "team", "dev", "--public-key", publicKey, "--local-port", "64022", "--dry-run"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "sshd/init_dry_run_traces_full_flow", normalize.Apply(result.Combined))
	})
}
