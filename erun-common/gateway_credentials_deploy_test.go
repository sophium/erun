package eruncommon

import (
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

const testGatewayURL = "https://openrouter.ai/api"

// failingSecretStore stands in for a store that cannot be read at all, which is
// the case the resolver must not paper over with the host key.
type failingSecretStore struct{ err error }

func (s failingSecretStore) SaveCloudSecret(string, string) error { return nil }
func (s failingSecretStore) LoadCloudSecret(string) (string, error) {
	return "", s.err
}
func (s failingSecretStore) DeleteCloudSecret(string) error { return nil }

// emptySecretStore is a store holding nothing, the ordinary "not saved yet" case.
func emptySecretStore(t *testing.T) cloud.CloudSecretStore {
	t.Helper()
	return cloud.NewFileCloudSecretStore(t.TempDir())
}

func TestResolveGatewayCredentialPrefersTheSavedValue(t *testing.T) {
	// The operator's explicit choice wins over the machine's own key: that is
	// what makes saving one meaningful. The host here is on the same gateway, so
	// the fallback would otherwise answer — this pins which one wins.
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_BASE_URL":"`+testGatewayURL+`","ANTHROPIC_AUTH_TOKEN":"from-host"}}`)
	store := cloud.NewFileCloudSecretStore(t.TempDir())
	if err := store.SaveCloudSecret("claude-gateway", "saved-by-operator"); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := resolveGatewayCredential(store, "claude-gateway", testGatewayURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "saved-by-operator" {
		t.Fatalf("credential = %q, want the saved value", got)
	}
}

func TestResolveGatewayCredentialTrimsWhatItDelivers(t *testing.T) {
	// A token pasted into a settings file or a dialog routinely carries a
	// trailing newline. Delivered raw it would be sent as part of the credential
	// and rejected by the gateway, so it is trimmed on the way out.
	store := cloud.NewFileCloudSecretStore(t.TempDir())
	if err := store.SaveCloudSecret("claude-gateway", "  sk-or-v1-abc\n"); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := resolveGatewayCredential(store, "claude-gateway", testGatewayURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sk-or-v1-abc" {
		t.Fatalf("credential = %q, want the trimmed token", got)
	}
}

func TestResolveGatewayCredentialReusesTheHostKeyForTheSameGateway(t *testing.T) {
	// The case the fallback exists for: an operator already running Claude Code
	// through this gateway has the key on this machine, so nothing saved yet
	// means the host key, not a failure.
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_BASE_URL":"`+testGatewayURL+`","ANTHROPIC_AUTH_TOKEN":"from-host"}}`)
	got, err := resolveGatewayCredential(emptySecretStore(t), "claude-gateway", testGatewayURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from-host" {
		t.Fatalf("credential = %q, want the host key", got)
	}
}

// TestResolveGatewayCredentialNeverSendsAHostKeyToAnotherGateway is the property
// that matters most here. A host key is scoped to the endpoint it was issued
// for, so reusing it against a different gateway would hand one service's
// credential to another. An operator authenticating directly against Anthropic
// carries an ANTHROPIC_API_KEY in these same settings, and delivering it as a
// gateway's bearer token would disclose it to that gateway.
func TestResolveGatewayCredentialNeverSendsAHostKeyToAnotherGateway(t *testing.T) {
	cases := []struct {
		name string
		env  string
	}{
		{
			name: "a direct Anthropic key with no gateway configured",
			env:  `{"env":{"ANTHROPIC_API_KEY":"sk-ant-api03-secret"}}`,
		},
		{
			name: "a bearer token for a different gateway",
			env:  `{"env":{"ANTHROPIC_BASE_URL":"https://gateway.example.com/anthropic","ANTHROPIC_AUTH_TOKEN":"other-gateway-token"}}`,
		},
		{
			name: "a key whose endpoint differs only by path",
			env:  `{"env":{"ANTHROPIC_BASE_URL":"` + testGatewayURL + `/v2","ANTHROPIC_AUTH_TOKEN":"wrong-path-token"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubClaudeSettings(t, tc.env)
			got, err := resolveGatewayCredential(emptySecretStore(t), "claude-gateway", testGatewayURL)
			if err == nil {
				t.Fatalf("a key for another endpoint must not be delivered, got %q", got)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "token") {
				// The value itself must never be quoted back in an error.
				for _, leak := range []string{"sk-ant-api03-secret", "other-gateway-token", "wrong-path-token"} {
					if strings.Contains(err.Error(), leak) {
						t.Fatalf("the credential leaked into the error: %v", err)
					}
				}
			}
			// The refusal says which refusal it is. A host that carries a key for
			// another gateway is told so, by endpoint; a host authenticating
			// directly is told that. Neither may read as "no key exists", which
			// would contradict what the operator sees in their own settings.
			if !strings.Contains(err.Error(), "Claude Code") {
				t.Fatalf("the refusal does not mention the host's own key: %v", err)
			}
			if strings.Contains(err.Error(), "settings carry none") {
				t.Fatalf("a host holding a key must not be told it holds none: %v", err)
			}
		})
	}
}

func TestResolveGatewayCredentialExplainsADirectAnthropicHost(t *testing.T) {
	// The nastiest version of the mismatch: the host has a perfectly good key
	// and no gateway at all, so there is no other endpoint to name. The refusal
	// has to say that, because otherwise the operator reads "no credential" over
	// a settings file that plainly has one.
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_API_KEY":"sk-ant-api03-secret"}}`)
	_, err := resolveGatewayCredential(emptySecretStore(t), "claude-gateway", testGatewayURL)
	if err == nil {
		t.Fatal("a direct-Anthropic key must not be delivered to a gateway")
	}
	if !strings.Contains(err.Error(), "authenticates directly") {
		t.Fatalf("the refusal does not explain the host's own setup: %v", err)
	}
}

func TestResolveGatewayCredentialNamesBothSourcesWhenNeitherExists(t *testing.T) {
	stubClaudeSettings(t, "")
	_, err := resolveGatewayCredential(emptySecretStore(t), "claude-gateway", testGatewayURL)
	if err == nil {
		t.Fatal("no credential anywhere must be an error, not an empty Secret")
	}
	// Both places are named, because an operator can act on either and does not
	// know which one the resolver looked in first.
	for _, want := range []string{"claude-gateway", "Claude Code"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
}

func TestResolveGatewayCredentialReportsAnUnreadableStore(t *testing.T) {
	// A store that cannot be read might hold a value that should win, so this
	// stops rather than silently delivering a different credential than the one
	// the operator saved.
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_BASE_URL":"`+testGatewayURL+`","ANTHROPIC_AUTH_TOKEN":"from-host"}}`)
	want := errors.New("permission denied")
	_, err := resolveGatewayCredential(failingSecretStore{err: want}, "claude-gateway", testGatewayURL)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want it to wrap %v", err, want)
	}
	if strings.Contains(err.Error(), "from-host") {
		t.Fatalf("the host key was delivered despite the fault: %v", err)
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
