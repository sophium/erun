package eruncommon

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

// gatewayFixture is the catalog the gateway launch contracts share. It is an
// ordinary catalog: both listings are driveable, so it says
// nothing about which models erun refuses. It deliberately names neither the
// model whose provider refuses a conversation nor any other id erun carries
// evidence against, so a test built on it exercises the plumbing it means to
// rather than a refusal that happens to be reached first.
func gatewayFixture() *OpenRouterConfig {
	return &OpenRouterConfig{
		BaseURL:      "https://openrouter.ai/api",
		AuthTokenRef: "claude-gateway",
		Models: []OpenRouterModel{
			{ID: "vendor/catalog-default", Context: 1048576},
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
	if strings.Count(got, "--model vendor/catalog-default") != 2 {
		t.Fatalf("expected the catalog default model in both guard branches, got %q", got)
	}
	if strings.Count(got, "CLAUDE_CODE_MAX_CONTEXT_TOKENS=1048576 ") != 2 {
		t.Fatalf("expected the context window in both guard branches, got %q", got)
	}
	if strings.Count(got, "CLAUDE_CODE_SUBAGENT_MODEL=vendor/catalog-default claude") != 2 {
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
// anything that can list processes. The pod reads it from a Secret erun
// delivers; neither the value nor the store ref belongs in the launch.
func TestAISessionLaunchGatewayCredentialStaysOut(t *testing.T) {
	gated := &OpenRouterConfig{
		BaseURL:      "https://openrouter.ai/api",
		AuthTokenRef: "sk-or-secret-must-not-leak",
		Models:       []OpenRouterModel{{ID: "a/b", Context: 100}},
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

// TestAISessionLaunchRefusesAnEnvironmentChoiceThatRequiresReasoningEcho covers
// the path an already-saved environment takes, which the catalog alone cannot
// fix: resolveGatewayLaunchModel deliberately honours an environment's own model
// choice even when the catalog does not list it, so a choice naming a declared
// listing has to be refused on its own rather than by omission. The launch must
// carry the driveable default and must not carry the refused id anywhere — not
// as --model, and not in the subagent/context env prefix built beside it.
func TestAISessionLaunchRefusesAnEnvironmentChoiceThatRequiresReasoningEcho(t *testing.T) {
	model := func(v string) *string { return &v }
	gateway := reasoningEchoGateway()

	chosen := AISessionLaunchCommand("", EnvironmentClaudeConfig{DefaultModel: model("deepseek/deepseek-v4.1-flash")}, gateway, "team", "dev")
	if strings.Contains(chosen, "deepseek/deepseek-v4.1-flash") {
		t.Fatalf("the refused listing reached the launch command: %q", chosen)
	}
	if !strings.Contains(chosen, "--model anthropic/claude-fable-5.1") {
		t.Fatalf("expected the launch to fall back to the driveable default, got %q", chosen)
	}

	// The environment's own driveable choice still wins, so the refusal is
	// scoped to the declared listing rather than to environment choices at all.
	kept := AISessionLaunchCommand("", EnvironmentClaudeConfig{DefaultModel: model("anthropic/claude-fable-5.1")}, gateway, "team", "dev")
	if !strings.Contains(kept, "--model anthropic/claude-fable-5.1") {
		t.Fatalf("expected a driveable environment choice to be honoured, got %q", kept)
	}

	// An uncurated id stays a supported act: the catalog never declared it, so
	// nothing here refuses it.
	uncurated := AISessionLaunchCommand("", EnvironmentClaudeConfig{DefaultModel: model("typed/by-hand")}, gateway, "team", "dev")
	if !strings.Contains(uncurated, "--model typed/by-hand") {
		t.Fatalf("expected an uncurated choice to be honoured, got %q", uncurated)
	}
}

// TestACatalogDeclaringReasoningEchoStopsAdvertisingAndSelectingIt drives the
// declaration through the operator's own config, which is the path that actually
// sets it, and states the defect in the terms the report gives: erun's catalog
// offered a model its agent lane cannot drive, and the failure surfaced as a
// conversation refused mid-run rather than as a launch that never happened.
//
// This is the reproduction. Before the declaration existed the key parsed as an
// unknown field and was ignored, so the listing stayed advertised, stayed the
// resolved default that an environment renders as ANTHROPIC_MODEL, and stayed
// the model an environment's own saved choice launched — the exact three ways
// the reported job reached it.
func TestACatalogDeclaringReasoningEchoStopsAdvertisingAndSelectingIt(t *testing.T) {
	var config ERunConfig
	if err := yaml.Unmarshal([]byte(`
openrouter:
    baseurl: https://openrouter.ai/api
    defaultmodel: deepseek/deepseek-v4.1-flash
    models:
        - id: deepseek/deepseek-v4.1-flash
          context: 262144
          requiresreasoningecho: true
        - id: anthropic/claude-fable-5.1
          context: 200000
`), &config); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	gateway := config.OpenRouter
	if gateway == nil {
		t.Fatal("expected a configured catalog")
	}
	if ids := gateway.ModelIDs(); len(ids) != 1 || ids[0] != "anthropic/claude-fable-5.1" {
		t.Fatalf("ModelIDs = %v, want the declared listing omitted so it is not offered", ids)
	}
	if got := gateway.ResolveDefaultModel(); got != "anthropic/claude-fable-5.1" {
		t.Fatalf("ResolveDefaultModel = %q, want a driveable listing rather than ANTHROPIC_MODEL landing on the declared one", got)
	}
	model := func(v string) *string { return &v }
	launch := AISessionLaunchCommand("", EnvironmentClaudeConfig{DefaultModel: model("deepseek/deepseek-v4.1-flash")}, gateway, "team", "dev")
	if strings.Contains(launch, "deepseek/deepseek-v4.1-flash") {
		t.Fatalf("an environment that already saved the declared listing still launched on it: %q", launch)
	}
	if !strings.Contains(launch, "--model anthropic/claude-fable-5.1") {
		t.Fatalf("expected the launch to fall back to the driveable default, got %q", launch)
	}
}

// TestACatalogThatNeverDeclaredReasoningEchoStillRefusesTheKnownModel is the
// reproduction of the reported failure, and it is deliberately not the declared
// case above: the operator's catalog names the model with no declaration at all.
//
// That is how the catalog is actually produced. The editor seeds an unconfigured
// catalog from this machine's own Claude Code settings, so a host already running
// ANTHROPIC_MODEL=deepseek/deepseek-v4.1-flash writes back a default and a model
// row carrying nothing but an id and a window — and the published configuration
// reference documents the same catalog. A declaration the operator never typed
// cannot be the only thing that refuses a model whose wire contract erun knows,
// or every catalog that predates the declaration keeps the lane on it.
//
// Before the known-model set, all three readers of the catalog resolved the
// listing and the agent lane reached a model its client cannot drive, losing the
// conversation to a provider refusal twelve to fifteen turns in.
func TestACatalogThatNeverDeclaredReasoningEchoStillRefusesTheKnownModel(t *testing.T) {
	var config ERunConfig
	if err := yaml.Unmarshal([]byte(`
openrouter:
    baseurl: https://openrouter.ai/api
    defaultmodel: deepseek/deepseek-v4.1-flash
    models:
        - id: deepseek/deepseek-v4.1-flash
          context: 262144
        - id: anthropic/claude-fable-5.1
          context: 200000
`), &config); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	gateway := config.OpenRouter
	if gateway == nil {
		t.Fatal("expected a configured catalog")
	}

	// The catalog still holds the entry; erun is what knows it cannot be driven.
	if !gateway.HasModel("deepseek/deepseek-v4.1-flash") {
		t.Fatal("expected the catalog to still list the model it was written with")
	}
	if ids := gateway.ModelIDs(); len(ids) != 1 || ids[0] != "anthropic/claude-fable-5.1" {
		t.Fatalf("ModelIDs = %v, want the known-undriveable listing omitted so it is never offered", ids)
	}
	if got := gateway.ResolveDefaultModel(); got != "anthropic/claude-fable-5.1" {
		t.Fatalf("ResolveDefaultModel = %q, want a driveable listing rather than ANTHROPIC_MODEL landing on the known-undriveable one", got)
	}

	// The environment that already saved the choice is the path the catalog
	// alone cannot cover.
	model := func(v string) *string { return &v }
	launch := AISessionLaunchCommand("", EnvironmentClaudeConfig{DefaultModel: model("deepseek/deepseek-v4.1-flash")}, gateway, "team", "dev")
	if strings.Contains(launch, "deepseek/deepseek-v4.1-flash") {
		t.Fatalf("an environment that already saved the known-undriveable model still launched on it: %q", launch)
	}
	if !strings.Contains(launch, "--model anthropic/claude-fable-5.1") {
		t.Fatalf("expected the launch to fall back to the driveable default, got %q", launch)
	}

	// It is refused as an environment choice even where no catalog lists it at
	// all, because the refusal is a property of the model rather than of a row
	// somebody remembered to annotate.
	if got := resolveClaudeLaunchModel(EnvironmentClaudeConfig{DefaultModel: model("deepseek/deepseek-v4.1-flash")}, &OpenRouterConfig{BaseURL: "https://x"}); got != "" {
		t.Fatalf("an unlisted known-undriveable choice resolved to %q, want no model at all", got)
	}
}
