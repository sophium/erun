package deploy

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

const testGatewayURL = "https://openrouter.ai/api"

func TestResolveGatewayCredentialTrimsWhatItDelivers(t *testing.T) {
	// A pasted token's trailing newline would be rejected by the gateway.
	store := cloud.NewFileCloudSecretStore(t.TempDir())
	if err := store.SaveCloudSecret("claude-gateway", "  sk-or-v1-abc\n"); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := ResolveGatewayCredentialFromStore(store, "claude-gateway", testGatewayURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sk-or-v1-abc" {
		t.Fatalf("credential = %q, want the trimmed token", got)
	}
}

func TestRenderGatewayCredentialsSecret(t *testing.T) {
	manifest := renderGatewayCredentialsSecret(config.GatewaySecretName, "team-dev", "sk-or-v1-abc")
	for _, want := range []string{
		"namespace: team-dev",
		"name: " + config.GatewaySecretName,
		config.GatewaySecretKey + ":",
		"sk-or-v1-abc",
		"app.kubernetes.io/managed-by: erun-deploy",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest does not contain %q:\n%s", want, manifest)
		}
	}
}

func TestApplyGatewayCredentialsSecretSkipsAnOptedOutEnvironment(t *testing.T) {
	ctx, trace := traceCapturingContext()
	ctx.DryRun = true
	no := false
	spec := HelmDeploySpec{
		ReleaseName: "erun-devops",
		Namespace:   "team-dev",
		Claude:      config.EnvironmentClaudeConfig{UseGateway: &no},
		OpenRouter:  &config.OpenRouterConfig{BaseURL: testGatewayURL},
	}
	if err := applyGatewayCredentialsSecret(ctx, spec); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(trace.String(), config.GatewaySecretName) {
		t.Fatalf("an opted-out environment must get no gateway Secret, trace:\n%s", trace.String())
	}
}

// Claude Code reads CLAUDE_CODE_USE_BEDROCK and _MANTLE ahead of ANTHROPIC_BASE_URL.
func TestHelmClaudeSetArgsGatewayRendersProviderFlagsOff(t *testing.T) {
	on, off := true, false
	catalog := &config.OpenRouterConfig{BaseURL: testGatewayURL}
	providers := config.EnvironmentClaudeConfig{UseBedrock: &on, UseMantle: &on}

	set := strings.Join(helmClaudeSetArgs(providers, catalog), " ")
	for _, want := range []string{"claude.useBedrock=0", "claude.useMantle=0", "claude.openRouterBaseURL="} {
		if !strings.Contains(set, want) {
			t.Fatalf("a gateway-routed environment should render %s:\n%s", want, set)
		}
	}

	optedOut := providers
	optedOut.UseGateway = &off
	set = strings.Join(helmClaudeSetArgs(optedOut, catalog), " ")
	for _, want := range []string{"claude.useBedrock=1", "claude.useMantle=1"} {
		if !strings.Contains(set, want) {
			t.Fatalf("an environment that left the gateway should keep its provider choice %s:\n%s", want, set)
		}
	}
	if strings.Contains(set, "claude.openRouterBaseURL=") {
		t.Fatalf("an environment that left the gateway must not render it:\n%s", set)
	}
}

func stubGatewaySecretDelete(t *testing.T) *[][]string {
	t.Helper()
	var calls [][]string
	previous := deleteGatewayCredentialsSecret
	deleteGatewayCredentialsSecret = func(args []string) error {
		calls = append(calls, args)
		return nil
	}
	t.Cleanup(func() { deleteGatewayCredentialsSecret = previous })
	return &calls
}

func TestPruneGatewayCredentialsSecretRemovesItWhenTheGatewayIsOff(t *testing.T) {
	no := false
	for name, spec := range map[string]HelmDeploySpec{
		"catalog switched off": {OpenRouter: &config.OpenRouterConfig{Models: []config.OpenRouterModel{{ID: "deepseek/deepseek-v4.1-flash"}}}},
		"no catalog":           {},
		"environment opted out": {
			Claude:     config.EnvironmentClaudeConfig{UseGateway: &no},
			OpenRouter: &config.OpenRouterConfig{BaseURL: testGatewayURL},
		},
	} {
		t.Run(name, func(t *testing.T) {
			calls := stubGatewaySecretDelete(t)
			spec.ReleaseName, spec.Tenant, spec.Namespace, spec.KubernetesContext = "erun-devops", "erun", "erun-build", "erun-build"
			if err := pruneGatewayCredentialsSecret(core.Context{}, spec); err != nil {
				t.Fatal(err)
			}
			want := "--context erun-build -n erun-build delete secret " + config.GatewaySecretName + " --ignore-not-found"
			if len(*calls) != 1 || strings.Join((*calls)[0], " ") != want {
				t.Fatalf("calls = %v, want %q", *calls, want)
			}
		})
	}
}
