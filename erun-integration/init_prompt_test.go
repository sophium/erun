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
	// Exercises the container-registry prompt body itself, so it omits
	// --container-registry and answers the prompt over stdin.
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
	// A bare Enter at the container-registry prompt must submit the prompt's
	// default; this empty-input fallback only exists inside the prompt.
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
	// Exercises the kubernetes-context select prompt. preferCurrentKubernetesContext
	// moves the current context (ctx-two) to the front, so confirming the
	// highlighted first item selects it, not the alphabetical first.
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
	// The kubectl stub fails the context listing so init falls to the
	// manual-entry prompt. That listing-failure path is the only way to
	// reach the manual prompt here: choosing manual entry inside the select
	// would need a second promptui prompt, which readline's read-ahead makes
	// unreachable over piped stdin.
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
	// Bare Enter at the default-tenant confirm exercises the default-yes
	// branch. Only one promptui prompt survives per subprocess (readline
	// read-ahead), so the environment confirm is bypassed via its flag.
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
	// Typing "y" at the environment confirm exercises the typed-yes branch;
	// the tenant confirm is bypassed via --set-default-tenant to keep the
	// run at one prompt.
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
	// Declining the environment confirm must abort before the env config
	// is created.
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

// stubKubectlContexts feeds init's kubernetes-context selection deterministic
// input so the rendered select list stays stable across machines.
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
