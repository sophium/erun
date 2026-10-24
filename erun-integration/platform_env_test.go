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

func testPlatformEnvRegisterDryRun(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	args := []string{"platform", "env", "register", "--name", "prod", "--type", "runtime", "--runtime-version", "1.2.3", "--dry-run"}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "platform/env_register_dry_run", normalize.Apply(result.Combined))
}

func testPlatformEnvLifecycleRealRun(t *testing.T) {
	setup := env.New(t)
	server := platformAPIStubServer(t)
	platformAlias(t, setup, server)

	steps := []struct {
		label string
		args  []string
		want  string
	}{
		{"register", []string{"platform", "env", "register", "--name", "prod", "--type", "runtime"}, ""},
		{"list", []string{"platform", "env", "list"}, "prod"},
		{"get", []string{"platform", "env", "get", "env-1"}, "env-1"},
		{"deploy", []string{"platform", "env", "deploy", "env-1", "--version", "1.3.0"}, "provisioning"},
		{"stop", []string{"platform", "env", "stop", "env-1"}, ""},
		{"delete", []string{"platform", "env", "delete", "env-1", "-y"}, "status=deleting"},
	}
	for _, step := range steps {
		result := erun.Run(t, step.args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 || !strings.Contains(result.Combined, step.want) {
			t.Fatalf("%s exit %d: %s", step.label, result.ExitCode, result.Combined)
		}
	}
}

func testPlatformEnvPushDryRun(t *testing.T) {
	// The fields list is the allowlist, so a host-owned value here would be a leak.
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	fixture.SeedHostedRuntimeTenantEnv(t, setup, "acme", "prod", "https://api.example.test", "tenant-1", "env-1", 1)
	result := erun.Run(t, []string{"platform", "env", "push", "acme", "prod", "--dry-run"},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "platform/env_push_dry_run", normalize.Apply(result.Combined))
}

func testPlatformEnvPullDryRun(t *testing.T) {
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	fixture.SeedHostedRuntimeTenantEnv(t, setup, "acme", "prod", "https://api.example.test", "tenant-1", "env-1", 1)
	result := erun.Run(t, []string{"platform", "env", "pull", "acme", "prod", "--dry-run"},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "platform/env_pull_dry_run", normalize.Apply(result.Combined))
}

func testPlatformEnvDefinitionRoundTripRealRun(t *testing.T) {
	// Upload then pull: portable fields arrive, host-owned values stay.
	setup := env.New(t)
	server := platformAPIStubServer(t)
	platformAlias(t, setup, server)
	fixture.SeedHostedRuntimeTenantEnv(t, setup, "acme", "prod", server.URL, "tenant-1", "env-1", 1)
	configPath := filepath.Join(setup.ConfigHome, "erun", "acme", "prod", "config.yaml")

	push := erun.Run(t, []string{"platform", "env", "push", "acme", "prod"},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if push.ExitCode != 0 {
		t.Fatalf("push exit %d: %s", push.ExitCode, push.Combined)
	}
	if !strings.Contains(push.Combined, "revision 1") {
		t.Fatalf("push did not report the revision it wrote:\n%s", push.Combined)
	}

	rewriteEnvConfigLine(t, configPath, "runtimeversion: 1.0.0", "runtimeversion: 9.9.9")
	pull := erun.Run(t, []string{"platform", "env", "pull", "acme", "prod"},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if pull.ExitCode != 0 {
		t.Fatalf("pull exit %d: %s", pull.ExitCode, pull.Combined)
	}

	restored := mustReadFile(t, configPath)
	if !strings.Contains(restored, "runtimeversion: 1.0.0") {
		t.Errorf("pull did not restore the portable runtime version:\n%s", restored)
	}
	if !strings.Contains(restored, "definitionrevision: 1") {
		t.Errorf("pull did not stamp the revision it synced from:\n%s", restored)
	}
	for _, hostOwned := range []string{
		"host-owned-pull-secret",
		"host-owned-component",
		"localportrangestart: 17100",
		"localrepopath:",
		// The writer only touches keys EnvConfig declares, so unknown keys survive.
		"futureunmodelledkey: keep-me",
	} {
		if !strings.Contains(restored, hostOwned) {
			t.Errorf("pull dropped the host-owned value %q:\n%s", hostOwned, restored)
		}
	}
}

func testPlatformEnvPullRefusesAPlatformNameMismatch(t *testing.T) {
	// The stub row is "prod" and this env is "dev"; writing would create a second env.
	setup := env.New(t)
	server := platformAPIStubServer(t)
	platformAlias(t, setup, server)
	fixture.SeedHostedRuntimeTenantEnv(t, setup, "acme", "dev", server.URL, "tenant-1", "env-1", 1)

	result := erun.Run(t, []string{"platform", "env", "pull", "acme", "dev"},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("a platform-name mismatch was accepted:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "Name is platform-owned") {
		t.Fatalf("refusal did not name the platform-owned field:\n%s", result.Combined)
	}
	configPath := filepath.Join(setup.ConfigHome, "erun", "acme", "dev", "config.yaml")
	if config := mustReadFile(t, configPath); strings.Contains(config, "runtimeversion: 1.2.3") {
		t.Fatalf("a refused pull still wrote the platform's definition:\n%s", config)
	}
}

func testPlatformEnvPullRefusesAMarkerPointingAtAnotherTenant(t *testing.T) {
	// Adopting the resolved tenant would write to a row this machine does not follow.
	setup := env.New(t)
	server := platformAPIStubServer(t)
	platformAlias(t, setup, server)
	fixture.SeedHostedRuntimeTenantEnv(t, setup, "acme", "prod", server.URL, "tenant-other", "env-1", 1)

	result := erun.Run(t, []string{"platform", "env", "pull", "acme", "prod"},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("a tenant mismatch was adopted rather than refused:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "refusing to transfer the definition") {
		t.Fatalf("refusal did not say it refused the transfer:\n%s", result.Combined)
	}
}

func testPlatformEnvGetNotFound(t *testing.T) {
	setup := env.New(t)
	server := platformAPIStubServer(t)
	platformAlias(t, setup, server)
	result := erun.Run(t, []string{"platform", "env", "get", "missing"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for a missing environment, got:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "not found") {
		t.Fatalf("expected a not-found error, got:\n%s", result.Combined)
	}
}

func testPlatformEnvDeleteDryRunSkipsConfirmation(t *testing.T) {
	// The harness has no TTY to answer the confirm prompt.
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{"platform", "env", "delete", "env-1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "platform/env_delete_dry_run", normalize.Apply(result.Combined))
}

func rewriteEnvConfigLine(t testing.TB, path, from, to string) {
	t.Helper()
	content := mustReadFile(t, path)
	updated := strings.Replace(content, from, to, 1)
	if updated == content {
		t.Fatalf("env config %s has no line %q to rewrite:\n%s", path, from, content)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatalf("rewrite %s: %v", path, err)
	}
}
