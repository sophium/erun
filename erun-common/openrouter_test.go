package eruncommon

import (
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"gopkg.in/yaml.v3"
)

// gatewayFixture is the catalog the gateway launch contracts share. It is an
// ordinary catalog: both listings are driveable, so it says
// nothing about which models erun refuses. It deliberately names neither the
// model whose provider refuses a conversation nor any other id erun carries
// evidence against, so a test built on it exercises the plumbing it means to
// rather than a refusal that happens to be reached first.
func gatewayFixture() *configpkg.OpenRouterConfig {
	return &configpkg.OpenRouterConfig{
		BaseURL:      "https://openrouter.ai/api",
		AuthTokenRef: "claude-gateway",
		Models: []configpkg.OpenRouterModel{
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

	got := AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{}, gateway, "team", "dev")
	if strings.Count(got, "--model vendor/catalog-default") != 1 {
		t.Fatalf("expected the catalog default model, got %q", got)
	}
	if strings.Count(got, "CLAUDE_CODE_MAX_CONTEXT_TOKENS=1048576 ") != 1 {
		t.Fatalf("expected the context window, got %q", got)
	}
	if strings.Count(got, "CLAUDE_CODE_SUBAGENT_MODEL=vendor/catalog-default claude") != 1 {
		t.Fatalf("expected the subagent mirror, got %q", got)
	}

	chosen := AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{DefaultModel: model("openai/gpt-6-astra")}, gateway, "team", "dev")
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
	got := AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{DefaultModel: model("vendor/typed-by-hand")}, gatewayFixture(), "team", "dev")
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
	noWindow := &configpkg.OpenRouterConfig{BaseURL: "https://openrouter.ai/api", Models: []configpkg.OpenRouterModel{{ID: "a/b"}}}
	got := AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{}, noWindow, "team", "dev")
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
	got := AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{}, gatewayFixture(), "team", "dev")
	if strings.Contains(got, "--remote-control") {
		t.Fatalf("gateway auth must not enable remote control, got %q", got)
	}
}

// TestAISessionLaunchGatewayCredentialStaysOut pins the property that matters
// most: the credential never reaches a launch command, whose argv is visible to
// anything that can list processes. The pod reads it from a Secret erun
// delivers; neither the value nor the store ref belongs in the launch.
func TestAISessionLaunchGatewayCredentialStaysOut(t *testing.T) {
	gated := &configpkg.OpenRouterConfig{
		BaseURL:      "https://openrouter.ai/api",
		AuthTokenRef: "sk-or-secret-must-not-leak",
		Models:       []configpkg.OpenRouterModel{{ID: "a/b", Context: 100}},
	}
	for _, got := range []string{
		AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{}, gated, "team", "dev"),
		strings.Join(AISessionLaunchLines("", configpkg.EnvironmentClaudeConfig{}, gated, "team", "dev"), "\n"),
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
	withNil := AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{}, nil, "team", "dev")
	withEmpty := AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{}, &configpkg.OpenRouterConfig{}, "team", "dev")
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
	gateway := &configpkg.OpenRouterConfig{BaseURL: "https://x", Models: []configpkg.OpenRouterModel{{ID: "a/b"}, {ID: "c/d"}}}

	cases := []struct {
		name   string
		config configpkg.EnvironmentClaudeConfig
		want   string
	}{
		{"unset takes the catalog's first entry", configpkg.EnvironmentClaudeConfig{}, "a/b"},
		{"the environment's choice wins", configpkg.EnvironmentClaudeConfig{DefaultModel: model("c/d")}, "c/d"},
		{"an id outside the catalog is honoured", configpkg.EnvironmentClaudeConfig{DefaultModel: model("typed/by-hand")}, "typed/by-hand"},
		{"an unsafe choice falls back to the catalog default", configpkg.EnvironmentClaudeConfig{DefaultModel: model("a b; rm")}, "a/b"},
		{"a blank choice falls back to the catalog default", configpkg.EnvironmentClaudeConfig{DefaultModel: model("   ")}, "a/b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveClaudeLaunchModel(tc.config, gateway); got != tc.want {
				t.Fatalf("resolveClaudeLaunchModel(%+v, gateway) = %q, want %q", tc.config, got, tc.want)
			}
		})
	}
}

// reasoningEchoGateway is a catalog whose first entry is declared undriveable:
// the default an environment would otherwise render as ANTHROPIC_MODEL, and the
// model an exec agent job would start on.
func reasoningEchoGateway() *configpkg.OpenRouterConfig {
	return &configpkg.OpenRouterConfig{
		BaseURL: "https://openrouter.ai/api",
		Models: []configpkg.OpenRouterModel{
			{ID: "deepseek/deepseek-v4.1-flash", Context: 262144, RequiresReasoningEcho: true},
			{ID: "anthropic/claude-fable-5.1", Context: 200000},
		},
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

	chosen := AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{DefaultModel: model("deepseek/deepseek-v4.1-flash")}, gateway, "team", "dev")
	if strings.Contains(chosen, "deepseek/deepseek-v4.1-flash") {
		t.Fatalf("the refused listing reached the launch command: %q", chosen)
	}
	if !strings.Contains(chosen, "--model anthropic/claude-fable-5.1") {
		t.Fatalf("expected the launch to fall back to the driveable default, got %q", chosen)
	}

	// The environment's own driveable choice still wins, so the refusal is
	// scoped to the declared listing rather than to environment choices at all.
	kept := AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{DefaultModel: model("anthropic/claude-fable-5.1")}, gateway, "team", "dev")
	if !strings.Contains(kept, "--model anthropic/claude-fable-5.1") {
		t.Fatalf("expected a driveable environment choice to be honoured, got %q", kept)
	}

	// An uncurated id stays a supported act: the catalog never declared it, so
	// nothing here refuses it.
	uncurated := AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{DefaultModel: model("typed/by-hand")}, gateway, "team", "dev")
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
	var config configpkg.ERunConfig
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
	launch := AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{DefaultModel: model("deepseek/deepseek-v4.1-flash")}, gateway, "team", "dev")
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
	var config configpkg.ERunConfig
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
	launch := AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{DefaultModel: model("deepseek/deepseek-v4.1-flash")}, gateway, "team", "dev")
	if strings.Contains(launch, "deepseek/deepseek-v4.1-flash") {
		t.Fatalf("an environment that already saved the known-undriveable model still launched on it: %q", launch)
	}
	if !strings.Contains(launch, "--model anthropic/claude-fable-5.1") {
		t.Fatalf("expected the launch to fall back to the driveable default, got %q", launch)
	}

	// It is refused as an environment choice even where no catalog lists it at
	// all, because the refusal is a property of the model rather than of a row
	// somebody remembered to annotate.
	if got := ResolveClaudeLaunchModel(configpkg.EnvironmentClaudeConfig{DefaultModel: model("deepseek/deepseek-v4.1-flash")}, &configpkg.OpenRouterConfig{BaseURL: "https://x"}); got != "" {
		t.Fatalf("an unlisted known-undriveable choice resolved to %q, want no model at all", got)
	}
}
