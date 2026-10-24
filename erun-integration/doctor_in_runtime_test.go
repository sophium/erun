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

func doctorInRuntimeScenarios(t *testing.T) {
	t.Run("repair_workspace_sync_dry_run", testDoctorRepairWorkspaceSyncDryRun)
	t.Run("in_runtime_complete_marker_dry_run", testDoctorInRuntimeCompleteMarkerDryRun)
	t.Run("in_runtime_finish_remote_init_dry_run", testDoctorInRuntimeFinishRemoteInitDryRun)
	t.Run("in_runtime_skills_installed_dry_run", testDoctorInRuntimeSkillsInstalledDryRun)
	t.Run("in_runtime_finish_skills_dry_run", testDoctorInRuntimeFinishSkillsDryRun)
	t.Run("in_runtime_finish_remote_init_codecommit_dry_run", testDoctorInRuntimeFinishRemoteInitCodecommitDryRun)
	t.Run("in_runtime_no_marker_dry_run", testDoctorInRuntimeNoMarkerDryRun)
	t.Run("in_runtime_multi_tenant_markers_dry_run", testDoctorInRuntimeMultiTenantMarkersDryRun)
	t.Run("in_runtime_no_git_marker_dry_run", testDoctorInRuntimeNoGitMarkerDryRun)
	t.Run("in_runtime_incomplete_marker_all_artifacts_present_dry_run", testDoctorInRuntimeIncompleteMarkerAllArtifactsPresentDryRun)
	t.Run("in_runtime_finish_without_identity_error", testDoctorInRuntimeFinishWithoutIdentityError)
}

func testDoctorRepairWorkspaceSyncDryRun(t *testing.T) {
	// Exercises doctor.go --repair-workspace-sync for a remote-agent env
	// with workspace sync enabled: --dry-run must trace the non-destructive
	// provisioning it would run — write the local ssh config alias, install
	// the pod authorized_keys through the runtime container (never a helm
	// redeploy), ensure the SSH port-forward, and re-check reachability —
	// then short-circuit before the deploy diagnosis and prune prompts. The
	// high localportrangestart pins the SSH port so the port-forward preview
	// never collides with a developer's live erun session.
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithWorkspaceSync(t, setup, "team", "dev", 26100)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--repair-workspace-sync", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/repair_workspace_sync_dry_run", normalize.Apply(result.Combined))
}

func testDoctorInRuntimeCompleteMarkerDryRun(t *testing.T) {
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
}

func testDoctorInRuntimeFinishRemoteInitDryRun(t *testing.T) {
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
}

func testDoctorInRuntimeSkillsInstalledDryRun(t *testing.T) {
	// ERUN_SKILLS_DIR points doctor at the runtime image's baked-skills
	// seam (/etc/erun/skills). When every baked skill is installed under
	// ~/.claude/skills the workability check reports OK with nothing to
	// finish.
	setup := env.New(t)
	projectRoot := filepath.Join(setup.Home, "git", "team")
	bakedSkills := filepath.Join(setup.Home, "baked-skills")
	for _, dir := range []string{
		filepath.Join(projectRoot, ".git"),
		filepath.Join(setup.Home, ".ssh"),
		filepath.Join(bakedSkills, "erun-sample"),
		filepath.Join(setup.Home, ".claude", "skills", "erun-sample"),
		filepath.Join(setup.Home, ".erun", "team", "dev"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	mustWriteFile(t, filepath.Join(setup.Home, ".ssh", "id_ed25519"), "stub\n")
	mustWriteFile(t, filepath.Join(bakedSkills, "erun-sample", "SKILL.md"), "sample\n")
	mustWriteFile(t, filepath.Join(setup.Home, ".claude", "skills", "erun-sample", "SKILL.md"), "sample\n")
	mustWriteFile(t, filepath.Join(setup.Home, ".erun", "team", "dev", "bootstrap.yaml"),
		"tenant: team\nenvironment: dev\nproject_root: "+projectRoot+"\nrepository_url: git@example.com:team/repo.git\nbootstrap_complete: true\n")
	envVars := append(setup.Env(),
		"ERUN_REPO_REMOTE=true",
		"ERUN_TENANT=team",
		"ERUN_ENVIRONMENT=dev",
		"ERUN_REPO_PATH="+projectRoot,
		"ERUN_SKILLS_DIR="+bakedSkills,
	)
	result := erun.Run(t, []string{"doctor", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/in_runtime_skills_installed_dry_run", normalize.Apply(result.Combined))
}

func testDoctorInRuntimeFinishSkillsDryRun(t *testing.T) {
	// Baked skills exist but none are installed under ~/.claude/skills, so
	// the workability check is MISSING. Project root, git, and ssh are in
	// place so skills are the only missing artifact; with
	// --finish-remote-init in dry-run doctor must trace the cp into both
	// ~/.claude/skills and ~/.codex/skills.
	setup := env.New(t)
	projectRoot := filepath.Join(setup.Home, "git", "team")
	bakedSkills := filepath.Join(setup.Home, "baked-skills")
	for _, dir := range []string{
		filepath.Join(projectRoot, ".git"),
		filepath.Join(setup.Home, ".ssh"),
		filepath.Join(bakedSkills, "erun-sample"),
		filepath.Join(setup.Home, ".erun", "team", "dev"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	mustWriteFile(t, filepath.Join(setup.Home, ".ssh", "id_ed25519"), "stub\n")
	mustWriteFile(t, filepath.Join(bakedSkills, "erun-sample", "SKILL.md"), "sample\n")
	mustWriteFile(t, filepath.Join(setup.Home, ".erun", "team", "dev", "bootstrap.yaml"),
		"tenant: team\nenvironment: dev\nproject_root: "+projectRoot+"\nrepository_url: git@example.com:team/repo.git\nbootstrap_complete: true\n")
	envVars := append(setup.Env(),
		"ERUN_REPO_REMOTE=true",
		"ERUN_TENANT=team",
		"ERUN_ENVIRONMENT=dev",
		"ERUN_REPO_PATH="+projectRoot,
		"ERUN_SKILLS_DIR="+bakedSkills,
	)
	result := erun.Run(t, []string{"doctor", "--dry-run", "--finish-remote-init"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/in_runtime_finish_skills_dry_run", normalize.Apply(result.Combined))
}

func testDoctorInRuntimeFinishRemoteInitCodecommitDryRun(t *testing.T) {
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
}

func testDoctorInRuntimeNoMarkerDryRun(t *testing.T) {
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
}

func testDoctorInRuntimeMultiTenantMarkersDryRun(t *testing.T) {
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
}

func testDoctorInRuntimeNoGitMarkerDryRun(t *testing.T) {
	// `erun init --remote --no-git` records no_git: true in the
	// marker. doctor must report the git checkout and SSH keypair
	// rows as N/A (not MISSING) and conclude the init is complete,
	// covering the NotApplicable arms of inspectGitCheckout /
	// inspectSSHKey that the other in-runtime scenarios never hit.
	setup := env.New(t)
	projectRoot := filepath.Join(setup.Home, "git", "team")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	seedRemoteInitMarker(t, setup, "team", "dev",
		"tenant: team\n"+
			"environment: dev\n"+
			"project_root: "+projectRoot+"\n"+
			"no_git: true\n"+
			"bootstrap_complete: true\n")
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
	golden.Equal(t, "doctor/in_runtime_no_git_marker_dry_run", normalize.Apply(result.Combined))
}

func testDoctorInRuntimeIncompleteMarkerAllArtifactsPresentDryRun(t *testing.T) {
	// The marker says bootstrap_complete=false but every artifact is
	// already on disk (init was interrupted after the work finished
	// but before the marker rewrite). doctor must not offer recovery
	// — there is nothing to redo — and instead point the user at
	// re-running `erun init --remote` to refresh the marker. Covers
	// the MissingItems()==0 arm of writeRemoteInitShortCircuit.
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
	seedRemoteInitMarker(t, setup, "team", "dev",
		"tenant: team\n"+
			"environment: dev\n"+
			"project_root: "+projectRoot+"\n"+
			"repository_url: git@example.com:team/repo.git\n"+
			"bootstrap_complete: false\n")
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
	golden.Equal(t, "doctor/in_runtime_incomplete_marker_all_artifacts_present_dry_run", normalize.Apply(result.Combined))
}

func testDoctorInRuntimeFinishWithoutIdentityError(t *testing.T) {
	// ERUN_REPO_REMOTE=true but neither a marker nor
	// ERUN_TENANT/ERUN_ENVIRONMENT identify the target. The report
	// must render <unknown>/<unknown> and --finish-remote-init must
	// fail with the explicit missing-identity error from
	// RunRemoteInitFinish instead of recovering into a wrong path.
	setup := env.New(t)
	envVars := append(setup.Env(), "ERUN_REPO_REMOTE=true")
	result := erun.Run(t, []string{"doctor", "--finish-remote-init"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0: %s", result.Combined)
	}
	golden.Equal(t, "doctor/in_runtime_finish_without_identity_error", normalize.Apply(result.Combined))
}
