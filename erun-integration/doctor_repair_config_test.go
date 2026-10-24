package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func doctorRepairConfigScenarios(t *testing.T) {
	t.Run("repair_orphaned_alias_real_run_via_aws_config_profile", testDoctorRepairOrphanedAliasRealRunViaAwsConfigProfile)
	t.Run("restore_config_from_backup_dry_run", testDoctorRestoreConfigFromBackupDryRun)
	t.Run("repair_orphaned_alias_real_run_declined", testDoctorRepairOrphanedAliasRealRunDeclined)
	t.Run("restore_config_from_backup_real_run", testDoctorRestoreConfigFromBackupRealRun)
	t.Run("restore_config_from_backup_invalid_date_error", testDoctorRestoreConfigFromBackupInvalidDateError)
	t.Run("restore_config_from_backup_no_match_error", testDoctorRestoreConfigFromBackupNoMatchError)
	t.Run("restore_env_config_from_backup_dry_run", testDoctorRestoreEnvConfigFromBackupDryRun)
	t.Run("restore_env_config_from_backup_real_run", testDoctorRestoreEnvConfigFromBackupRealRun)
	t.Run("restore_env_config_from_backup_no_match_error", testDoctorRestoreEnvConfigFromBackupNoMatchError)
	t.Run("restore_env_config_from_backup_requires_target_error", testDoctorRestoreEnvConfigFromBackupRequiresTargetError)
	t.Run("repair_config_restores_backup_via_prompt", testDoctorRepairConfigRestoresBackupViaPrompt)
	t.Run("repair_orphaned_alias_rotates_daily_backups", testDoctorRepairOrphanedAliasRotatesDailyBackups)
	t.Run("repair_config_recovers_cloud_context_real_run", testDoctorRepairConfigRecoversCloudContextRealRun)
	t.Run("repair_config_cloud_context_recovery_failure", testDoctorRepairConfigCloudContextRecoveryFailure)
}

func testDoctorRepairOrphanedAliasRealRunViaAwsConfigProfile(t *testing.T) {
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
}

func testDoctorRestoreConfigFromBackupDryRun(t *testing.T) {
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
}

func testDoctorRepairOrphanedAliasRealRunDeclined(t *testing.T) {
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
}

func testDoctorRestoreConfigFromBackupRealRun(t *testing.T) {
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
}

func testDoctorRestoreConfigFromBackupInvalidDateError(t *testing.T) {
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
}

func testDoctorRestoreConfigFromBackupNoMatchError(t *testing.T) {
	// A well-formed date with no backup on disk must name the
	// unmatched selector so the user can pick from the listed dates.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"doctor", "--restore-config-from-backup", "2026-01-01"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0: %s", result.Combined)
	}
	golden.Equal(t, "doctor/restore_config_from_backup_no_match_error", normalize.Apply(result.Combined))
}

func testDoctorRestoreEnvConfigFromBackupDryRun(t *testing.T) {
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
}

func testDoctorRestoreEnvConfigFromBackupRealRun(t *testing.T) {
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
}

func testDoctorRestoreEnvConfigFromBackupNoMatchError(t *testing.T) {
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
}

func testDoctorRestoreEnvConfigFromBackupRequiresTargetError(t *testing.T) {
	// Without an explicit tenant and environment there is no env to
	// restore; the flag must fail fast rather than guess a default.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"doctor", "--restore-env-config-from-backup", "2026-05-19"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0: %s", result.Combined)
	}
	golden.Equal(t, "doctor/restore_env_config_from_backup_requires_target_error", normalize.Apply(result.Combined))
}

func testDoctorRepairConfigRestoresBackupViaPrompt(t *testing.T) {
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
}

func testDoctorRepairOrphanedAliasRotatesDailyBackups(t *testing.T) {
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
}

func testDoctorRepairConfigRecoversCloudContextRealRun(t *testing.T) {
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
}

func testDoctorRepairConfigCloudContextRecoveryFailure(t *testing.T) {
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
