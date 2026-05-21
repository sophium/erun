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
		markerDir := filepath.Join(setup.Home, ".erun", "team", "dev")
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
		markerDir := filepath.Join(setup.Home, ".erun", "team", "dev")
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
		markerDir := filepath.Join(setup.Home, ".erun", "petios", "dev")
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

	t.Run("in_runtime_legacy_marker_path_dry_run", func(t *testing.T) {
		// Existing in-pod markers from a previous version live at the
		// legacy single-marker path $HOME/.erun/bootstrap.yaml. On upgrade
		// doctor must still consume that marker so a previously-resumed
		// init is not stranded; the legacy fallback only kicks in when the
		// marker's tenant/environment match the runtime env.
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
		if err := os.MkdirAll(filepath.Join(setup.Home, ".erun"), 0o700); err != nil {
			t.Fatalf("mkdir legacy marker dir: %v", err)
		}
		marker := "tenant: team\n" +
			"environment: dev\n" +
			"project_root: " + projectRoot + "\n" +
			"repository_url: git@example.com:team/repo.git\n" +
			"bootstrap_complete: true\n"
		if err := os.WriteFile(filepath.Join(setup.Home, ".erun", "bootstrap.yaml"), []byte(marker), 0o600); err != nil {
			t.Fatalf("write legacy marker: %v", err)
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
		golden.Equal(t, "doctor/in_runtime_legacy_marker_path_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("in_runtime_multi_tenant_markers_dry_run", func(t *testing.T) {
		// Two tenants share one $HOME (developer machine or shared runtime
		// host). Each writes its own bootstrap marker under
		// $HOME/.erun/<tenant>/<env>/bootstrap.yaml. doctor for tenant
		// "team" must see the team marker and ignore the other tenant's
		// marker even though both live under the same .erun root.
		setup := env.New(t)
		teamRoot := filepath.Join(setup.Home, "git", "team")
		otherRoot := filepath.Join(setup.Home, "git", "other")
		for _, root := range []string{teamRoot, otherRoot} {
			if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
				t.Fatalf("mkdir .git: %v", err)
			}
		}
		if err := os.MkdirAll(filepath.Join(setup.Home, ".ssh"), 0o700); err != nil {
			t.Fatalf("mkdir .ssh: %v", err)
		}
		if err := os.WriteFile(filepath.Join(setup.Home, ".ssh", "id_ed25519"), []byte("stub\n"), 0o600); err != nil {
			t.Fatalf("write ssh key: %v", err)
		}
		teamMarkerDir := filepath.Join(setup.Home, ".erun", "team", "dev")
		if err := os.MkdirAll(teamMarkerDir, 0o700); err != nil {
			t.Fatalf("mkdir team marker dir: %v", err)
		}
		teamMarker := "tenant: team\n" +
			"environment: dev\n" +
			"project_root: " + teamRoot + "\n" +
			"repository_url: git@example.com:team/repo.git\n" +
			"bootstrap_complete: true\n"
		if err := os.WriteFile(filepath.Join(teamMarkerDir, "bootstrap.yaml"), []byte(teamMarker), 0o600); err != nil {
			t.Fatalf("write team marker: %v", err)
		}
		otherMarkerDir := filepath.Join(setup.Home, ".erun", "other", "dev")
		if err := os.MkdirAll(otherMarkerDir, 0o700); err != nil {
			t.Fatalf("mkdir other marker dir: %v", err)
		}
		otherMarker := "tenant: other\n" +
			"environment: dev\n" +
			"project_root: " + otherRoot + "\n" +
			"repository_url: git@example.com:other/repo.git\n" +
			"bootstrap_complete: false\n"
		if err := os.WriteFile(filepath.Join(otherMarkerDir, "bootstrap.yaml"), []byte(otherMarker), 0o600); err != nil {
			t.Fatalf("write other marker: %v", err)
		}
		envVars := append(setup.Env(),
			"ERUN_REPO_REMOTE=true",
			"ERUN_TENANT=team",
			"ERUN_ENVIRONMENT=dev",
			"ERUN_REPO_PATH="+teamRoot,
		)
		result := erun.Run(t, []string{"doctor", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/in_runtime_multi_tenant_markers_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("inspect_clean_root_config", func(t *testing.T) {
		// On a clean install (tenant with no cloud aliases, no
		// cloud contexts), the host-side root-config inspection
		// must report ok status, zero orphans, and proceed
		// silently for callers that did not ask for repair work.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"doctor", "--repair-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/inspect_clean_root_config", normalize.Apply(result.Combined))
	})

	t.Run("inspect_orphaned_alias", func(t *testing.T) {
		// Seed a tenant that references a cloud-provider alias the
		// root config does not list. Doctor must surface the
		// orphan with its decoded username/account/provider and
		// the tenant back-reference, then suggest the repair flow
		// without prompting (dry-run is non-interactive).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		// Overwrite the tenant config to reference an orphan alias.
		tenantPath := filepath.Join(setup.ConfigHome, "erun", "team", "config.yaml")
		body := "projectroot: " + setup.Cwd + "\n" +
			"name: team\n" +
			"defaultenvironment: dev\n" +
			"cloudprovideraliases:\n" +
			"    - alice+1234567890@aws\n" +
			"primarycloudprovideralias: alice+1234567890@aws\n"
		if err := os.WriteFile(tenantPath, []byte(body), 0o644); err != nil {
			t.Fatalf("write tenant: %v", err)
		}
		result := erun.Run(t, []string{"doctor", "--repair-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/inspect_orphaned_alias", normalize.Apply(result.Combined))
	})

	t.Run("inspect_corrupted_root_config", func(t *testing.T) {
		// Truncated root config: doctor must report corrupted
		// status without crashing and (when --repair-config is set
		// but no backup exists) tell the user to resolve the file
		// manually rather than silently overwriting.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		rootPath := filepath.Join(setup.ConfigHome, "erun", "config.yaml")
		if err := os.WriteFile(rootPath, []byte(""), 0o644); err != nil {
			t.Fatalf("truncate root config: %v", err)
		}
		result := erun.Run(t, []string{"doctor", "--repair-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		// Exit can be non-zero when corruption is fatal; the
		// golden is what we lock, not the code.
		_ = result
		golden.Equal(t, "doctor/inspect_corrupted_root_config", normalize.Apply(result.Combined))
	})

	t.Run("restore_config_from_backup_dry_run", func(t *testing.T) {
		// Seed a 0-byte root config plus a healthy backup for
		// 2026-05-19. Doctor with --restore-config-from-backup
		// 2026-05-19 --dry-run must trace the planned restore and
		// stop without actually replacing the file.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		rootPath := filepath.Join(setup.ConfigHome, "erun", "config.yaml")
		if err := os.WriteFile(rootPath, []byte(""), 0o644); err != nil {
			t.Fatalf("truncate root config: %v", err)
		}
		backupPath := filepath.Join(setup.ConfigHome, "erun", "config.yaml.2026-05-19.bak")
		body := "defaulttenant: team\n" +
			"cloudproviders:\n" +
			"    - alias: alice+1234567890@aws\n" +
			"      provider: aws\n" +
			"      username: alice\n" +
			"      accountid: \"1234567890\"\n"
		if err := os.WriteFile(backupPath, []byte(body), 0o644); err != nil {
			t.Fatalf("write backup: %v", err)
		}
		result := erun.Run(t, []string{"doctor", "--restore-config-from-backup", "2026-05-19", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/restore_config_from_backup_dry_run", normalize.Apply(result.Combined))
	})
}
