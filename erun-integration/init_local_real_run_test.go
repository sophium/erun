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

func initLocalRealRunScenarios(t *testing.T) {
	t.Run("local_real_run_persists_project_registry", testInitLocalRealRunPersistsProjectRegistry)
	t.Run("local_real_run_drops_redundant_registry_override", testInitLocalRealRunDropsRedundantRegistryOverride)
	t.Run("real_run_via_stubs", testInitRealRunViaStubs)
}

func testInitLocalRealRunPersistsProjectRegistry(t *testing.T) {
	// Real-run local-agent init: a --container-registry that differs from the
	// project's base must persist as a per-environment override, and the k8s
	// deploy plan must round-trip in both its scalar (single step) and
	// sequence (parallel group) shapes — round-trips dry-run init never
	// reaches. A pre-existing ~/.claude/settings.json with custom permissions
	// but no bypass mode drives the merge: the custom allow-list must survive
	// while bypass mode is stamped in.
	setup := env.New(t)
	claudeDir := filepath.Join(setup.Home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatalf("mkdir ~/.claude: %v", err)
	}
	mustWrite(t, filepath.Join(claudeDir, "settings.json"),
		`{"permissions":{"allow":["Bash(ls:*)"]}}`)
	fixture.SeedProjectK8sConfig(t, setup,
		"containerregistry: registry.example/base\n"+
			"environments:\n"+
			"  local:\n"+
			"    k8s:\n"+
			"      deployments:\n"+
			"        - app\n"+
			"        - [api, worker]\n",
	)
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	args := []string{
		"init", "team", "local",
		"--type", "local-agent",
		"--project-root", setup.Cwd,
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/custom",
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "init/local_real_run_persists_project_registry", normalize.Apply(result.Combined))
	// The rewritten project config is a side effect outside the captured
	// streams, so assert on the file directly.
	raw, err := os.ReadFile(filepath.Join(setup.Cwd, ".erun", "config.yaml"))
	if err != nil {
		t.Fatalf("read project config: %v", err)
	}
	body := string(raw)
	for _, want := range []string{
		"registry: registry.example/base",
		"registry.example/custom",
		"- app",
		"api",
		"worker",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected project config to contain %q, got:\n%s", want, body)
		}
	}
	claudeRaw, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		t.Fatalf("read ~/.claude/settings.json: %v", err)
	}
	// JSON re-indentation makes exact-substring asserts brittle; strip
	// all whitespace before matching the merged keys.
	flattened := strings.Join(strings.Fields(string(claudeRaw)), "")
	for _, want := range []string{
		`"Bash(ls:*)"`,
		`"defaultMode":"bypassPermissions"`,
		`"skipDangerousModePermissionPrompt":true`,
	} {
		if !strings.Contains(flattened, want) {
			t.Errorf("expected claude settings to contain %q, got:\n%s", want, claudeRaw)
		}
	}
}

func testInitLocalRealRunDropsRedundantRegistryOverride(t *testing.T) {
	// Re-initializing an env whose per-environment registry override equals
	// the project-wide base must drop the now-redundant override. Production
	// deletes the whole map entry, so it would also wipe a co-located
	// k8s/docker block if one were present. A pre-seeded fully-configured
	// ~/.claude/settings.json must take EnsureClaudeSettings' already-configured
	// early return and stay byte-identical.
	setup := env.New(t)
	claudeDir := filepath.Join(setup.Home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatalf("mkdir ~/.claude: %v", err)
	}
	const configuredClaudeSettings = `{"permissions":{"defaultMode":"bypassPermissions"},"skipDangerousModePermissionPrompt":true}`
	mustWrite(t, filepath.Join(claudeDir, "settings.json"), configuredClaudeSettings)
	fixture.SeedProjectK8sConfig(t, setup,
		"containerregistry: registry.example/base\n"+
			"environments:\n"+
			"  local:\n"+
			"    containerregistry: registry.example/custom\n",
	)
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	args := []string{
		"init", "team", "local",
		"--type", "local-agent",
		"--project-root", setup.Cwd,
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/base",
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	raw, err := os.ReadFile(filepath.Join(setup.Cwd, ".erun", "config.yaml"))
	if err != nil {
		t.Fatalf("read project config: %v", err)
	}
	body := string(raw)
	if strings.Contains(body, "registry.example/custom") {
		t.Errorf("expected the per-environment registry override to be removed, got:\n%s", body)
	}
	if !strings.Contains(body, "registry: registry.example/base") {
		t.Errorf("expected the base registry to survive, got:\n%s", body)
	}
	claudeRaw, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		t.Fatalf("read ~/.claude/settings.json: %v", err)
	}
	if string(claudeRaw) != configuredClaudeSettings {
		t.Errorf("expected already-configured claude settings to be left untouched, got:\n%s", claudeRaw)
	}
}

func testInitRealRunViaStubs(t *testing.T) {
	// A real run (through stubs) covers the kubectl namespace check/create
	// and helm-runner code that dry-run traces but never executes.
	setup := env.New(t)
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	fixture.StubBinary(t, stubs, "git", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker", "git")...)
	args := []string{
		"init", "team", "dev",
		"--remote",
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--no-git",
		"--bootstrap",
		"--set-default-tenant=true",
		"--confirm-environment=true",
	}
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	golden.Equal(t, "init/real_run_via_stubs", normalize.Apply(result.Combined))
}
