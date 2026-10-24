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
	// One scenario drives register -> list -> get -> deploy -> stop ->
	// delete against the real stub server, covering every environment
	// PlatformClient method's request/response handling in one pass.
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
	// The dry run resolves the local marker and traces which row it names
	// and which portable fields would travel, without reaching the network
	// or writing anything. The fields list is the allowlist itself, so a
	// host-owned value appearing here would be the leak this plan forbids.
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
	// The pull's dry run traces the two reads it would make and states the
	// boundary it will hold: only portable fields are written.
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
	// The round trip the issue names, driven through the compiled binary
	// against the API double: an upload, then a pull, showing the portable
	// fields arrive while every host-owned value stays exactly as this
	// machine had it.
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

	// Edit the portable value locally, then pull the platform's copy back.
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
		// A key this binary cannot model at all: a pull preserves it
		// because the writer only ever touches keys EnvConfig declares.
		"futureunmodelledkey: keep-me",
	} {
		if !strings.Contains(restored, hostOwned) {
			t.Errorf("pull dropped the host-owned value %q:\n%s", hostOwned, restored)
		}
	}
}

func testPlatformEnvPullRefusesAPlatformNameMismatch(t *testing.T) {
	// A platform row whose name is not the local directory being pulled
	// into. The stub's row is named "prod"; this environment is "dev".
	// Writing it would create a second environment rather than update this
	// one, so the pull must refuse rather than rename.
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
	// The marker names tenant-other; the platform resolves tenant-1.
	// Adopting the resolved identity instead of refusing would write the
	// definition to a row this machine does not think it is following.
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
	// --dry-run must never block on the interactive confirm prompt: the
	// harness has no TTY to answer it.
	setup := env.New(t)
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{"platform", "env", "delete", "env-1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "platform/env_delete_dry_run", normalize.Apply(result.Combined))
}

// rewriteEnvConfigLine replaces one line of a seeded env config in place, so a
// scenario can move a portable value locally and prove the next pull brings the
// platform's copy back without touching anything else in the file.
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
