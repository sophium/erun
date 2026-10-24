package config

import (
	"errors"
	"strings"
	"testing"
)

func TestOpenRouterConfigNilSafety(t *testing.T) {
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

func TestOpenRouterConfigAuthTokenRefName(t *testing.T) {
	defaulted := &OpenRouterConfig{BaseURL: "https://x"}
	if got := defaulted.AuthTokenRefName(); got != DefaultOpenRouterAuthTokenRef {
		t.Fatalf("unnamed ref = %q, want the default", got)
	}

	// An existing ref is kept so its store entry is not orphaned.
	named := &OpenRouterConfig{BaseURL: "https://x", AuthTokenRef: "  my-existing-ref  "}
	if got := named.AuthTokenRefName(); got != "my-existing-ref" {
		t.Fatalf("named ref = %q, want the trimmed ref", got)
	}

	var unconfigured *OpenRouterConfig
	if got := unconfigured.AuthTokenRefName(); got != "" {
		t.Fatalf("unconfigured ref = %q, want empty", got)
	}
	if got := (&OpenRouterConfig{}).AuthTokenRefName(); got != "" {
		t.Fatalf("catalog without a base URL ref = %q, want empty", got)
	}
}

func TestEffectiveGateway(t *testing.T) {
	yes, no := func() *bool { b := true; return &b }(), func() *bool { b := false; return &b }()
	catalog := &OpenRouterConfig{BaseURL: "https://openrouter.ai/api", AuthTokenRef: "claude-gateway"}

	t.Run("an unconfigured catalog reaches no environment", func(t *testing.T) {
		if got := EffectiveGateway(EnvironmentClaudeConfig{}, nil); got.Configured() {
			t.Fatal("no catalog must mean no gateway")
		}
		if got := EffectiveGateway(EnvironmentClaudeConfig{}, &OpenRouterConfig{}); got.Configured() {
			t.Fatal("a catalog with no base URL must mean no gateway")
		}
	})

	t.Run("unset inherits the erun-level decision", func(t *testing.T) {
		if got := EffectiveGateway(EnvironmentClaudeConfig{}, catalog); got != catalog {
			t.Fatalf("unset override = %v, want the catalog itself", got)
		}
		if got := EffectiveGateway(EnvironmentClaudeConfig{UseGateway: yes}, catalog); got != catalog {
			t.Fatalf("explicit true = %v, want the catalog itself", got)
		}
	})

	t.Run("an environment can stay on its own Claude sign-in", func(t *testing.T) {
		if got := EffectiveGateway(EnvironmentClaudeConfig{UseGateway: no}, catalog); got != nil {
			t.Fatalf("opted-out environment = %v, want no gateway", got)
		}
	})

	t.Run("the catalog is returned as it is, credential included", func(t *testing.T) {
		got := EffectiveGateway(EnvironmentClaudeConfig{}, catalog)
		if got != catalog {
			t.Fatal("the catalog must be returned unchanged, not copied or rewritten")
		}
		if got.AuthTokenRefName() != "claude-gateway" {
			t.Fatalf("ref = %q, want the catalog's own", got.AuthTokenRefName())
		}
	})
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
	// A missing window and an unknown model both report 0.
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
	c := &OpenRouterConfig{BaseURL: "  https://openrouter.ai/api  ", AuthTokenRef: "claude-gateway"}
	env := c.GatewayEnvVars()
	if env["ANTHROPIC_BASE_URL"] != "https://openrouter.ai/api" {
		t.Fatalf("base URL = %q, want the trimmed value", env["ANTHROPIC_BASE_URL"])
	}
	// Present-but-empty: unset lets Claude Code bypass the gateway.
	if value, ok := env["ANTHROPIC_API_KEY"]; !ok || value != "" {
		t.Fatalf("ANTHROPIC_API_KEY must be present and empty, got %q (present=%v)", value, ok)
	}
	// The credential and its ref never belong in the settings env block.
	for key, value := range env {
		if strings.Contains(key, "TOKEN") || strings.Contains(value, "claude-gateway") {
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

// reasoningEchoGateway's first entry is undriveable.
func reasoningEchoGateway() *OpenRouterConfig {
	return &OpenRouterConfig{
		BaseURL: "https://openrouter.ai/api",
		Models: []OpenRouterModel{
			{ID: "deepseek/deepseek-v4.1-flash", Context: 262144, RequiresReasoningEcho: true},
			{ID: "anthropic/claude-fable-5.1", Context: 200000},
		},
	}
}

func TestOpenRouterCatalogStopsAdvertisingAModelThatRequiresReasoningEcho(t *testing.T) {
	gateway := reasoningEchoGateway()

	if got := gateway.ModelIDs(); len(got) != 1 || got[0] != "anthropic/claude-fable-5.1" {
		t.Fatalf("ModelIDs = %v, want only the driveable listing", got)
	}
	if !gateway.RequiresReasoningEcho("deepseek/deepseek-v4.1-flash") {
		t.Fatal("the declared listing must report as requiring reasoning echo")
	}
	if gateway.RequiresReasoningEcho("anthropic/claude-fable-5.1") {
		t.Fatal("a driveable listing must not report as requiring reasoning echo")
	}
	if gateway.RequiresReasoningEcho("typed/by-hand") {
		t.Fatal("an uncurated id must not be reported as requiring reasoning echo")
	}
}

func TestOpenRouterDefaultResolutionSkipsAModelThatRequiresReasoningEcho(t *testing.T) {
	gateway := reasoningEchoGateway()
	if got := gateway.ResolveDefaultModel(); got != "anthropic/claude-fable-5.1" {
		t.Fatalf("ResolveDefaultModel = %q, want the first driveable listing", got)
	}

	named := reasoningEchoGateway()
	named.DefaultModel = "deepseek/deepseek-v4.1-flash"
	if got := named.ResolveDefaultModel(); got != "anthropic/claude-fable-5.1" {
		t.Fatalf("ResolveDefaultModel with an undriveable default = %q, want a driveable listing", got)
	}

	only := reasoningEchoGateway()
	only.Models = []OpenRouterModel{{ID: "deepseek/deepseek-v4.1-flash", RequiresReasoningEcho: true}}
	if got := only.ResolveDefaultModel(); got != "" {
		t.Fatalf("ResolveDefaultModel of an undriveable-only catalog = %q, want empty", got)
	}
}
