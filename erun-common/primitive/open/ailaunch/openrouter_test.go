package ailaunch

import (
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"gopkg.in/yaml.v3"
)

// gatewayFixture names no model erun refuses, so tests exercise plumbing only.
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

func TestAISessionLaunchGatewayRemoteControl(t *testing.T) {
	got := AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{}, gatewayFixture(), "team", "dev")
	if strings.Contains(got, "--remote-control") {
		t.Fatalf("gateway auth must not enable remote control, got %q", got)
	}
}

// The launch argv is visible to any process lister; the pod reads the Secret.
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

// reasoningEchoGateway's first entry, the would-be default, is undriveable.
func reasoningEchoGateway() *configpkg.OpenRouterConfig {
	return &configpkg.OpenRouterConfig{
		BaseURL: "https://openrouter.ai/api",
		Models: []configpkg.OpenRouterModel{
			{ID: "deepseek/deepseek-v4.1-flash", Context: 262144, RequiresReasoningEcho: true},
			{ID: "anthropic/claude-fable-5.1", Context: 200000},
		},
	}
}

// The refused id must appear nowhere, including the env prefix.
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

	kept := AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{DefaultModel: model("anthropic/claude-fable-5.1")}, gateway, "team", "dev")
	if !strings.Contains(kept, "--model anthropic/claude-fable-5.1") {
		t.Fatalf("expected a driveable environment choice to be honoured, got %q", kept)
	}

	uncurated := AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{DefaultModel: model("typed/by-hand")}, gateway, "team", "dev")
	if !strings.Contains(uncurated, "--model typed/by-hand") {
		t.Fatalf("expected an uncurated choice to be honoured, got %q", uncurated)
	}
}

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

// Catalogs seeded from Claude Code settings carry no declaration, so erun must
// know the model itself.
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

	if !gateway.HasModel("deepseek/deepseek-v4.1-flash") {
		t.Fatal("expected the catalog to still list the model it was written with")
	}
	if ids := gateway.ModelIDs(); len(ids) != 1 || ids[0] != "anthropic/claude-fable-5.1" {
		t.Fatalf("ModelIDs = %v, want the known-undriveable listing omitted so it is never offered", ids)
	}
	if got := gateway.ResolveDefaultModel(); got != "anthropic/claude-fable-5.1" {
		t.Fatalf("ResolveDefaultModel = %q, want a driveable listing rather than ANTHROPIC_MODEL landing on the known-undriveable one", got)
	}

	model := func(v string) *string { return &v }
	launch := AISessionLaunchCommand("", configpkg.EnvironmentClaudeConfig{DefaultModel: model("deepseek/deepseek-v4.1-flash")}, gateway, "team", "dev")
	if strings.Contains(launch, "deepseek/deepseek-v4.1-flash") {
		t.Fatalf("an environment that already saved the known-undriveable model still launched on it: %q", launch)
	}
	if !strings.Contains(launch, "--model anthropic/claude-fable-5.1") {
		t.Fatalf("expected the launch to fall back to the driveable default, got %q", launch)
	}

	// The refusal belongs to the model, not to a catalog row.
	if got := ResolveClaudeLaunchModel(configpkg.EnvironmentClaudeConfig{DefaultModel: model("deepseek/deepseek-v4.1-flash")}, &configpkg.OpenRouterConfig{BaseURL: "https://x"}); got != "" {
		t.Fatalf("an unlisted known-undriveable choice resolved to %q, want no model at all", got)
	}
}
