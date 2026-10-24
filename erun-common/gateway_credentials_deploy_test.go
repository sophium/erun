package eruncommon

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

const testGatewayURL = "https://openrouter.ai/api"

func TestResolveGatewayCredentialTrimsWhatItDelivers(t *testing.T) {
	// A token pasted into a settings file or a dialog routinely carries a
	// trailing newline. Delivered raw it would be sent as part of the credential
	// and rejected by the gateway, so it is trimmed on the way out.
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
		// The key the chart reads, so the two cannot drift apart.
		config.GatewaySecretKey + ":",
		"sk-or-v1-abc",
		// The label every erun-applied Secret carries.
		"app.kubernetes.io/managed-by: erun-deploy",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("manifest does not contain %q:\n%s", want, manifest)
		}
	}
}

func TestApplyGatewayCredentialsSecretSkipsAnOptedOutEnvironment(t *testing.T) {
	// An environment that opted out renders no gateway env block, so it must get
	// no Secret either: a credential left in a namespace that asked not to have
	// one is not harmless, it is a credential where none was wanted.
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

func TestApplyGatewayCredentialsSecretDeliversForAnEnvironmentUsingIt(t *testing.T) {
	ctx, trace := traceCapturingContext()
	// The dry run stops before the store is read, which is the property that
	// matters: a dry run must not require a credential to exist.
	ctx.DryRun = true
	spec := HelmDeploySpec{
		ReleaseName: "erun-devops",
		Namespace:   "team-dev",
		OpenRouter:  &config.OpenRouterConfig{BaseURL: testGatewayURL},
	}
	if err := applyGatewayCredentialsSecret(ctx, spec); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(trace.String(), config.GatewaySecretName) {
		t.Fatalf("expected the gateway Secret in the trace:\n%s", trace.String())
	}
}

// TestAppliedSecretNamesMatchTheChartValues pins the one link that has to hold:
// the Secret deploy creates and the name the chart is told to read are the same
// constant, so a rename cannot leave the pod pointing at nothing.
func TestAppliedSecretNamesMatchTheChartValues(t *testing.T) {
	spec := HelmDeploySpec{
		ReleaseName: "erun-devops",
		Namespace:   "team-dev",
		OpenRouter:  &config.OpenRouterConfig{BaseURL: testGatewayURL},
	}
	args := helmClaudeSetArgs(config.EnvironmentClaudeConfig{}, spec.OpenRouter)
	set := strings.Join(args, " ")
	if !strings.Contains(set, "claude.openRouterAuthTokenSecret="+config.GatewaySecretName) {
		t.Fatalf("the chart is not told the Secret deploy creates:\n%s", set)
	}
	if !strings.Contains(set, "claude.openRouterAuthTokenKey="+config.GatewaySecretKey) {
		t.Fatalf("the chart is not told the entry that Secret carries:\n%s", set)
	}
}

// TestHelmClaudeSetArgsGatewayRendersProviderFlagsOff pins that a gateway
// replaces the model provider: Claude Code reads CLAUDE_CODE_USE_BEDROCK and
// _MANTLE ahead of ANTHROPIC_BASE_URL, so an environment that selected the
// gateway with either still on would be routed past it. One that opted out of
// the gateway keeps its own provider choice.
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

// TestPruneGatewayCredentialsSecretRemovesItWhenTheGatewayIsOff is the second
// half of switching the gateway off: before this, deploy only ever applied the
// credential, so a namespace kept it after the environment left the gateway.
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

func TestPruneGatewayCredentialsSecretKeepsItWhileTheGatewayApplies(t *testing.T) {
	calls := stubGatewaySecretDelete(t)
	spec := HelmDeploySpec{ReleaseName: "erun-devops", Tenant: "erun", Namespace: "erun-build", OpenRouter: &config.OpenRouterConfig{BaseURL: testGatewayURL}}
	if err := pruneGatewayCredentialsSecret(core.Context{}, spec); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("deleted the credential an environment on the gateway reads: %v", *calls)
	}
}

// A component release shares the namespace but does not carry the env's Claude
// configuration, so it must not decide the runtime's credential away.
func TestPruneGatewayCredentialsSecretLeavesItToTheRuntimeRelease(t *testing.T) {
	calls := stubGatewaySecretDelete(t)
	spec := HelmDeploySpec{ReleaseName: "erun-backend-postgres", Tenant: "erun", Namespace: "erun-build"}
	if err := pruneGatewayCredentialsSecret(core.Context{}, spec); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("a component release pruned the runtime's credential: %v", *calls)
	}
}

func TestPruneGatewayCredentialsSecretDryRunOnlyTraces(t *testing.T) {
	calls := stubGatewaySecretDelete(t)
	ctx, trace := traceCapturingContext()
	ctx.DryRun = true
	spec := HelmDeploySpec{ReleaseName: "erun-devops", Tenant: "erun", Namespace: "erun-build"}
	if err := pruneGatewayCredentialsSecret(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 || !strings.Contains(trace.String(), config.GatewaySecretName) {
		t.Fatalf("calls=%v trace:\n%s", *calls, trace.String())
	}
}
