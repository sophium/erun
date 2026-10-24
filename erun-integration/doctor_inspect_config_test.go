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
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"doctor", "--repair-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/inspect_clean_root_config", normalize.Apply(result.Combined))
}

func testDoctorInspectOrphanedAlias(t *testing.T) {
	// Dry-run is non-interactive, so repair is suggested, not prompted.
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
	// With no backup, repair must not silently overwrite the file.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	rootPath := filepath.Join(setup.ConfigHome, "erun", "config.yaml")
	if err := os.WriteFile(rootPath, []byte(""), 0o644); err != nil {
		t.Fatalf("truncate root config: %v", err)
	}
	result := erun.Run(t, []string{"doctor", "--repair-config", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	// The golden is locked, not the exit code.
	_ = result
	golden.Equal(t, "doctor/inspect_corrupted_root_config", normalize.Apply(result.Combined))
}

func testDoctorInspectOrphanedCloudContext(t *testing.T) {
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
	// Keep the alias side clean so only the context orphan surfaces.
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
	// One malformed alias and one non-aws provider; repair skips both.
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
