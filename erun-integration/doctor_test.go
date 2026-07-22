package integration

import (
	"crypto/sha1"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

	t.Run("dry_run_rollback_traces_helm_rollback", func(t *testing.T) {
		// Exercises the deploy-recovery action: --rollback --dry-run must
		// trace the `helm rollback <release> --namespace --kube-context
		// --wait --timeout` command for the runtime release, without
		// mutating the cluster.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run", "--rollback"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/dry_run_rollback_traces_helm_rollback", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_clear_pending_helm_traces_kubectl_delete", func(t *testing.T) {
		// Exercises the deploy-recovery action: --clear-pending-helm
		// --dry-run must trace the `kubectl delete secrets,configmaps -l
		// owner=helm,...,status in (pending-install,...)` command that
		// clears the stuck helm pending-operation lock.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run", "--clear-pending-helm"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/dry_run_clear_pending_helm_traces_kubectl_delete", normalize.Apply(result.Combined))
	})

	t.Run("conflicting_recovery_flags_error", func(t *testing.T) {
		// Clearing a pending lock and rolling back are alternative
		// recoveries; passing both must fail fast with a clear error
		// rather than running both (which would step the release back a
		// revision too far).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"doctor", "team", "dev", "--dry-run", "--clear-pending-helm", "--rollback"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0: %s", result.Combined)
		}
		golden.Equal(t, "doctor/conflicting_recovery_flags_error", normalize.Apply(result.Combined))
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

	t.Run("in_runtime_skills_installed_dry_run", func(t *testing.T) {
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
	})

	t.Run("in_runtime_finish_skills_dry_run", func(t *testing.T) {
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

	t.Run("inspect_orphaned_cloud_context", func(t *testing.T) {
		// Reproduce the screenshot scenario: an env config names a
		// cloud-managed kubernetes context that the root config no
		// longer lists, and the env still carries the cloud provider
		// alias. Doctor must surface the orphan with decoded account
		// + region and the back-reference to the env.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "petios", "rihards-review")
		tenantPath := filepath.Join(setup.ConfigHome, "erun", "petios", "config.yaml")
		alias := "alice+020362606330@aws"
		if err := os.WriteFile(tenantPath, []byte("projectroot: "+setup.Cwd+"\n"+
			"name: petios\n"+
			"defaultenvironment: rihards-review\n"+
			"cloudprovideraliases:\n"+
			"    - "+alias+"\n"+
			"primarycloudprovideralias: "+alias+"\n"), 0o644); err != nil {
			t.Fatalf("tenant: %v", err)
		}
		envPath := filepath.Join(setup.ConfigHome, "erun", "petios", "rihards-review", "config.yaml")
		if err := os.WriteFile(envPath, []byte("name: rihards-review\n"+
			"repopath: "+setup.Cwd+"\n"+
			"kubernetescontext: erun-001-020362606330-eu-west-2\n"+
			"cloudprovideralias: "+alias+"\n"+
			"managedcloud: true\n"), 0o644); err != nil {
			t.Fatalf("env: %v", err)
		}
		// Seed the root config with the provider so the alias side
		// of the inspection stays clean — we only want the context
		// orphan to surface here.
		rootPath := filepath.Join(setup.ConfigHome, "erun", "config.yaml")
		if err := os.WriteFile(rootPath, []byte("defaulttenant: petios\n"+
			"cloudproviders:\n"+
			"    - alias: "+alias+"\n"+
			"      provider: aws\n"+
			"      username: alice\n"+
			"      accountid: \"020362606330\"\n"), 0o644); err != nil {
			t.Fatalf("root: %v", err)
		}
		result := erun.Run(t, []string{"doctor", "--repair-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/inspect_orphaned_cloud_context", normalize.Apply(result.Combined))
	})

	t.Run("repair_orphaned_alias_real_run_via_aws_config_profile", func(t *testing.T) {
		// Real-run --repair-config with a confirmed prompt: covers
		// buildOrphanRepairParams → common.LookupAWSSSOProfileByAccountID
		// (parseAWSSharedConfig, findAWSSSOProfileForAccount, and the
		// sso_session indirection through buildAWSSSOSessionIndex) plus
		// the follow-up InitAWSCloudProvider re-init through the
		// argv-branching aws stub. Dry-run cannot reach the lookup:
		// offerOrphanedAliasRepair short-circuits to runOrphanRepairDryRun
		// before buildOrphanRepairParams runs, so the ~/.aws/config scan
		// only happens in a confirmed real run.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		// The tenant references an alias the root config does not list →
		// orphan. The alias matches the identity the aws stub returns
		// (user test-user, account 123456789012) so the re-init heals
		// exactly this alias.
		alias := "test-user+123456789012@aws"
		tenantPath := filepath.Join(setup.ConfigHome, "erun", "team", "config.yaml")
		tenantBody := "projectroot: " + setup.Cwd + "\n" +
			"name: team\n" +
			"defaultenvironment: dev\n" +
			"cloudprovideraliases:\n" +
			"    - " + alias + "\n" +
			"primarycloudprovideralias: " + alias + "\n"
		if err := os.WriteFile(tenantPath, []byte(tenantBody), 0o644); err != nil {
			t.Fatalf("write tenant: %v", err)
		}
		startURL := fixture.SeedAWSSharedConfig(t, setup, "123456789012", "corp-dev")
		envVars, issuer := stubAWSCallerIdentityAndJWT(t, setup)
		result := erun.Run(t, []string{"doctor", "--repair-config"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   envVars,
			Stdin: "y\n",
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/repair_orphaned_alias_real_run_via_aws_config_profile", normalize.Apply(result.Combined))
		// Persistence is a side effect outside the captured streams: the
		// re-initialized provider must carry the SSO settings discovered
		// from ~/.aws/config plus the issuer extracted from the stubbed
		// web-identity token.
		raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
		if err != nil {
			t.Fatalf("read root config: %v", err)
		}
		for _, want := range []string{
			"alias: " + alias,
			"ssostarturl: " + startURL,
			"oidcissuerurl: " + issuer,
		} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("expected persisted config to contain %q, got:\n%s", want, raw)
			}
		}
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

	t.Run("in_runtime_no_git_marker_dry_run", func(t *testing.T) {
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
	})

	t.Run("in_runtime_incomplete_marker_all_artifacts_present_dry_run", func(t *testing.T) {
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
	})

	t.Run("in_runtime_finish_without_identity_error", func(t *testing.T) {
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
	})

	t.Run("finish_remote_init_real_run_prompts_url_and_retries_clone", func(t *testing.T) {
		// Real-run --finish-remote-init for a plain SSH host: covers the
		// non-dry-run arms of RunRemoteInitFinish that the dry-run trace
		// cannot reach because their decisions depend on side effects —
		// finishRemoteInitProjectRoot/SSHKey actually creating files,
		// ensureRemoteInitSSHKeyPermissions re-chmodding the generated
		// keypair, finishRemoteInitGitAccess printing the public key and
		// polling ls-remote (WaitForGitAccess retry + confirmation), the
		// clone, and SaveRemoteInitMarker. The marker has no
		// repository_url so the single interactive prompt of this
		// subprocess supplies it (one prompt max — readline read-ahead
		// eats remaining stdin once the first prompt closes). ssh-keygen
		// is stubbed to write a deterministic keypair; git is stubbed to
		// fail the first ls-remote (driving one retry cycle) and succeed
		// afterwards.
		setup := env.New(t)
		projectRoot := filepath.Join(setup.Home, "git", "team")
		seedRemoteInitMarker(t, setup, "team", "dev",
			"tenant: team\n"+
				"environment: dev\n"+
				"project_root: "+projectRoot+"\n"+
				"bootstrap_complete: false\n")
		stubs := filepath.Join(setup.Cwd, "stubs")
		stubSSHKeygenWritingKeypair(t, stubs)
		stubGitForRemoteInit(t, stubs, true)
		envVars := append(setup.Env(),
			"ERUN_REPO_REMOTE=true",
			"ERUN_TENANT=team",
			"ERUN_ENVIRONMENT=dev",
			"ERUN_REPO_PATH="+projectRoot,
		)
		envVars = append(envVars, fixture.StubEnv(stubs, "ssh-keygen", "git")...)
		result := erun.Run(t, []string{"doctor", "--finish-remote-init"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   envVars,
			Stdin: "git@example.com:team/repo.git\n",
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/finish_remote_init_real_run_prompts_url_and_retries_clone", normalize.Apply(result.Combined))
		// Side effects outside the captured streams: the marker must be
		// rewritten with the prompted URL and bootstrap_complete=true,
		// and the generated keypair must carry the ssh-required modes.
		marker := readFileForTest(t, filepath.Join(setup.Home, ".erun", "team", "dev", "bootstrap.yaml"))
		for _, want := range []string{
			"repository_url: git@example.com:team/repo.git",
			"bootstrap_complete: true",
		} {
			if !strings.Contains(marker, want) {
				t.Errorf("expected marker to contain %q, got:\n%s", want, marker)
			}
		}
		assertFileMode(t, filepath.Join(setup.Home, ".ssh", "id_ed25519"), 0o600)
		assertFileMode(t, filepath.Join(setup.Home, ".ssh", "id_ed25519.pub"), 0o644)
	})

	t.Run("finish_remote_init_codecommit_real_run_prompts_key_id", func(t *testing.T) {
		// Real-run CodeCommit recovery: the marker recorded no CodeCommit
		// metadata, so the host is detected from --remote-repository-url
		// and RunRemoteInitFinish dynamically appends the missing RSA
		// keypair item. Covers finishRemoteInitCodeCommitSSHKey,
		// resolveCodeCommitSSHKeyID's prompt branch (the public key +
		// IAM setup block printed before asking for the key ID — the
		// single prompt of this subprocess; init's equivalent prompt is
		// unreachable through a pipe because it is always the second
		// prompt there), finishRemoteInitCodeCommitSSHConfig writing
		// ~/.ssh/config, and the CodeCommit arm of
		// finishRemoteInitGitAccess. None of these run in dry-run mode:
		// they read the generated key files and mutate ~/.ssh.
		setup := env.New(t)
		projectRoot := filepath.Join(setup.Home, "git", "petios")
		if err := os.MkdirAll(projectRoot, 0o755); err != nil {
			t.Fatalf("mkdir project root: %v", err)
		}
		sshDir := filepath.Join(setup.Home, ".ssh")
		if err := os.MkdirAll(sshDir, 0o700); err != nil {
			t.Fatalf("mkdir .ssh: %v", err)
		}
		if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519"), []byte("seeded\n"), 0o600); err != nil {
			t.Fatalf("write ssh key: %v", err)
		}
		if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519.pub"), []byte("ssh-ed25519 AAAASEEDPUB user@example\n"), 0o644); err != nil {
			t.Fatalf("write ssh pub key: %v", err)
		}
		seedRemoteInitMarker(t, setup, "petios", "dev",
			"tenant: petios\n"+
				"environment: dev\n"+
				"project_root: "+projectRoot+"\n"+
				"bootstrap_complete: false\n")
		stubs := filepath.Join(setup.Cwd, "stubs")
		stubSSHKeygenWritingKeypair(t, stubs)
		stubGitForRemoteInit(t, stubs, false)
		envVars := append(setup.Env(),
			"ERUN_REPO_REMOTE=true",
			"ERUN_TENANT=petios",
			"ERUN_ENVIRONMENT=dev",
			"ERUN_REPO_PATH="+projectRoot,
		)
		envVars = append(envVars, fixture.StubEnv(stubs, "ssh-keygen", "git")...)
		result := erun.Run(t, []string{
			"doctor", "--finish-remote-init",
			"--remote-repository-url", "ssh://git-codecommit.eu-west-1.amazonaws.com/v1/repos/petios",
		}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   envVars,
			Stdin: "APKAEXAMPLEKEYID\n",
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/finish_remote_init_codecommit_real_run_prompts_key_id", normalize.Apply(result.Combined))
		// The marker must capture the CodeCommit coordinates so the next
		// doctor run inspects the RSA keypair, and ~/.ssh/config must
		// carry the IAM host stanza git's ssh relies on.
		marker := readFileForTest(t, filepath.Join(setup.Home, ".erun", "petios", "dev", "bootstrap.yaml"))
		for _, want := range []string{
			"repository_url: ssh://git-codecommit.eu-west-1.amazonaws.com/v1/repos/petios",
			"codecommit_host: git-codecommit.eu-west-1.amazonaws.com",
			"codecommit_ssh_key_id: APKAEXAMPLEKEYID",
			"bootstrap_complete: true",
		} {
			if !strings.Contains(marker, want) {
				t.Errorf("expected marker to contain %q, got:\n%s", want, marker)
			}
		}
		sshConfig := readFileForTest(t, filepath.Join(sshDir, "config"))
		for _, want := range []string{
			"Host git-codecommit.eu-west-1.amazonaws.com",
			"User APKAEXAMPLEKEYID",
			"IdentityFile ~/.ssh/id_rsa_codecommit",
		} {
			if !strings.Contains(sshConfig, want) {
				t.Errorf("expected ~/.ssh/config to contain %q, got:\n%s", want, sshConfig)
			}
		}
		assertFileMode(t, filepath.Join(sshDir, "id_rsa_codecommit"), 0o600)
	})

	t.Run("inspect_missing_root_config", func(t *testing.T) {
		// Root config file deleted outright (vs the truncated/corrupted
		// variant above): InspectRootConfig must take the
		// ErrNotInitialized arm and report status=missing, and the
		// repair flow — with no backups to restore — must point at
		// manual resolution.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		if err := os.Remove(filepath.Join(setup.ConfigHome, "erun", "config.yaml")); err != nil {
			t.Fatalf("remove root config: %v", err)
		}
		result := erun.Run(t, []string{"doctor", "--repair-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/inspect_missing_root_config", normalize.Apply(result.Combined))
	})

	t.Run("inspect_malformed_and_unsupported_orphan_aliases", func(t *testing.T) {
		// Two orphan aliases that the repair flow must refuse to
		// auto-fix: one that does not parse as <user>+<account>@<provider>
		// (report renders the "alias is malformed" arm, repair skips with
		// the recreate-manually hint) and one that parses but names a
		// provider other than aws (repair skips with the unsupported-
		// provider reason). Both skip messages print before the dry-run
		// gate, so this locks orphanRepairBlockedReason end to end.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		tenantPath := filepath.Join(setup.ConfigHome, "erun", "team", "config.yaml")
		body := "projectroot: " + setup.Cwd + "\n" +
			"name: team\n" +
			"defaultenvironment: dev\n" +
			"cloudprovideraliases:\n" +
			"    - \"###bad-alias\"\n" +
			"    - alice+999000111@gcp\n" +
			"primarycloudprovideralias: alice+999000111@gcp\n"
		if err := os.WriteFile(tenantPath, []byte(body), 0o644); err != nil {
			t.Fatalf("write tenant: %v", err)
		}
		result := erun.Run(t, []string{"doctor", "--repair-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/inspect_malformed_and_unsupported_orphan_aliases", normalize.Apply(result.Combined))
	})

	t.Run("inspect_orphan_alias_referenced_by_cloud_contexts", func(t *testing.T) {
		// The orphan alias is referenced by cloud contexts in the root
		// config rather than by a tenant: covers the addOrphanContext
		// aggregation arm of InspectRootConfig, the cloud-contexts line
		// of writeOrphanedAliasReport (formatOrphanedContexts with and
		// without a region), and preferredRegionForOrphan skipping the
		// region-less ref to seed the dry-run re-init from the second.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		rootPath := filepath.Join(setup.ConfigHome, "erun", "config.yaml")
		body := "defaulttenant: team\n" +
			"cloudcontexts:\n" +
			"    - name: ctx-unnamed-region\n" +
			"      provider: aws\n" +
			"      cloudprovideralias: bob+111122223333@aws\n" +
			"      kubernetescontext: ctx-unnamed-region\n" +
			"    - name: erun-001-111122223333-eu-west-2\n" +
			"      provider: aws\n" +
			"      cloudprovideralias: bob+111122223333@aws\n" +
			"      region: eu-west-2\n" +
			"      kubernetescontext: erun-001-111122223333-eu-west-2\n"
		if err := os.WriteFile(rootPath, []byte(body), 0o644); err != nil {
			t.Fatalf("write root config: %v", err)
		}
		result := erun.Run(t, []string{"doctor", "--repair-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/inspect_orphan_alias_referenced_by_cloud_contexts", normalize.Apply(result.Combined))
	})

	t.Run("repair_orphaned_alias_real_run_declined", func(t *testing.T) {
		// Real-run --repair-config where the user answers "n" to the
		// re-init confirm: the repair must stop without touching the
		// root config (covers the declined arm of
		// offerOrphanedAliasRepair, which dry-run short-circuits past).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		alias := "test-user+123456789012@aws"
		tenantPath := filepath.Join(setup.ConfigHome, "erun", "team", "config.yaml")
		body := "projectroot: " + setup.Cwd + "\n" +
			"name: team\n" +
			"defaultenvironment: dev\n" +
			"cloudprovideraliases:\n" +
			"    - " + alias + "\n" +
			"primarycloudprovideralias: " + alias + "\n"
		if err := os.WriteFile(tenantPath, []byte(body), 0o644); err != nil {
			t.Fatalf("write tenant: %v", err)
		}
		result := erun.Run(t, []string{"doctor", "--repair-config"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "n\n",
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/repair_orphaned_alias_real_run_declined", normalize.Apply(result.Combined))
		raw := readFileForTest(t, filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
		if strings.Contains(raw, "cloudproviders") {
			t.Errorf("expected declined repair to leave root config without providers, got:\n%s", raw)
		}
	})

	t.Run("restore_config_from_backup_real_run", func(t *testing.T) {
		// Non-interactive restore by date without --dry-run: covers
		// runRootConfigRestore's real arm (RestoreRootConfigFromBackup
		// validating + atomically replacing the live file) and the
		// re-inspection + refreshed report runRootConfigRestoreSelector
		// prints once the restore lands. The roundtrip is asserted on
		// disk: the live config must equal the backup bytes.
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
		result := erun.Run(t, []string{"doctor", "--restore-config-from-backup", "2026-05-19"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/restore_config_from_backup_real_run", normalize.Apply(result.Combined))
		if restored := readFileForTest(t, rootPath); restored != body {
			t.Errorf("expected restored config to equal backup body, got:\n%s", restored)
		}
	})

	t.Run("restore_config_from_backup_invalid_date_error", func(t *testing.T) {
		// A selector that is neither a path nor a YYYY-MM-DD date must
		// fail fast with the explicit format error instead of silently
		// matching nothing.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"doctor", "--restore-config-from-backup", "not-a-date"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0: %s", result.Combined)
		}
		golden.Equal(t, "doctor/restore_config_from_backup_invalid_date_error", normalize.Apply(result.Combined))
	})

	t.Run("restore_config_from_backup_no_match_error", func(t *testing.T) {
		// A well-formed date with no backup on disk must name the
		// unmatched selector so the user can pick from the listed dates.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"doctor", "--restore-config-from-backup", "2026-01-01"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0: %s", result.Combined)
		}
		golden.Equal(t, "doctor/restore_config_from_backup_no_match_error", normalize.Apply(result.Combined))
	})

	t.Run("restore_env_config_from_backup_dry_run", func(t *testing.T) {
		// Per-env restore: a dated backup sits next to the
		// env's config.yaml. Doctor team dev --restore-env-config-from-backup
		// 2026-05-19 --dry-run must trace the planned cp and stop without
		// touching the live file.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envBackup := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml.2026-05-19.bak")
		backupBody := "name: dev\ntype: remote-agent\nkubernetescontext: team-dev\n"
		if err := os.WriteFile(envBackup, []byte(backupBody), 0o644); err != nil {
			t.Fatalf("write env backup: %v", err)
		}
		result := erun.Run(t, []string{"doctor", "team", "dev", "--restore-env-config-from-backup", "2026-05-19", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/restore_env_config_from_backup_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("restore_env_config_from_backup_real_run", func(t *testing.T) {
		// Real-run per-env restore: covers RestoreEnvConfigFromBackup
		// validating the bytes deserialize into an EnvConfig and atomically
		// replacing the live file. The roundtrip is asserted on disk.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		envBackup := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml.2026-05-19.bak")
		backupBody := "name: dev\ntype: remote-agent\nkubernetescontext: team-dev\n"
		if err := os.WriteFile(envBackup, []byte(backupBody), 0o644); err != nil {
			t.Fatalf("write env backup: %v", err)
		}
		result := erun.Run(t, []string{"doctor", "team", "dev", "--restore-env-config-from-backup", "2026-05-19"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/restore_env_config_from_backup_real_run", normalize.Apply(result.Combined))
		if restored := readFileForTest(t, envPath); restored != backupBody {
			t.Errorf("expected restored env config to equal backup body, got:\n%s", restored)
		}
	})

	t.Run("restore_env_config_from_backup_no_match_error", func(t *testing.T) {
		// A well-formed date with no matching env backup must name the
		// unmatched selector and the target env, and list the dates that
		// ARE available (recognition over recall) — here a 2026-05-19 backup
		// exists but 2026-01-01 was requested.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envBackup := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml.2026-05-19.bak")
		if err := os.WriteFile(envBackup, []byte("name: dev\ntype: remote-agent\n"), 0o644); err != nil {
			t.Fatalf("write env backup: %v", err)
		}
		result := erun.Run(t, []string{"doctor", "team", "dev", "--restore-env-config-from-backup", "2026-01-01"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0: %s", result.Combined)
		}
		golden.Equal(t, "doctor/restore_env_config_from_backup_no_match_error", normalize.Apply(result.Combined))
	})

	t.Run("restore_env_config_from_backup_requires_target_error", func(t *testing.T) {
		// Without an explicit tenant and environment there is no env to
		// restore; the flag must fail fast rather than guess a default.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"doctor", "--restore-env-config-from-backup", "2026-05-19"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0: %s", result.Combined)
		}
		golden.Equal(t, "doctor/restore_env_config_from_backup_requires_target_error", normalize.Apply(result.Combined))
	})

	t.Run("repair_config_restores_backup_via_prompt", func(t *testing.T) {
		// Real-run --repair-config on a corrupted root config with a
		// backup available: the repair flow's first move is the restore
		// confirm (offerRootConfigBackupRestore — the one interactive
		// prompt of this subprocess), then the restore, re-inspection,
		// and the restored-and-complete early return. Dry-run cannot
		// reach this: shouldOfferRootConfigRepair prints the suggestion
		// and exits before any prompt.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		rootPath := filepath.Join(setup.ConfigHome, "erun", "config.yaml")
		if err := os.WriteFile(rootPath, []byte(""), 0o644); err != nil {
			t.Fatalf("truncate root config: %v", err)
		}
		backupPath := filepath.Join(setup.ConfigHome, "erun", "config.yaml.2026-05-19.bak")
		body := "defaulttenant: team\n"
		if err := os.WriteFile(backupPath, []byte(body), 0o644); err != nil {
			t.Fatalf("write backup: %v", err)
		}
		result := erun.Run(t, []string{"doctor", "--repair-config"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "y\n",
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/repair_config_restores_backup_via_prompt", normalize.Apply(result.Combined))
		if restored := readFileForTest(t, rootPath); restored != body {
			t.Errorf("expected restored config to equal backup body, got:\n%s", restored)
		}
	})

	t.Run("repair_orphaned_alias_rotates_daily_backups", func(t *testing.T) {
		// Same confirmed alias re-init as the scenario above, but with
		// six dated backups already on disk: the SaveERunConfig inside
		// the repair must snapshot today's pre-save config
		// (writeRootConfigBackupIfDue) and evict the two oldest files to
		// hold the count-based retention at five
		// (pruneOldRootConfigBackups). The rotation only happens on a
		// real save, which dry-run never performs. The inspection report
		// also locks the backups-listed-while-status-ok rendering arm.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		alias := "test-user+123456789012@aws"
		tenantPath := filepath.Join(setup.ConfigHome, "erun", "team", "config.yaml")
		tenantBody := "projectroot: " + setup.Cwd + "\n" +
			"name: team\n" +
			"defaultenvironment: dev\n" +
			"cloudprovideraliases:\n" +
			"    - " + alias + "\n" +
			"primarycloudprovideralias: " + alias + "\n"
		if err := os.WriteFile(tenantPath, []byte(tenantBody), 0o644); err != nil {
			t.Fatalf("write tenant: %v", err)
		}
		seededDates := []string{"2026-01-01", "2026-01-02", "2026-01-03", "2026-01-04", "2026-01-05", "2026-01-06"}
		for _, date := range seededDates {
			backupPath := filepath.Join(setup.ConfigHome, "erun", "config.yaml."+date+".bak")
			if err := os.WriteFile(backupPath, []byte("defaulttenant: team\n"), 0o644); err != nil {
				t.Fatalf("write backup %s: %v", date, err)
			}
		}
		fixture.SeedAWSSharedConfig(t, setup, "123456789012", "corp-dev")
		envVars, _ := stubAWSCallerIdentityAndJWT(t, setup)
		result := erun.Run(t, []string{"doctor", "--repair-config"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   envVars,
			Stdin: "y\n",
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/repair_orphaned_alias_rotates_daily_backups", normalize.Apply(result.Combined))
		// Rotation is a side effect outside the captured streams: the
		// save writes today's snapshot, and retention keeps the five
		// newest dated files — today plus 2026-01-03..06.
		today := time.Now().UTC().Format("2006-01-02")
		for _, date := range []string{today, "2026-01-03", "2026-01-04", "2026-01-05", "2026-01-06"} {
			path := filepath.Join(setup.ConfigHome, "erun", "config.yaml."+date+".bak")
			if _, err := os.Stat(path); err != nil {
				t.Errorf("expected backup for %s to exist: %v", date, err)
			}
		}
		for _, date := range []string{"2026-01-01", "2026-01-02"} {
			path := filepath.Join(setup.ConfigHome, "erun", "config.yaml."+date+".bak")
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("expected backup for %s to be evicted, stat err: %v", date, err)
			}
		}
	})

	t.Run("repair_jetbrains_gateway_dry_run_then_real_run", func(t *testing.T) {
		// JetBrains Gateway repair against a seeded
		// sshRecentConnections.v2.xml carrying a latestUsedIde block (the
		// state IntelliJ leaves behind after a remote session; shape
		// mirrors open_test.go's gateway scenario). Pass 1 (--dry-run)
		// must preview the clear and leave the XML untouched; pass 2
		// (real) must route through
		// jetbrainsconfig.ClearRecentProjectLatestUsedIDE and strip the
		// block while preserving the rest of the project entry. The file
		// mutation is unreachable from dry-run by design.
		// ERUN_HOST_OS_OVERRIDE=linux pins the options-dir resolution to
		// ~/.config/JetBrains (per erun-integration/AGENTS.md, platform-
		// dependent goldens).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		configID := jetBrainsStableConfigID("erun-team-dev")
		projectPath := "/home/erun/git/" + filepath.Base(setup.Cwd)
		idePath := filepath.Join(setup.Home, "jetbrains-backends", "ideaIU-243.22562.222")
		xmlPath := seedJetBrainsRecentProject(t, setup, configID, projectPath, idePath)
		envVars := append(setup.Env(), "ERUN_HOST_OS_OVERRIDE=linux")

		dryRun := erun.Run(t, []string{"doctor", "team", "dev", "--repair-jetbrains-gateway", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if dryRun.ExitCode != 0 {
			t.Fatalf("dry-run exit %d: %s", dryRun.ExitCode, dryRun.Combined)
		}
		golden.Equal(t, "doctor/repair_jetbrains_gateway_dry_run", normalize.Apply(dryRun.Combined))
		if body := readFileForTest(t, xmlPath); !strings.Contains(body, "latestUsedIde") {
			t.Fatalf("expected dry-run to leave latestUsedIde in place, got:\n%s", body)
		}

		realRun := erun.Run(t, []string{"doctor", "team", "dev", "--repair-jetbrains-gateway"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if realRun.ExitCode != 0 {
			t.Fatalf("real-run exit %d: %s", realRun.ExitCode, realRun.Combined)
		}
		golden.Equal(t, "doctor/repair_jetbrains_gateway_real_run", normalize.Apply(realRun.Combined))
		body := readFileForTest(t, xmlPath)
		if strings.Contains(body, "latestUsedIde") {
			t.Errorf("expected real run to clear latestUsedIde, got:\n%s", body)
		}
		if !strings.Contains(body, projectPath) {
			t.Errorf("expected real run to preserve the project entry for %s, got:\n%s", projectPath, body)
		}
	})

	t.Run("repair_jetbrains_gateway_no_metadata", func(t *testing.T) {
		// --repair-jetbrains-gateway with no JetBrains install at all:
		// the options-dir resolution fails, the repair finds nothing to
		// clear, and doctor must say so and stop (covering the not-found
		// arm of runSelectedJetBrainsGatewayRepair) instead of falling
		// through to the kubectl-driven cleanup actions.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		envVars := append(setup.Env(), "ERUN_HOST_OS_OVERRIDE=linux")
		result := erun.Run(t, []string{"doctor", "team", "dev", "--repair-jetbrains-gateway"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/repair_jetbrains_gateway_no_metadata", normalize.Apply(result.Combined))
	})

	t.Run("real_run_prune_images_and_build_cache_via_stubs", func(t *testing.T) {
		// Real-run doctor cleanup with a healthy release: covers the
		// non-dry-run arms of runDeployDiagnosis (helm status + pods
		// rendered, guidance line), RecommendedDeployRecovery declining
		// to offer a recovery on STATUS: deployed, the real
		// traceAndWaitForRuntime/RunTracedRuntimeContainerCommand path
		// through kubectl, and two doctor actions (prune images + build
		// cache) printing their dind output via writeDoctorCommandOutput.
		// helm/kubectl are stubbed to drive the side effects the dry-run
		// trace stops short of (per erun-integration/AGENTS.md, real-run
		// scenarios via stubs).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := filepath.Join(setup.Cwd, "stubs")
		stubDoctorHelmStatus(t, stubs, "deployed")
		stubDoctorKubectl(t, stubs, "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
		result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images", "--prune-build-cache"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/real_run_prune_images_and_build_cache_via_stubs", normalize.Apply(result.Combined))
	})

	t.Run("real_run_clear_pending_helm_via_prompt_then_prune_containers", func(t *testing.T) {
		// The helm stub reports STATUS: pending-install, so the
		// diagnosis recommends exactly one recovery and the interactive
		// confirm (the single prompt of this subprocess) accepts it:
		// covers selectedDeployRecoveryActions' prompt arm,
		// RecommendedDeployRecovery's pending-install match, and
		// RunDeployRecovery's real clear-pending arm
		// (ClearHelmReleasePendingOperation through the kubectl stub).
		// --prune-containers then exercises the third doctor action for
		// real without adding a second prompt.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := filepath.Join(setup.Cwd, "stubs")
		stubDoctorHelmStatus(t, stubs, "pending-install")
		stubDoctorKubectl(t, stubs, "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
		result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-containers"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   envVars,
			Stdin: "y\n",
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/real_run_clear_pending_helm_via_prompt_then_prune_containers", normalize.Apply(result.Combined))
	})

	t.Run("real_run_storage_unhealthy_diagnostic_error", func(t *testing.T) {
		// kubectl wait fails with a disk i/o error: doctor must fold the
		// stderr into the storage-unhealthy diagnostic
		// (normalizeDoctorKubectlError → doctorKubectlDiagnostic's
		// unhealthy-storage arm) instead of surfacing a bare exit-status
		// error. Only reachable in a real run — dry-run never executes
		// the wait.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := filepath.Join(setup.Cwd, "stubs")
		stubDoctorHelmStatus(t, stubs, "deployed")
		stubDoctorKubectl(t, stubs, `echo 'Error from server: disk i/o error' >&2; exit 1`)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
		result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0: %s", result.Combined)
		}
		golden.Equal(t, "doctor/real_run_storage_unhealthy_diagnostic_error", normalize.Apply(result.Combined))
	})

	t.Run("repair_config_recovers_cloud_context_real_run", func(t *testing.T) {
		// Real-run recovery of an orphaned cloud context: the confirm
		// (the single prompt of this subprocess) accepts, then
		// RecoverCloudContextFromAWS parses real describe-instances /
		// describe-volumes JSON from the aws stub, restores the admin
		// token from a seeded ~/.kube/config (the "kubeconfig" arm of
		// the recovery summary), and persists the rebuilt context. The
		// dry-run sibling scenario stops at placeholder values, so the
		// JSON parsing, token lookup, and save only run here.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "petios", "rihards-review")
		alias := "alice+020362606330@aws"
		seedOrphanedCloudContextEnv(t, setup, "petios", "rihards-review", alias)
		kubeDir := filepath.Join(setup.Home, ".kube")
		if err := os.MkdirAll(kubeDir, 0o700); err != nil {
			t.Fatalf("mkdir .kube: %v", err)
		}
		kubeconfig := "apiVersion: v1\n" +
			"kind: Config\n" +
			"users:\n" +
			"    - name: erun-001-020362606330-eu-west-2\n" +
			"      user:\n" +
			"        token: stub-admin-token\n"
		if err := os.WriteFile(filepath.Join(kubeDir, "config"), []byte(kubeconfig), 0o600); err != nil {
			t.Fatalf("write kubeconfig: %v", err)
		}
		stubs := filepath.Join(setup.Cwd, "stubs")
		stubAWSDescribeForContextRecovery(t, stubs,
			`{"Reservations":[{"Instances":[{"InstanceId":"i-0aaaabbbbcccc1111","PublicIpAddress":"203.0.113.10","InstanceType":"c8gd.2xlarge","LaunchTime":"2026-01-02T03:04:05+00:00","BlockDeviceMappings":[{"Ebs":{"VolumeId":"vol-0a1b2c3d"}}]}]}]}`)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		result := erun.Run(t, []string{"doctor", "--repair-config"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   envVars,
			Stdin: "y\n",
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/repair_config_recovers_cloud_context_real_run", normalize.Apply(result.Combined))
		// Persistence is a side effect outside the captured streams: the
		// recovered context must land in the root config with the
		// coordinates parsed from AWS plus the kubeconfig token.
		raw := readFileForTest(t, filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
		for _, want := range []string{
			"kubernetescontext: erun-001-020362606330-eu-west-2",
			"instanceid: i-0aaaabbbbcccc1111",
			"instancetype: c8gd.2xlarge",
			"disktype: gp3",
			"disksizegb: 100",
			"admintoken: stub-admin-token",
		} {
			if !strings.Contains(raw, want) {
				t.Errorf("expected persisted config to contain %q, got:\n%s", want, raw)
			}
		}
	})

	t.Run("repair_config_cloud_context_recovery_failure", func(t *testing.T) {
		// Same orphaned context, but AWS reports no instance with the
		// expected Name tag: the recovery must print the failure with
		// its cause and continue (exit 0) rather than aborting the
		// repair walk — covering the non-fatal failure arm of
		// offerOrphanedCloudContextRecovery and the zero-match arm of
		// describeCloudContextInstanceByName.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "petios", "rihards-review")
		alias := "alice+020362606330@aws"
		seedOrphanedCloudContextEnv(t, setup, "petios", "rihards-review", alias)
		stubs := filepath.Join(setup.Cwd, "stubs")
		stubAWSDescribeForContextRecovery(t, stubs, `{"Reservations":[]}`)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		result := erun.Run(t, []string{"doctor", "--repair-config"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   envVars,
			Stdin: "y\n",
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/repair_config_cloud_context_recovery_failure", normalize.Apply(result.Combined))
		raw := readFileForTest(t, filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
		if strings.Contains(raw, "cloudcontexts") {
			t.Errorf("expected failed recovery to leave root config without contexts, got:\n%s", raw)
		}
	})

	t.Run("real_run_namespace_listed_but_api_failing_error", func(t *testing.T) {
		// kubectl wait reports the runtime namespace as not found while
		// `kubectl get namespaces` lists it — the split-brain failure
		// mode where the namespace exists but direct API access is
		// broken. Covers doctorNamespaceLookupFailed +
		// doctorNamespaceIsListed and the second diagnostic arm of
		// doctorKubectlDiagnostic; the probe-then-diagnose sequence only
		// runs on a real wait failure.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := filepath.Join(setup.Cwd, "stubs")
		stubDoctorHelmStatus(t, stubs, "deployed")
		stubDoctorKubectl(t, stubs, `echo 'Error from server (NotFound): namespaces "team-dev" not found' >&2; exit 1`)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "helm", "kubectl")...)
		result := erun.Run(t, []string{"doctor", "team", "dev", "--prune-images"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit, got 0: %s", result.Combined)
		}
		golden.Equal(t, "doctor/real_run_namespace_listed_but_api_failing_error", normalize.Apply(result.Combined))
	})

	// --- erun doctor --sync-config: in-pod config reconciliation ---

	t.Run("in_runtime_sync_config_in_sync_dry_run", func(t *testing.T) {
		// The on-disk projection already matches the injected env, so the only
		// action is a status line — no write-yaml traces (the no-op branch).
		setup := env.New(t)
		seedRuntimeEnvConfig(t, setup, "team", "dev",
			"name: dev\ntype: runtime\nkubernetescontext: in-cluster\nidle:\n  timeout: 5m0s\n  workinghours: 08:00-20:00\n")
		envVars := append(setup.Env(),
			"ERUN_REPO_REMOTE=true", "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev",
			"ERUN_ENV_TYPE=runtime", "ERUN_KUBERNETES_CONTEXT=in-cluster")
		result := erun.Run(t, []string{"doctor", "--sync-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "doctor/in_runtime_sync_config_in_sync_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("in_runtime_sync_config_missing_cloud_context_dry_run", func(t *testing.T) {
		// ERUN_CLOUD_* injected but the on-disk root carries no cloud context:
		// missing drift for the root blocks plus the managedcloud flip, and
		// write-yaml traces for both the env and root files.
		setup := env.New(t)
		seedRuntimeEnvConfig(t, setup, "team", "dev",
			"name: dev\ntype: runtime\nkubernetescontext: in-cluster\nidle:\n  timeout: 5m0s\n  workinghours: 08:00-20:00\n")
		envVars := append(setup.Env(),
			"ERUN_REPO_REMOTE=true", "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev",
			"ERUN_ENV_TYPE=runtime", "ERUN_KUBERNETES_CONTEXT=in-cluster",
			"ERUN_CLOUD_ENVIRONMENT=true", "ERUN_CLOUD_PROVIDER=aws",
			"ERUN_CLOUD_PROVIDER_ALIAS=team-aws", "ERUN_CLOUD_REGION=eu-west-1",
			"ERUN_CLOUD_CONTEXT_NAME=team-context", "ERUN_CLOUD_INSTANCE_ID=i-0abc")
		result := erun.Run(t, []string{"doctor", "--sync-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "doctor/in_runtime_sync_config_missing_cloud_context_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("in_runtime_sync_config_legacy_remote_key_dry_run", func(t *testing.T) {
		// A `remote: true` key on disk is detected as legacy drift for
		// `type` even though a struct decode would migrate it away, and the
		// rewrite drops it in favour of the canonical type.
		setup := env.New(t)
		seedRuntimeEnvConfig(t, setup, "team", "dev",
			"name: dev\nremote: true\nkubernetescontext: in-cluster\nidle:\n  timeout: 5m0s\n  workinghours: 08:00-20:00\n")
		envVars := append(setup.Env(),
			"ERUN_REPO_REMOTE=true", "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev",
			"ERUN_ENV_TYPE=runtime", "ERUN_KUBERNETES_CONTEXT=in-cluster")
		result := erun.Run(t, []string{"doctor", "--sync-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "doctor/in_runtime_sync_config_legacy_remote_key_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("in_runtime_sync_config_wrong_value_dry_run", func(t *testing.T) {
		// On-disk kubernetescontext differs from the injected value: wrong drift
		// plus an env-file write trace.
		setup := env.New(t)
		seedRuntimeEnvConfig(t, setup, "team", "dev",
			"name: dev\ntype: runtime\nkubernetescontext: stale-context\nidle:\n  timeout: 5m0s\n  workinghours: 08:00-20:00\n")
		envVars := append(setup.Env(),
			"ERUN_REPO_REMOTE=true", "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev",
			"ERUN_ENV_TYPE=runtime", "ERUN_KUBERNETES_CONTEXT=in-cluster")
		result := erun.Run(t, []string{"doctor", "--sync-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "doctor/in_runtime_sync_config_wrong_value_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("in_runtime_sync_config_idle_drift_dry_run", func(t *testing.T) {
		// The idle block is in the projection: an on-disk timeout that differs
		// from the injected one is flagged and rewritten.
		setup := env.New(t)
		seedRuntimeEnvConfig(t, setup, "team", "dev",
			"name: dev\ntype: runtime\nkubernetescontext: in-cluster\nidle:\n  timeout: 10m0s\n  workinghours: 08:00-20:00\n")
		envVars := append(setup.Env(),
			"ERUN_REPO_REMOTE=true", "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev",
			"ERUN_ENV_TYPE=runtime", "ERUN_KUBERNETES_CONTEXT=in-cluster", "ERUN_IDLE_TIMEOUT=5m0s")
		result := erun.Run(t, []string{"doctor", "--sync-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "doctor/in_runtime_sync_config_idle_drift_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("in_runtime_sync_config_legacy_repopath_no_spurious_drift_dry_run", func(t *testing.T) {
		// On-disk `repopath:` with no `localrepopath:` and an injected env that
		// carries no LocalRepoPath must NOT report repopath/localrepopath drift
		// (the field is excluded from the comparison set), so an otherwise
		// matching config reports in-sync.
		setup := env.New(t)
		seedRuntimeEnvConfig(t, setup, "team", "dev",
			"name: dev\ntype: runtime\nrepopath: /home/erun/git/team\nkubernetescontext: in-cluster\nidle:\n  timeout: 5m0s\n  workinghours: 08:00-20:00\n")
		envVars := append(setup.Env(),
			"ERUN_REPO_REMOTE=true", "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev",
			"ERUN_ENV_TYPE=runtime", "ERUN_KUBERNETES_CONTEXT=in-cluster")
		result := erun.Run(t, []string{"doctor", "--sync-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "doctor/in_runtime_sync_config_legacy_repopath_no_spurious_drift_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("in_runtime_sync_config_no_tenant_env_noop", func(t *testing.T) {
		// ERUN_REPO_REMOTE=true but no tenant/environment: nothing to resolve,
		// so the command reports it and writes nothing.
		setup := env.New(t)
		envVars := append(setup.Env(), "ERUN_REPO_REMOTE=true")
		result := erun.Run(t, []string{"doctor", "--sync-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "doctor/in_runtime_sync_config_no_tenant_env_noop", normalize.Apply(result.Combined))
	})

	t.Run("in_runtime_sync_config_real_run_reconciles_and_preserves", func(t *testing.T) {
		// Real run: a drifting projected key (kubernetescontext) is rewritten
		// while unprojected keys (sshd, runtimeversion) are preserved — the
		// load-bearing read-modify-write invariant.
		setup := env.New(t)
		seedRuntimeEnvConfig(t, setup, "team", "dev",
			"name: dev\ntype: runtime\nkubernetescontext: stale-context\nruntimeversion: 1.2.3\nsshd:\n  enabled: true\nidle:\n  timeout: 5m0s\n  workinghours: 08:00-20:00\n")
		envVars := append(setup.Env(),
			"ERUN_REPO_REMOTE=true", "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev",
			"ERUN_ENV_TYPE=runtime", "ERUN_KUBERNETES_CONTEXT=in-cluster")
		result := erun.Run(t, []string{"doctor", "--sync-config"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "doctor/in_runtime_sync_config_real_run_reconciles_and_preserves", normalize.Apply(result.Combined))
		written := readFileForTest(t, filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml"))
		if !strings.Contains(written, "kubernetescontext: in-cluster") {
			t.Fatalf("projected key not reconciled:\n%s", written)
		}
		if !strings.Contains(written, "runtimeversion: 1.2.3") || !strings.Contains(written, "enabled: true") {
			t.Fatalf("unprojected keys not preserved:\n%s", written)
		}
	})

	t.Run("in_runtime_sync_config_real_run_idempotent", func(t *testing.T) {
		// After a real reconcile, a second --sync-config run round-trips to
		// in-sync with zero drift — no perpetual-drift loop.
		setup := env.New(t)
		seedRuntimeEnvConfig(t, setup, "team", "dev",
			"name: dev\ntype: runtime\nkubernetescontext: stale-context\nidle:\n  timeout: 5m0s\n  workinghours: 08:00-20:00\n")
		envVars := append(setup.Env(),
			"ERUN_REPO_REMOTE=true", "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev",
			"ERUN_ENV_TYPE=runtime", "ERUN_KUBERNETES_CONTEXT=in-cluster")
		first := erun.Run(t, []string{"doctor", "--sync-config"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if first.ExitCode != 0 {
			t.Fatalf("first run exit %d: %s", first.ExitCode, first.Combined)
		}
		second := erun.Run(t, []string{"doctor", "--sync-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if !strings.Contains(second.Combined, "In-pod config matches the injected env") {
			t.Fatalf("second run not in-sync (perpetual drift):\n%s", second.Combined)
		}
		if strings.Contains(second.Combined, "write-yaml") {
			t.Fatalf("second run still traced writes:\n%s", second.Combined)
		}
	})
}

// seedRuntimeEnvConfig mirrors what the entrypoint would have written on disk so
// the --sync-config scenarios can drive reconciliation.
func seedRuntimeEnvConfig(t *testing.T, setup env.Setup, tenant, environment, envYAML string) {
	t.Helper()
	envDir := filepath.Join(setup.ConfigHome, "erun", tenant, environment)
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir env config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "config.yaml"), []byte(envYAML), 0o644); err != nil {
		t.Fatalf("write env config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"), []byte("defaulttenant: "+tenant+"\n"), 0o644); err != nil {
		t.Fatalf("write root config: %v", err)
	}
}

// seedOrphanedCloudContextEnv shapes the orphaned-context inspection input: the
// env names a cloud-managed kubernetes context the root config does not list,
// while the root still registers the provider so only the context orphan surfaces.
func seedOrphanedCloudContextEnv(t *testing.T, setup env.Setup, tenant, environment, alias string) {
	t.Helper()
	tenantPath := filepath.Join(setup.ConfigHome, "erun", tenant, "config.yaml")
	if err := os.WriteFile(tenantPath, []byte("projectroot: "+setup.Cwd+"\n"+
		"name: "+tenant+"\n"+
		"defaultenvironment: "+environment+"\n"+
		"cloudprovideraliases:\n"+
		"    - "+alias+"\n"+
		"primarycloudprovideralias: "+alias+"\n"), 0o644); err != nil {
		t.Fatalf("write tenant: %v", err)
	}
	envPath := filepath.Join(setup.ConfigHome, "erun", tenant, environment, "config.yaml")
	if err := os.WriteFile(envPath, []byte("name: "+environment+"\n"+
		"repopath: "+setup.Cwd+"\n"+
		"kubernetescontext: erun-001-020362606330-eu-west-2\n"+
		"cloudprovideralias: "+alias+"\n"+
		"managedcloud: true\n"), 0o644); err != nil {
		t.Fatalf("write env: %v", err)
	}
	rootPath := filepath.Join(setup.ConfigHome, "erun", "config.yaml")
	if err := os.WriteFile(rootPath, []byte("defaulttenant: "+tenant+"\n"+
		"cloudproviders:\n"+
		"    - alias: "+alias+"\n"+
		"      provider: aws\n"+
		"      username: alice\n"+
		"      accountid: \"020362606330\"\n"), 0o644); err != nil {
		t.Fatalf("write root: %v", err)
	}
}

// stubAWSDescribeForContextRecovery stubs the two AWS reads
// RecoverCloudContextFromAWS performs on a real run: describe-instances
// (the caller-supplied JSON — the decision input for the match/no-match
// arms) and describe-volumes (a fixed gp3/100G answer). Everything else
// exits 0 silently.
func stubAWSDescribeForContextRecovery(t *testing.T, stubsDir, describeInstancesJSON string) {
	t.Helper()
	script := strings.Join([]string{
		`case "$*" in`,
		`  *"ec2 describe-instances"*) printf '%s\n' '` + describeInstancesJSON + `' ;;`,
		`  *"ec2 describe-volumes"*) printf '%s\n' '{"Volumes":[{"VolumeType":"gp3","Size":100}]}' ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubsDir, "aws", script)
}

// seedRemoteInitMarker writes a bootstrap marker under the
// per-tenant/per-environment path doctor's in-runtime inspection reads.
func seedRemoteInitMarker(t *testing.T, setup env.Setup, tenant, environment, body string) {
	t.Helper()
	markerDir := filepath.Join(setup.Home, ".erun", tenant, environment)
	if err := os.MkdirAll(markerDir, 0o700); err != nil {
		t.Fatalf("mkdir marker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "bootstrap.yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
}

// stubSSHKeygenWritingKeypair stubs ssh-keygen to actually create the
// requested keypair files (private key plus .pub) at the -f path, the
// side effect the real-run remote-init recovery depends on: production
// chmods the private key and reads the .pub to print it for import.
// The key type in argv picks the public-key flavor so ed25519 and the
// CodeCommit RSA key produce distinguishable golden output.
func stubSSHKeygenWritingKeypair(t *testing.T, stubsDir string) {
	t.Helper()
	script := strings.Join([]string{
		`for arg in "$@"; do keyfile="$arg"; done`,
		`mkdir -p "$(dirname "$keyfile")"`,
		`printf 'stub-private-key\n' > "$keyfile"`,
		`case "$*" in`,
		`  *" rsa "*) printf 'ssh-rsa AAAASTUBRSAKEY erun-doctor-test\n' > "$keyfile.pub" ;;`,
		`  *) printf 'ssh-ed25519 AAAASTUBEDKEY erun-doctor-test\n' > "$keyfile.pub" ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubsDir, "ssh-keygen", script)
}

// stubGitForRemoteInit stubs git for the remote-init recovery: clone
// always succeeds; ls-remote either succeeds immediately or — when
// failFirstLsRemote is set — fails exactly once so WaitForGitAccess
// runs one retry cycle (the "not active yet / access confirmed" lines).
// The first-call state lives in a marker file beside the stub.
func stubGitForRemoteInit(t *testing.T, stubsDir string, failFirstLsRemote bool) {
	t.Helper()
	lines := []string{`case "$*" in`, `  *"ls-remote"*)`}
	if failFirstLsRemote {
		attempted := filepath.Join(stubsDir, "ls-remote-attempted")
		lines = append(lines,
			`    if [ ! -f '`+attempted+`' ]; then`,
			`      : > '`+attempted+`'`,
			`      echo 'fatal: Could not read from remote repository.' >&2`,
			`      exit 128`,
			`    fi`,
		)
	}
	lines = append(lines,
		`    printf 'stub-ref\tHEAD\n'`,
		`    exit 0`,
		`    ;;`,
		`esac`,
		`exit 0`,
	)
	fixture.StubBinaryWithScript(t, stubsDir, "git", strings.Join(lines, "\n"))
}

// stubDoctorHelmStatus stubs `helm status` to report the runtime
// release in the given state; every other helm invocation succeeds
// silently. The status string is the decision input
// RecommendedDeployRecovery branches on.
func stubDoctorHelmStatus(t *testing.T, stubsDir, releaseStatus string) {
	t.Helper()
	script := strings.Join([]string{
		`case "$1" in`,
		`  status) printf '%s\n' 'NAME: team-devops' 'STATUS: ` + releaseStatus + `' ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubsDir, "helm", script)
}

// stubDoctorKubectl stubs every kubectl surface the real-run doctor
// cleanup path touches: the pod listing for the diagnosis, the
// deployment wait (overridable via waitArm to simulate cluster
// failures), the pending-helm lock delete, the namespace probe the
// failure diagnostic runs, and the dind exec scripts for inspection and
// the three prune actions (matched on their distinctive docker lines).
func stubDoctorKubectl(t *testing.T, stubsDir, waitArm string) {
	t.Helper()
	if waitArm == "" {
		waitArm = ":"
	}
	script := strings.Join([]string{
		`case "$*" in`,
		`  *" get pods "*) printf '%s\n' 'NAME                READY   STATUS    RESTARTS' 'team-devops-pod-1   2/2     Running   0' ;;`,
		`  *" get namespaces "*) printf 'namespace/team-dev\n' ;;`,
		`  *" wait "*) ` + waitArm + ` ;;`,
		`  *" delete "*) printf 'secret/sh.helm.release deleted\n' ;;`,
		`  *"df -h /var/lib/docker"*) printf '%s\n' 'Filesystem  Size  Used  Avail  Mounted on' 'overlay     100G  20G   80G    /var/lib/docker' ;;`,
		`  *"docker image prune"*) printf '%s\n' 'Total reclaimed space: 2GB' ;;`,
		`  *"docker builder prune"*) printf '%s\n' 'Total: 3GB' ;;`,
		`  *"docker container prune"*) printf '%s\n' 'Total reclaimed space: 1GB' ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubsDir, "kubectl", script)
}

// jetBrainsStableConfigID duplicates jetbrainsconfig.StableConfigID because that
// internal package cannot be imported across modules; the seeded XML must carry
// the same configId or FindRecentProject never matches.
func jetBrainsStableConfigID(hostAlias string) string {
	sum := sha1.Sum([]byte(strings.TrimSpace(hostAlias)))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		b[0], b[1], b[2], b[3],
		b[4], b[5],
		b[6], b[7],
		b[8], b[9],
		b[10], b[11], b[12], b[13], b[14], b[15],
	)
}

// seedJetBrainsRecentProject writes the linux-side IntelliJ options dir
// with a sshRecentConnections.v2.xml whose single recent project
// carries a latestUsedIde block — the exact state `erun open
// --intellij` plus a finished Gateway session leaves behind (shape
// cribbed from open_test.go's gateway scenario). Returns the XML path
// so callers can assert the on-disk mutation.
func seedJetBrainsRecentProject(t *testing.T, setup env.Setup, configID, projectPath, idePath string) string {
	t.Helper()
	optionsDir := filepath.Join(setup.ConfigHome, "JetBrains", "IntelliJIdea2024.3", "options")
	if err := os.MkdirAll(optionsDir, 0o755); err != nil {
		t.Fatalf("mkdir IntelliJ options: %v", err)
	}
	body := `<application>
  <component name="SshLocalRecentConnectionsManager">
    <option name="connections">
      <list>
        <LocalRecentConnectionState>
          <option name="configId" value="` + configID + `" />
          <option name="projects">
            <list>
              <RecentProjectState>
                <option name="latestUsedIde">
                  <RecentProjectInstalledIde>
                    <option name="buildNumber" value="243.22562.222" />
                    <option name="pathToIde" value="` + idePath + `" />
                    <option name="productCode" value="IU" />
                  </RecentProjectInstalledIde>
                </option>
                <option name="date" value="1748000000000" />
                <option name="productCode" value="IU" />
                <option name="projectPath" value="` + projectPath + `" />
              </RecentProjectState>
            </list>
          </option>
        </LocalRecentConnectionState>
      </list>
    </option>
  </component>
</application>
`
	path := filepath.Join(optionsDir, "sshRecentConnections.v2.xml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write sshRecentConnections.v2.xml: %v", err)
	}
	return path
}

func readFileForTest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	// Windows has no Unix permission bits — os.Chmod only toggles the read-only
	// attribute, so Stat reports 0666/0777. The mode contract is Unix-only.
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Errorf("stat %s: %v", path, err)
		return
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("expected %s mode %o, got %o", path, want, got)
	}
}
