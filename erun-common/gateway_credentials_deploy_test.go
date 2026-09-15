package eruncommon

import (
	"errors"
	"strings"
	"testing"
)

// failingSecretStore stands in for a store that cannot be read at all, which is
// the case the resolver must not paper over with the host key.
type failingSecretStore struct{ err error }

func (s failingSecretStore) SaveCloudSecret(string, string) error { return nil }
func (s failingSecretStore) LoadCloudSecret(string) (string, error) {
	return "", s.err
}
func (s failingSecretStore) DeleteCloudSecret(string) error { return nil }

func TestResolveGatewayCredentialPrefersTheSavedValue(t *testing.T) {
	// The operator's explicit choice wins over the machine's own key: that is
	// what makes saving one meaningful.
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_AUTH_TOKEN":"from-host"}}`)
	store := NewFileCloudSecretStore(t.TempDir())
	if err := store.SaveCloudSecret("claude-gateway", "saved-by-operator"); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := resolveGatewayCredential(store, "claude-gateway")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "saved-by-operator" {
		t.Fatalf("credential = %q, want the saved value", got)
	}
}

func TestResolveGatewayCredentialFallsBackToTheHostKey(t *testing.T) {
	// The catalog has to work the moment it is configured: an operator already
	// running Claude Code against a gateway has the key on this machine, so
	// nothing saved yet means the host key, not a failure.
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_AUTH_TOKEN":"from-host"}}`)
	got, err := resolveGatewayCredential(NewFileCloudSecretStore(t.TempDir()), "claude-gateway")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from-host" {
		t.Fatalf("credential = %q, want the host key", got)
	}
}

func TestResolveGatewayCredentialNamesBothSourcesWhenNeitherExists(t *testing.T) {
	stubClaudeSettings(t, "")
	_, err := resolveGatewayCredential(NewFileCloudSecretStore(t.TempDir()), "claude-gateway")
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
	stubClaudeSettings(t, `{"env":{"ANTHROPIC_AUTH_TOKEN":"from-host"}}`)
	want := errors.New("permission denied")
	_, err := resolveGatewayCredential(failingSecretStore{err: want}, "claude-gateway")
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want it to wrap %v", err, want)
	}
	if strings.Contains(err.Error(), "from-host") {
		t.Fatalf("the host key was delivered despite the fault: %v", err)
	}
}

func TestRenderGatewayCredentialsSecret(t *testing.T) {
	manifest := renderGatewayCredentialsSecret(GatewaySecretName, "team-dev", "sk-or-v1-abc")
	for _, want := range []string{
		"namespace: team-dev",
		"name: " + GatewaySecretName,
		// The key the chart reads, so the two cannot drift apart.
		GatewaySecretKey + ":",
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
		Claude:      EnvironmentClaudeConfig{UseGateway: &no},
		OpenRouter:  &OpenRouterConfig{BaseURL: "https://openrouter.ai/api"},
	}
	if err := applyGatewayCredentialsSecret(ctx, spec); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(trace.String(), GatewaySecretName) {
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
		OpenRouter:  &OpenRouterConfig{BaseURL: "https://openrouter.ai/api"},
	}
	if err := applyGatewayCredentialsSecret(ctx, spec); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(trace.String(), GatewaySecretName) {
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
		OpenRouter:  &OpenRouterConfig{BaseURL: "https://openrouter.ai/api"},
	}
	args := helmClaudeSetArgs(EnvironmentClaudeConfig{}, spec.OpenRouter)
	set := strings.Join(args, " ")
	if !strings.Contains(set, "claude.openRouterAuthTokenSecret="+GatewaySecretName) {
		t.Fatalf("the chart is not told the Secret deploy creates:\n%s", set)
	}
	if !strings.Contains(set, "claude.openRouterAuthTokenKey="+GatewaySecretKey) {
		t.Fatalf("the chart is not told the entry that Secret carries:\n%s", set)
	}
}
