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

	t.Run("init_dry_run_workspace_sync_resolves_project_root", func(t *testing.T) {
		// Exercises resolveSSHDWorkspaceSyncLocalPath: with
		// sshd.workspacesync.enabled persisted on the env and the command
		// running from inside a git project, init must resolve the sync
		// local path to the project root and trace the workspace-sync
		// enable (saveSSHDEnvConfig's dry-run sync arm).
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		envCfg := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		body := "name: dev\n" +
			"repopath: /home/erun/git/team\n" +
			"kubernetescontext: test-context\n" +
			"containerregistry: registry.example/test\n" +
			"runtimeversion: 1.0.0\n" +
			"type: remote-agent\n" +
			"sshd:\n" +
			"  workspacesync:\n" +
			"    enabled: true\n"
		if err := os.WriteFile(envCfg, []byte(body), 0o644); err != nil {
			t.Fatalf("rewrite env config with workspace sync: %v", err)
		}
		fixture.SeedGitRepo(t, setup.Cwd)
		publicKey := filepath.Join(setup.Home, "id_ed25519.pub")
		if err := os.WriteFile(publicKey, []byte("ssh-ed25519 AAAATEST user@example\n"), 0o644); err != nil {
			t.Fatalf("write public key: %v", err)
		}
		args := []string{"sshd", "init", "team", "dev", "--public-key", publicKey, "--local-port", "64022", "--dry-run"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "sshd/init_dry_run_workspace_sync_resolves_project_root", normalize.Apply(result.Combined))
	})

	t.Run("init_real_run_remote_exec_failure_surfaces_stderr", func(t *testing.T) {
		// Exercises syncRemoteSSHDKey's non-retryable failure arm: the
		// kubectl exec that syncs authorized_keys fails with a stderr that
		// does not match sshdRemoteExecNeedsDeploymentRetry's pod-churn
		// tokens, so the command must fail immediately (no retry loop) and
		// the error must carry the remote stderr via
		// formatRemoteCommandStderr. The argv-branching kubectl stub fails
		// only the exec; namespace/wait/helm calls succeed so the flow
		// reaches the sync step.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		publicKey := filepath.Join(setup.Home, "id_ed25519.pub")
		if err := os.WriteFile(publicKey, []byte("ssh-ed25519 AAAATEST user@example\n"), 0o644); err != nil {
			t.Fatalf("write public key: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "kubectl", strings.Join([]string{
			`case "$*" in`,
			`  *" exec "*)`,
			`    printf '%s\n' 'error: unable to upgrade connection: Forbidden (user cannot exec into pod)' >&2`,
			`    exit 1 ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n"))
		fixture.StubBinary(t, stubs, "helm", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm")...)
		args := []string{"sshd", "init", "team", "dev", "--public-key", publicKey, "--local-port", "64022"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when remote exec fails, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "sshd/init_real_run_remote_exec_failure_surfaces_stderr", normalize.Apply(result.Combined))
	})

	t.Run("init_real_run_retries_after_pod_not_found", func(t *testing.T) {
		// Exercises syncRemoteSSHDKey's retry loop: the first authorized_keys
		// exec fails with "pod not found" (a pod-churn token
		// sshdRemoteExecNeedsDeploymentRetry treats as retryable), the
		// command waits for the deployment (kubectl wait via the stub) and
		// the second exec succeeds. The stateful stub flips on a marker
		// file after the first exec call. The scenario pays one real
		// 5-second retry delay (sshdRemoteExecRetryDelay), which is why
		// there is exactly one failing attempt.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		publicKey := filepath.Join(setup.Home, "id_ed25519.pub")
		if err := os.WriteFile(publicKey, []byte("ssh-ed25519 AAAATEST user@example\n"), 0o644); err != nil {
			t.Fatalf("write public key: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		marker := filepath.Join(stubs, "exec-attempted")
		fixture.StubBinaryWithScript(t, stubs, "kubectl", strings.Join([]string{
			`case "$*" in`,
			`  *" exec "*)`,
			`    if [ -f '` + marker + `' ]; then`,
			`      exit 0`,
			`    fi`,
			`    touch '` + marker + `'`,
			`    printf '%s\n' 'error: pod not found' >&2`,
			`    exit 1 ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n"))
		fixture.StubBinary(t, stubs, "helm", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm")...)
		args := []string{"sshd", "init", "team", "dev", "--public-key", publicKey, "--local-port", "64022"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "sshd/init_real_run_retries_after_pod_not_found", normalize.Apply(result.Combined))
	})
}
