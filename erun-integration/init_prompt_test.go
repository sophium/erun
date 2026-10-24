package integration

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func initPromptScenarios(t *testing.T) {
	t.Run("prompts_for_container_registry_via_stdin", testInitPromptsForContainerRegistryViaStdin)
	t.Run("container_registry_empty_submit_uses_default", testInitContainerRegistryEmptySubmitUsesDefault)
	t.Run("selects_kubernetes_context_via_stdin", testInitSelectsKubernetesContextViaStdin)
	t.Run("enters_kubernetes_context_manually_via_stdin", testInitEntersKubernetesContextManuallyViaStdin)
	t.Run("prompts_default_tenant_confirm_via_stdin", testInitPromptsDefaultTenantConfirmViaStdin)
	t.Run("prompts_environment_confirm_via_stdin", testInitPromptsEnvironmentConfirmViaStdin)
	t.Run("environment_confirm_declined_via_stdin", testInitEnvironmentConfirmDeclinedViaStdin)
}

func testInitPromptsForContainerRegistryViaStdin(t *testing.T) {
	setup := env.New(t)
	args := []string{
		"init", "team", "local",
		"--type", "local-agent",
		"--project-root", setup.Cwd,
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   setup.Env(),
		Stdin: "registry.example/prompted\n",
	})
	golden.Equal(t, "init/prompts_for_container_registry_via_stdin", normalize.Apply(result.Combined))
}

func testInitContainerRegistryEmptySubmitUsesDefault(t *testing.T) {
	setup := env.New(t)
	args := []string{
		"init", "team", "local",
		"--type", "local-agent",
		"--project-root", setup.Cwd,
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   setup.Env(),
		Stdin: "\n",
	})
	golden.Equal(t, "init/container_registry_empty_submit_uses_default", normalize.Apply(result.Combined))
}

func testInitSelectsKubernetesContextViaStdin(t *testing.T) {
	// The current context (ctx-two) is moved to the front, so Enter selects it.
	setup := env.New(t)
	stubs := setup.Cwd + "/stubs"
	stubKubectlContexts(t, stubs, []string{"ctx-one", "ctx-two"}, "ctx-two")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	args := []string{
		"init", "team", "local",
		"--type", "local-agent",
		"--project-root", setup.Cwd,
		"--version", "1.0.0",
		"--container-registry", "registry.example/test",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   envVars,
		Stdin: "\r",
	})
	golden.Equal(t, "init/selects_kubernetes_context_via_stdin", normalize.Apply(result.Combined))
}

func testInitEntersKubernetesContextManuallyViaStdin(t *testing.T) {
	// Readline read-ahead makes a second prompt unreachable over piped stdin, so a failed listing reaches manual entry.
	setup := env.New(t)
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{
		Stderr:   "error: unable to read kubeconfig",
		ExitCode: 1,
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	args := []string{
		"init", "team", "local",
		"--type", "local-agent",
		"--project-root", setup.Cwd,
		"--version", "1.0.0",
		"--container-registry", "registry.example/test",
		"--set-default-tenant=true",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   envVars,
		Stdin: "manual-ctx\n",
	})
	golden.Equal(t, "init/enters_kubernetes_context_manually_via_stdin", normalize.Apply(result.Combined))
}

func testInitPromptsDefaultTenantConfirmViaStdin(t *testing.T) {
	// Only one promptui prompt survives per subprocess (readline read-ahead).
	setup := env.New(t)
	args := []string{
		"init", "team", "local",
		"--type", "local-agent",
		"--project-root", setup.Cwd,
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--confirm-environment=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   setup.Env(),
		Stdin: "\n",
	})
	golden.Equal(t, "init/prompts_default_tenant_confirm_via_stdin", normalize.Apply(result.Combined))
}

func testInitPromptsEnvironmentConfirmViaStdin(t *testing.T) {
	setup := env.New(t)
	args := []string{
		"init", "team", "local",
		"--type", "local-agent",
		"--project-root", setup.Cwd,
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--set-default-tenant=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   setup.Env(),
		Stdin: "y\n",
	})
	golden.Equal(t, "init/prompts_environment_confirm_via_stdin", normalize.Apply(result.Combined))
}

func testInitEnvironmentConfirmDeclinedViaStdin(t *testing.T) {
	setup := env.New(t)
	args := []string{
		"init", "team", "local",
		"--type", "local-agent",
		"--project-root", setup.Cwd,
		"--version", "1.0.0",
		"--kubernetes-context", "test-context",
		"--container-registry", "registry.example/test",
		"--set-default-tenant=true",
		"--dry-run",
	}
	result := erun.Run(t, args, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   setup.Env(),
		Stdin: "n\n",
	})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "init/environment_confirm_declined_via_stdin", normalize.Apply(result.Combined))
}

func stubKubectlContexts(t *testing.T, dir string, contexts []string, current string) {
	t.Helper()
	script := strings.Join([]string{
		`case "$*" in`,
		`  *current-context*) printf '%s\n' '` + current + `' ;;`,
		`  *get-contexts*) printf '%s\n' ` + shellQuotedLines(contexts) + ` ;;`,
		`  *) : ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, dir, "kubectl", script)
}

func shellQuotedLines(lines []string) string {
	quoted := make([]string, 0, len(lines))
	for _, line := range lines {
		quoted = append(quoted, "'"+line+"'")
	}
	return strings.Join(quoted, " ")
}
