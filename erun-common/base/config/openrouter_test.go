package config

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

func TestOpenRouterConfigAuthTokenRefName(t *testing.T) {
	// One erun-level catalog means one ref for every environment, so a catalog
	// that names none still resolves to a working one rather than leaving the
	// operator to invent a reference.
	defaulted := &OpenRouterConfig{BaseURL: "https://x"}
	if got := defaulted.AuthTokenRefName(); got != DefaultOpenRouterAuthTokenRef {
		t.Fatalf("unnamed ref = %q, want the default", got)
	}

	// A ref the operator has already written their credential under keeps being
	// used, so an existing store entry is not orphaned by configuring a catalog.
	named := &OpenRouterConfig{BaseURL: "https://x", AuthTokenRef: "  my-existing-ref  "}
	if got := named.AuthTokenRefName(); got != "my-existing-ref" {
		t.Fatalf("named ref = %q, want the trimmed ref", got)
	}

	// A catalog that names no gateway resolves to no ref at all, so "no gateway"
	// stays distinguishable from "gateway with the default ref".
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
		// The point of the opt-out: one environment leaves the gateway without
		// moving every other environment off it.
		if got := EffectiveGateway(EnvironmentClaudeConfig{UseGateway: no}, catalog); got != nil {
			t.Fatalf("opted-out environment = %v, want no gateway", got)
		}
	})

	t.Run("the catalog is returned as it is, credential included", func(t *testing.T) {
		// The credential is one erun-level value, so there is nothing here to
		// resolve per environment: an environment takes the gateway and its
		// credential together or opts out of both. Returning the catalog itself
		// is what keeps the chart values, the launch, and the delivered Secret
		// from ever disagreeing about which key is in play.
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
	c := &OpenRouterConfig{BaseURL: "  https://openrouter.ai/api  ", AuthTokenRef: "claude-gateway"}
	env := c.GatewayEnvVars()
	if env["ANTHROPIC_BASE_URL"] != "https://openrouter.ai/api" {
		t.Fatalf("base URL = %q, want the trimmed value", env["ANTHROPIC_BASE_URL"])
	}
	// Present-but-empty, not absent: an unset API key lets Claude Code fall back
	// to a direct provider and bypass the gateway.
	if value, ok := env["ANTHROPIC_API_KEY"]; !ok || value != "" {
		t.Fatalf("ANTHROPIC_API_KEY must be present and empty, got %q (present=%v)", value, ok)
	}
	// These are the settings-file variables, which erun writes; the credential
	// itself is delivered separately as a Secret, so neither it nor its ref
	// belongs in this block.
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

// reasoningEchoGateway is a catalog whose first entry is declared undriveable:
// the default an environment would otherwise render as ANTHROPIC_MODEL, and the
// model an exec agent job would start on.
func reasoningEchoGateway() *OpenRouterConfig {
	return &OpenRouterConfig{
		BaseURL: "https://openrouter.ai/api",
		Models: []OpenRouterModel{
			{ID: "deepseek/deepseek-v4.1-flash", Context: 262144, RequiresReasoningEcho: true},
			{ID: "anthropic/claude-fable-5.1", Context: 200000},
		},
	}
}

// TestOpenRouterCatalogStopsAdvertisingAModelThatRequiresReasoningEcho pins the
// first half of the contract: a listing the provider refuses to continue a
// conversation on is not offered to anyone. ModelIDs is what reaches
// ERUN_CLAUDE_AVAILABLE_MODELS and the desktop's model choices, so an entry
// surviving here is an entry an operator can select and then lose a
// conversation to.
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
	// An id the catalog does not list is a supported by-hand choice, not a
	// declared one, so it is never refused.
	if gateway.RequiresReasoningEcho("typed/by-hand") {
		t.Fatal("an uncurated id must not be reported as requiring reasoning echo")
	}
}

// TestOpenRouterDefaultResolutionSkipsAModelThatRequiresReasoningEcho pins the
// second half: the resolved default is what an environment renders as
// ANTHROPIC_MODEL, which is the model every exec agent job in it starts on, so
// resolving an undriveable listing here puts the whole environment's agent lane
// on a model that dies mid-run.
func TestOpenRouterDefaultResolutionSkipsAModelThatRequiresReasoningEcho(t *testing.T) {
	gateway := reasoningEchoGateway()
	if got := gateway.ResolveDefaultModel(); got != "anthropic/claude-fable-5.1" {
		t.Fatalf("ResolveDefaultModel = %q, want the first driveable listing", got)
	}

	// A default naming the undriveable listing is skipped for the same reason a
	// stale one naming an unlisted model is: it must not reach ANTHROPIC_MODEL.
	named := reasoningEchoGateway()
	named.DefaultModel = "deepseek/deepseek-v4.1-flash"
	if got := named.ResolveDefaultModel(); got != "anthropic/claude-fable-5.1" {
		t.Fatalf("ResolveDefaultModel with an undriveable default = %q, want a driveable listing", got)
	}

	// Nothing driveable is left, so there is no default to resolve rather than a
	// listing launched on the strength of being the only one present.
	only := reasoningEchoGateway()
	only.Models = []OpenRouterModel{{ID: "deepseek/deepseek-v4.1-flash", RequiresReasoningEcho: true}}
	if got := only.ResolveDefaultModel(); got != "" {
		t.Fatalf("ResolveDefaultModel of an undriveable-only catalog = %q, want empty", got)
	}
}
