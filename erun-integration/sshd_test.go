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
		for _, want := range []string{
			"save SSHD config for team/dev",
			"helm upgrade --install",
			"sshdEnabled=true",
			"authorized_keys",
			"ssh-ed25519 AAAATEST user@example",
			"write ssh config host erun-team-dev",
		} {
			if !strings.Contains(result.Combined, want) {
				t.Errorf("expected dry-run trace to contain %q, got combined output:\n%s", want, result.Combined)
			}
		}
	})
}
