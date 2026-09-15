package eruncommon

import (
	"errors"
	"strings"
	"testing"
)

func TestOpenRouterConfigNilSafety(t *testing.T) {
	// Nil keeps an unconfigured install on exactly its existing behaviour, so
	// every accessor answers safely rather than panicking.
	var c *OpenRouterConfig
	if c.Configured() {
		t.Fatal("nil catalog must not report configured")
	}
	if got := c.ModelIDs(); got != nil {
		t.Fatalf("nil catalog ModelIDs = %v, want nil", got)
	}
	if got := c.ContextFor("anything"); got != 0 {
		t.Fatalf("nil catalog ContextFor = %d, want 0", got)
	}
	if c.HasModel("anything") {
		t.Fatal("nil catalog must not report a model")
	}
	if got := c.ResolveDefaultModel(); got != "" {
		t.Fatalf("nil catalog ResolveDefaultModel = %q, want empty", got)
	}
	if got := c.GatewayEnvVars(); got != nil {
		t.Fatalf("nil catalog GatewayEnvVars = %v, want nil", got)
	}
}

func TestOpenRouterConfigConfigured(t *testing.T) {
	if (&OpenRouterConfig{Models: []OpenRouterModel{{ID: "x"}}}).Configured() {
		t.Fatal("a catalog with models but no base URL must not report configured")
	}
	if !(&OpenRouterConfig{BaseURL: "  https://openrouter.ai/api  "}).Configured() {
		t.Fatal("a whitespace-padded base URL must still report configured")
	}
}

func TestOpenRouterConfigAuthTokenSecretName(t *testing.T) {
	// One erun-level catalog means one Secret name for every environment, so a
	// catalog that names none still has a working name rather than leaving the
	// operator to invent one.
	defaulted := &OpenRouterConfig{BaseURL: "https://x"}
	if got := defaulted.AuthTokenSecretName(); got != DefaultOpenRouterAuthTokenSecret {
		t.Fatalf("unnamed Secret = %q, want the default", got)
	}

	// An operator who already manages the credential under their own name keeps
	// using it.
	named := &OpenRouterConfig{BaseURL: "https://x", AuthTokenSecret: "  my-existing-secret  "}
	if got := named.AuthTokenSecretName(); got != "my-existing-secret" {
		t.Fatalf("named Secret = %q, want the trimmed name", got)
	}

	// A catalog that names no gateway resolves to no Secret at all, so "no
	// gateway" stays distinguishable from "gateway with the default Secret".
	var unconfigured *OpenRouterConfig
	if got := unconfigured.AuthTokenSecretName(); got != "" {
		t.Fatalf("unconfigured Secret = %q, want empty", got)
	}
	if got := (&OpenRouterConfig{}).AuthTokenSecretName(); got != "" {
		t.Fatalf("catalog without a base URL Secret = %q, want empty", got)
	}
}

func TestOpenRouterConfigModelIDs(t *testing.T) {
	c := &OpenRouterConfig{BaseURL: "https://x", Models: []OpenRouterModel{
		{ID: "b"}, {ID: "  "}, {ID: "a"}, {ID: "b"}, {ID: " c "},
	}}
	if got := strings.Join(c.ModelIDs(), ","); got != "b,a,c" {
		t.Fatalf("ModelIDs = %q, want %q", got, "b,a,c")
	}
}

func TestOpenRouterConfigContextFor(t *testing.T) {
	c := &OpenRouterConfig{BaseURL: "https://x", Models: []OpenRouterModel{
		{ID: "a", Context: 1048576},
		{ID: "b"},
	}}
	if got := c.ContextFor("a"); got != 1048576 {
		t.Fatalf("ContextFor(a) = %d, want 1048576", got)
	}
	// An entry with no declared window is distinct from an unknown model: both
	// report 0, and the caller decides whether to fall back.
	for _, id := range []string{"b", "zzz"} {
		if got := c.ContextFor(id); got != 0 {
			t.Fatalf("ContextFor(%s) = %d, want 0", id, got)
		}
	}
}

func TestOpenRouterConfigResolveDefaultModel(t *testing.T) {
	c := &OpenRouterConfig{BaseURL: "https://x", DefaultModel: "b", Models: []OpenRouterModel{{ID: "a"}, {ID: "b"}}}
	if got := c.ResolveDefaultModel(); got != "b" {
		t.Fatalf("ResolveDefaultModel = %q, want b", got)
	}
	// A default naming no catalog entry must not route at an unlisted model.
	c.DefaultModel = "gone"
	if got := c.ResolveDefaultModel(); got != "a" {
		t.Fatalf("stale default ResolveDefaultModel = %q, want a", got)
	}
	c.Models = nil
	if got := c.ResolveDefaultModel(); got != "" {
		t.Fatalf("empty catalog ResolveDefaultModel = %q, want empty", got)
	}
}

func TestOpenRouterConfigGatewayEnvVars(t *testing.T) {
	c := &OpenRouterConfig{BaseURL: "  https://openrouter.ai/api  ", AuthTokenSecret: "erun-claude-gateway"}
	env := c.GatewayEnvVars()
	if env["ANTHROPIC_BASE_URL"] != "https://openrouter.ai/api" {
		t.Fatalf("base URL = %q, want the trimmed value", env["ANTHROPIC_BASE_URL"])
	}
	// Present-but-empty, not absent: an unset API key lets Claude Code fall back
	// to a direct provider and bypass the gateway.
	if value, ok := env["ANTHROPIC_API_KEY"]; !ok || value != "" {
		t.Fatalf("ANTHROPIC_API_KEY must be present and empty, got %q (present=%v)", value, ok)
	}
	for key, value := range env {
		if strings.Contains(key, "TOKEN") || strings.Contains(value, "erun-claude-gateway") {
			t.Fatalf("a credential or its reference leaked into the env block: %s=%q", key, value)
		}
	}
}

type stubRootConfigStore struct {
	config ERunConfig
	err    error
}

func (s stubRootConfigStore) LoadERunConfig() (ERunConfig, string, error) {
	return s.config, "/tmp/config.yaml", s.err
}

func TestResolveOpenRouterConfig(t *testing.T) {
	gateway := &OpenRouterConfig{BaseURL: "https://openrouter.ai/api"}

	t.Run("nil store yields no gateway", func(t *testing.T) {
		got, err := ResolveOpenRouterConfig(nil)
		if err != nil || got != nil {
			t.Fatalf("ResolveOpenRouterConfig(nil) = (%v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("an unconfigured install yields no gateway", func(t *testing.T) {
		got, err := ResolveOpenRouterConfig(stubRootConfigStore{})
		if err != nil || got != nil {
			t.Fatalf("unconfigured = (%v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("a configured install yields the catalog", func(t *testing.T) {
		got, err := ResolveOpenRouterConfig(stubRootConfigStore{config: ERunConfig{OpenRouter: gateway}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != gateway {
			t.Fatalf("got %v, want the configured catalog", got)
		}
	})

	t.Run("an unreadable root config is reported, not swallowed", func(t *testing.T) {
		want := errors.New("permission denied")
		if _, err := ResolveOpenRouterConfig(stubRootConfigStore{err: want}); !errors.Is(err, want) {
			t.Fatalf("err = %v, want %v", err, want)
		}
	})
}

// gatewayFixture is the catalog the gateway launch contracts share.
func gatewayFixture() *OpenRouterConfig {
	return &OpenRouterConfig{
		BaseURL:         "https://openrouter.ai/api",
		AuthTokenSecret: "erun-claude-gateway",
		Models: []OpenRouterModel{
			{ID: "deepseek/deepseek-v4.1-flash", Context: 1048576},
			{ID: "openai/gpt-6-astra", Context: 1050000},
		},
	}
}

// TestAISessionLaunchGatewayModelAndWindow pins that the catalog's model and its
// context window reach the launch, and that an environment's own choice wins
// over the catalog default with its own window.
func TestAISessionLaunchGatewayModelAndWindow(t *testing.T) {
	model := func(v string) *string { return &v }
	gateway := gatewayFixture()

	got := AISessionLaunchCommand("", EnvironmentClaudeConfig{}, gateway, "team", "dev")
	if strings.Count(got, "--model deepseek/deepseek-v4.1-flash") != 2 {
		t.Fatalf("expected the catalog default model in both guard branches, got %q", got)
	}
	if strings.Count(got, "CLAUDE_CODE_MAX_CONTEXT_TOKENS=1048576 ") != 2 {
		t.Fatalf("expected the context window in both guard branches, got %q", got)
	}
	if strings.Count(got, "CLAUDE_CODE_SUBAGENT_MODEL=deepseek/deepseek-v4.1-flash claude") != 2 {
		t.Fatalf("expected the subagent mirror in both guard branches, got %q", got)
	}

	chosen := AISessionLaunchCommand("", EnvironmentClaudeConfig{DefaultModel: model("openai/gpt-6-astra")}, gateway, "team", "dev")
	if !strings.Contains(chosen, "--model openai/gpt-6-astra") {
		t.Fatalf("expected the environment's chosen model, got %q", chosen)
	}
	if !strings.Contains(chosen, "CLAUDE_CODE_MAX_CONTEXT_TOKENS=1050000 ") {
		t.Fatalf("expected the chosen model's own window, got %q", chosen)
	}
}

// TestAISessionLaunchGatewayTypedId pins that an id the catalog does not list is
// honoured rather than silently replaced, and that no window is invented for it.
func TestAISessionLaunchGatewayTypedId(t *testing.T) {
	model := func(v string) *string { return &v }
	got := AISessionLaunchCommand("", EnvironmentClaudeConfig{DefaultModel: model("vendor/typed-by-hand")}, gatewayFixture(), "team", "dev")
	if !strings.Contains(got, "--model vendor/typed-by-hand") {
		t.Fatalf("a typed id must not be silently replaced, got %q", got)
	}
	if strings.Contains(got, "CLAUDE_CODE_MAX_CONTEXT_TOKENS") {
		t.Fatalf("an unlisted id must not invent a context window, got %q", got)
	}
}

// TestAISessionLaunchGatewayUndeclaredWindow pins that a catalog entry with no
// declared window emits no context variable, leaving Claude Code's own
// assumption in place rather than a made-up value.
func TestAISessionLaunchGatewayUndeclaredWindow(t *testing.T) {
	noWindow := &OpenRouterConfig{BaseURL: "https://openrouter.ai/api", Models: []OpenRouterModel{{ID: "a/b"}}}
	got := AISessionLaunchCommand("", EnvironmentClaudeConfig{}, noWindow, "team", "dev")
	if !strings.Contains(got, "--model a/b") {
		t.Fatalf("expected the model, got %q", got)
	}
	if strings.Contains(got, "CLAUDE_CODE_MAX_CONTEXT_TOKENS") {
		t.Fatalf("no declared window must emit no context variable, got %q", got)
	}
}

// TestAISessionLaunchGatewayRemoteControl pins that Remote Control is suppressed
// under gateway auth: it pairs through the claude.ai relay, which gateway
// credentials cannot satisfy, so enabling it would fail to pair.
func TestAISessionLaunchGatewayRemoteControl(t *testing.T) {
	got := AISessionLaunchCommand("", EnvironmentClaudeConfig{}, gatewayFixture(), "team", "dev")
	if strings.Contains(got, "--remote-control") {
		t.Fatalf("gateway auth must not enable remote control, got %q", got)
	}
}

// TestAISessionLaunchGatewayCredentialStaysOut pins the property that matters
// most: the credential never reaches a launch command, whose argv is visible to
// anything that can list processes. The Secret names it and the pod environment
// carries it; neither belongs in the launch.
func TestAISessionLaunchGatewayCredentialStaysOut(t *testing.T) {
	gated := &OpenRouterConfig{
		BaseURL:         "https://openrouter.ai/api",
		AuthTokenSecret: "sk-or-secret-must-not-leak",
		Models:          []OpenRouterModel{{ID: "a/b", Context: 100}},
	}
	for _, got := range []string{
		AISessionLaunchCommand("", EnvironmentClaudeConfig{}, gated, "team", "dev"),
		strings.Join(AISessionLaunchLines("", EnvironmentClaudeConfig{}, gated, "team", "dev"), "\n"),
	} {
		if strings.Contains(got, "sk-or-secret-must-not-leak") {
			t.Fatalf("the credential reference leaked into a launch command: %q", got)
		}
		if strings.Contains(got, "ANTHROPIC_AUTH_TOKEN") || strings.Contains(got, "ANTHROPIC_BASE_URL") {
			t.Fatalf("gateway routing variables belong in settings, not the launch command: %q", got)
		}
	}
}

// TestAISessionLaunchGatewayUnconfigured pins that an absent or empty catalog
// leaves the launch exactly as it was before gateways existed.
func TestAISessionLaunchGatewayUnconfigured(t *testing.T) {
	withNil := AISessionLaunchCommand("", EnvironmentClaudeConfig{}, nil, "team", "dev")
	withEmpty := AISessionLaunchCommand("", EnvironmentClaudeConfig{}, &OpenRouterConfig{}, "team", "dev")
	if withNil != withEmpty {
		t.Fatalf("an empty catalog must behave as no catalog:\n%q\n%q", withNil, withEmpty)
	}
	if !strings.Contains(withNil, "--remote-control team/dev") {
		t.Fatalf("without a gateway remote control must still be named, got %q", withNil)
	}
}

// TestResolveClaudeLaunchModelWithGateway covers gateway-side model resolution:
// the catalog supplies the default, and an environment's typed id is honoured
// even though the catalog does not list it — refusing it here would silently
// launch a different model than the tab shows.
func TestResolveClaudeLaunchModelWithGateway(t *testing.T) {
	model := func(v string) *string { return &v }
	gateway := &OpenRouterConfig{BaseURL: "https://x", Models: []OpenRouterModel{{ID: "a/b"}, {ID: "c/d"}}}

	cases := []struct {
		name   string
		config EnvironmentClaudeConfig
		want   string
	}{
		{"unset takes the catalog's first entry", EnvironmentClaudeConfig{}, "a/b"},
		{"the environment's choice wins", EnvironmentClaudeConfig{DefaultModel: model("c/d")}, "c/d"},
		{"an id outside the catalog is honoured", EnvironmentClaudeConfig{DefaultModel: model("typed/by-hand")}, "typed/by-hand"},
		{"an unsafe choice falls back to the catalog default", EnvironmentClaudeConfig{DefaultModel: model("a b; rm")}, "a/b"},
		{"a blank choice falls back to the catalog default", EnvironmentClaudeConfig{DefaultModel: model("   ")}, "a/b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveClaudeLaunchModel(tc.config, gateway); got != tc.want {
				t.Fatalf("resolveClaudeLaunchModel(%+v, gateway) = %q, want %q", tc.config, got, tc.want)
			}
		})
	}
}
