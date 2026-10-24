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

func doctorInspectConfigScenarios(t *testing.T) {
	t.Run("inspect_clean_root_config", testDoctorInspectCleanRootConfig)
	t.Run("inspect_orphaned_alias", testDoctorInspectOrphanedAlias)
	t.Run("inspect_corrupted_root_config", testDoctorInspectCorruptedRootConfig)
	t.Run("inspect_orphaned_cloud_context", testDoctorInspectOrphanedCloudContext)
	t.Run("inspect_missing_root_config", testDoctorInspectMissingRootConfig)
	t.Run("inspect_malformed_and_unsupported_orphan_aliases", testDoctorInspectMalformedAndUnsupportedOrphanAliases)
	t.Run("inspect_orphan_alias_referenced_by_cloud_contexts", testDoctorInspectOrphanAliasReferencedByCloudContexts)
}

func testDoctorInspectCleanRootConfig(t *testing.T) {
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
}

func testDoctorInspectOrphanedAlias(t *testing.T) {
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
}

func testDoctorInspectCorruptedRootConfig(t *testing.T) {
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
}

func testDoctorInspectOrphanedCloudContext(t *testing.T) {
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
}

func testDoctorInspectMissingRootConfig(t *testing.T) {
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
}

func testDoctorInspectMalformedAndUnsupportedOrphanAliases(t *testing.T) {
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
}

func testDoctorInspectOrphanAliasReferencedByCloudContexts(t *testing.T) {
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
}
