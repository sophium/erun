package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestDoctor(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"doctor", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_prune_images_traces_dind_exec", func(t *testing.T) {
		// Exercises doctor.go --prune-images action: --dry-run must trace
		// the kubectl wait + the dind exec command line that would prune
		// docker images, including the resolved kubernetes context and
		// namespace, without performing any side effect.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/dry_run_prune_images_traces_dind_exec", normalize.Apply(result.Combined))
	})

	t.Run("in_runtime_complete_marker_dry_run", func(t *testing.T) {
		// When the runtime env reports ERUN_REPO_REMOTE=true and the
		// bootstrap marker shows the previous init finished, doctor
		// should report 'complete' without offering to finish anything
		// and without falling through to the kubectl-driven dind path.
		setup := env.New(t)
		projectRoot := filepath.Join(setup.Home, "git", "team")
		if err := os.MkdirAll(filepath.Join(projectRoot, ".git"), 0o755); err != nil {
			t.Fatalf("mkdir .git: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(setup.Home, ".ssh"), 0o700); err != nil {
			t.Fatalf("mkdir .ssh: %v", err)
		}
		if err := os.WriteFile(filepath.Join(setup.Home, ".ssh", "id_ed25519"), []byte("stub\n"), 0o600); err != nil {
			t.Fatalf("write ssh key: %v", err)
		}
		markerDir := filepath.Join(setup.Home, ".erun")
		if err := os.MkdirAll(markerDir, 0o700); err != nil {
			t.Fatalf("mkdir marker dir: %v", err)
		}
		marker := "tenant: team\n" +
			"environment: dev\n" +
			"project_root: " + projectRoot + "\n" +
			"repository_url: git@example.com:team/repo.git\n" +
			"bootstrap_complete: true\n"
		if err := os.WriteFile(filepath.Join(markerDir, "bootstrap.yaml"), []byte(marker), 0o600); err != nil {
			t.Fatalf("write marker: %v", err)
		}
		envVars := append(setup.Env(),
			"ERUN_REPO_REMOTE=true",
			"ERUN_TENANT=team",
			"ERUN_ENVIRONMENT=dev",
			"ERUN_REPO_PATH="+projectRoot,
		)
		result := erun.Run(t, []string{"doctor", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/in_runtime_complete_marker_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("in_runtime_finish_remote_init_dry_run", func(t *testing.T) {
		// The bootstrap marker exists but bootstrap_complete=false and
		// the SSH key + .git checkout are missing. With
		// --finish-remote-init in dry-run mode doctor must trace the
		// ssh-keygen, git clone, and marker write it would perform.
		setup := env.New(t)
		projectRoot := filepath.Join(setup.Home, "git", "team")
		if err := os.MkdirAll(projectRoot, 0o755); err != nil {
			t.Fatalf("mkdir project root: %v", err)
		}
		markerDir := filepath.Join(setup.Home, ".erun")
		if err := os.MkdirAll(markerDir, 0o700); err != nil {
			t.Fatalf("mkdir marker dir: %v", err)
		}
		marker := "tenant: team\n" +
			"environment: dev\n" +
			"project_root: " + projectRoot + "\n" +
			"repository_url: git@example.com:team/repo.git\n" +
			"bootstrap_complete: false\n"
		if err := os.WriteFile(filepath.Join(markerDir, "bootstrap.yaml"), []byte(marker), 0o600); err != nil {
			t.Fatalf("write marker: %v", err)
		}
		envVars := append(setup.Env(),
			"ERUN_REPO_REMOTE=true",
			"ERUN_TENANT=team",
			"ERUN_ENVIRONMENT=dev",
			"ERUN_REPO_PATH="+projectRoot,
		)
		result := erun.Run(t, []string{"doctor", "--dry-run", "--finish-remote-init"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/in_runtime_finish_remote_init_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("in_runtime_finish_remote_init_codecommit_dry_run", func(t *testing.T) {
		// The marker records a CodeCommit URL and the IAM SSH public
		// key ID, but bootstrap_complete=false and the .git checkout
		// plus both SSH keypairs are missing. With --finish-remote-init
		// in dry-run mode doctor must trace ssh-keygen for the ed25519
		// key, ssh-keygen -t rsa -b 4096 for the codecommit key, the
		// ~/.ssh/config write, and an ls-remote + clone routed through
		// ssh -F "$HOME/.ssh/config" against the codecommit host.
		setup := env.New(t)
		projectRoot := filepath.Join(setup.Home, "git", "petios")
		if err := os.MkdirAll(projectRoot, 0o755); err != nil {
			t.Fatalf("mkdir project root: %v", err)
		}
		markerDir := filepath.Join(setup.Home, ".erun")
		if err := os.MkdirAll(markerDir, 0o700); err != nil {
			t.Fatalf("mkdir marker dir: %v", err)
		}
		marker := "tenant: petios\n" +
			"environment: dev\n" +
			"project_root: " + projectRoot + "\n" +
			"repository_url: ssh://git-codecommit.eu-west-1.amazonaws.com/v1/repos/petios\n" +
			"codecommit_host: git-codecommit.eu-west-1.amazonaws.com\n" +
			"codecommit_ssh_key_id: APKATESTCODECOMMITKEY\n" +
			"bootstrap_complete: false\n"
		if err := os.WriteFile(filepath.Join(markerDir, "bootstrap.yaml"), []byte(marker), 0o600); err != nil {
			t.Fatalf("write marker: %v", err)
		}
		envVars := append(setup.Env(),
			"ERUN_REPO_REMOTE=true",
			"ERUN_TENANT=petios",
			"ERUN_ENVIRONMENT=dev",
			"ERUN_REPO_PATH="+projectRoot,
		)
		result := erun.Run(t, []string{"doctor", "--dry-run", "--finish-remote-init"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/in_runtime_finish_remote_init_codecommit_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("in_runtime_no_marker_dry_run", func(t *testing.T) {
		// When ERUN_REPO_REMOTE=true but no marker exists on disk,
		// doctor falls back to the runtime-env vars to identify the
		// target and reports that the marker is missing without
		// attempting recovery.
		setup := env.New(t)
		projectRoot := filepath.Join(setup.Home, "git", "team")
		if err := os.MkdirAll(projectRoot, 0o755); err != nil {
			t.Fatalf("mkdir project root: %v", err)
		}
		envVars := append(setup.Env(),
			"ERUN_REPO_REMOTE=true",
			"ERUN_TENANT=team",
			"ERUN_ENVIRONMENT=dev",
			"ERUN_REPO_PATH="+projectRoot,
		)
		result := erun.Run(t, []string{"doctor", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/in_runtime_no_marker_dry_run", normalize.Apply(result.Combined))
	})
}
