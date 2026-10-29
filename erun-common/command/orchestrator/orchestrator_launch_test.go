package orchestrator

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/ai"
	"github.com/sophium/erun/erun-common/primitive/open/ailaunch"
)

func orchestratorLaunchTestCatalog() *config.OpenRouterConfig {
	return &config.OpenRouterConfig{
		BaseURL:      " https://gateway.example/api ",
		DefaultModel: "vendor/default-model",
		Models: []config.OpenRouterModel{
			{ID: "vendor/default-model", Context: 200000},
			{ID: "vendor/other-model"},
			{ID: "vendor/echo-model", RequiresReasoningEcho: true},
		},
	}
}

func TestResolveOrchestratorLaunchWithNoSettingsKeepsTheHistoricalLaunch(t *testing.T) {
	for name, catalog := range map[string]*config.OpenRouterConfig{
		"no catalog":         nil,
		"configured catalog": orchestratorLaunchTestCatalog(),
	} {
		t.Run(name, func(t *testing.T) {
			launch, err := ResolveOrchestratorLaunch(config.OrchestratorConfig{ID: "o1"}, catalog)
			if err != nil {
				t.Fatalf("ResolveOrchestratorLaunch: %v", err)
			}
			want := OrchestratorLaunch{
				Model:       "opus",
				Effort:      "ultracode",
				EffortFlags: ` --settings '{"ultracode":true}'`,
				Env:         []string{"CLAUDE_CODE_SUBAGENT_MODEL=opus"},
			}
			if !reflect.DeepEqual(launch, want) {
				t.Fatalf("launch = %#v, want %#v", launch, want)
			}
		})
	}
}

func TestResolveOrchestratorLaunchMapsEveryEffortLevelToItsFlags(t *testing.T) {
	want := map[string]string{
		"low":       " --effort low",
		"medium":    " --effort medium",
		"high":      " --effort high",
		"xhigh":     " --effort xhigh",
		"max":       " --effort max",
		"ultracode": ` --settings '{"ultracode":true}'`,
	}
	for _, level := range ai.ClaudeEffortLevels() {
		launch, err := ResolveOrchestratorLaunch(config.OrchestratorConfig{Effort: level}, nil)
		if err != nil {
			t.Fatalf("effort %q: %v", level, err)
		}
		if launch.Effort != level || launch.EffortFlags != want[level] {
			t.Fatalf("effort %q resolved to %q / %q, want %q", level, launch.Effort, launch.EffortFlags, want[level])
		}
	}
	if len(want) != len(ai.ClaudeEffortLevels()) {
		t.Fatalf("effort levels changed: %v", ai.ClaudeEffortLevels())
	}
}

func TestResolveOrchestratorLaunchHonoursAModelOffTheGateway(t *testing.T) {
	launch, err := ResolveOrchestratorLaunch(config.OrchestratorConfig{Model: "claude-sonnet-5-5"}, orchestratorLaunchTestCatalog())
	if err != nil {
		t.Fatalf("ResolveOrchestratorLaunch: %v", err)
	}
	if launch.Model != "claude-sonnet-5-5" || launch.Gateway != nil {
		t.Fatalf("launch = %#v, want the recorded model on the host's own configuration", launch)
	}
	if want := []string{"CLAUDE_CODE_SUBAGENT_MODEL=claude-sonnet-5-5"}; !reflect.DeepEqual(launch.Env, want) {
		t.Fatalf("env = %v, want %v", launch.Env, want)
	}
}

func TestResolveOrchestratorLaunchThroughTheGatewayMirrorsThePodEnvironment(t *testing.T) {
	catalog := orchestratorLaunchTestCatalog()
	launch, err := ResolveOrchestratorLaunch(config.OrchestratorConfig{UseGateway: true}, catalog)
	if err != nil {
		t.Fatalf("ResolveOrchestratorLaunch: %v", err)
	}
	if launch.Gateway != catalog {
		t.Fatalf("gateway = %#v, want the catalog", launch.Gateway)
	}
	if launch.Model != "vendor/default-model" {
		t.Fatalf("model = %q, want the catalog default", launch.Model)
	}
	want := []string{
		"CLAUDE_CODE_SUBAGENT_MODEL=vendor/default-model",
		"ANTHROPIC_BASE_URL=https://gateway.example/api",
		"ANTHROPIC_API_KEY=",
		"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1",
		"CLAUDE_CODE_MAX_CONTEXT_TOKENS=200000",
	}
	if !reflect.DeepEqual(launch.Env, want) {
		t.Fatalf("env = %v, want %v", launch.Env, want)
	}
	for _, entry := range launch.Env {
		if strings.HasPrefix(entry, "ANTHROPIC_AUTH_TOKEN=") {
			t.Fatalf("the credential must not be resolved into the launch: %v", launch.Env)
		}
	}
}

func TestResolveOrchestratorLaunchThroughTheGatewayHonoursAnUncuratedModel(t *testing.T) {
	launch, err := ResolveOrchestratorLaunch(config.OrchestratorConfig{UseGateway: true, Model: "vendor/uncurated"}, orchestratorLaunchTestCatalog())
	if err != nil {
		t.Fatalf("ResolveOrchestratorLaunch: %v", err)
	}
	if launch.Model != "vendor/uncurated" {
		t.Fatalf("model = %q, want the recorded model", launch.Model)
	}
	for _, entry := range launch.Env {
		if strings.HasPrefix(entry, "CLAUDE_CODE_MAX_CONTEXT_TOKENS=") {
			t.Fatalf("an uncatalogued model has no known window to declare: %v", launch.Env)
		}
	}
}

func TestResolveOrchestratorLaunchFallsBackToTheDefaultModelWhenTheCatalogListsNone(t *testing.T) {
	launch, err := ResolveOrchestratorLaunch(config.OrchestratorConfig{UseGateway: true}, &config.OpenRouterConfig{BaseURL: "https://gateway.example/api"})
	if err != nil {
		t.Fatalf("ResolveOrchestratorLaunch: %v", err)
	}
	if launch.Model != DefaultOrchestratorModel {
		t.Fatalf("model = %q, want %q", launch.Model, DefaultOrchestratorModel)
	}
}

func TestResolveOrchestratorLaunchRefusesSettingsItCannotHonour(t *testing.T) {
	cases := map[string]struct {
		orchestrator config.OrchestratorConfig
		catalog      *config.OpenRouterConfig
		want         string
	}{
		"gateway without a catalog": {
			orchestrator: config.OrchestratorConfig{UseGateway: true},
			want:         "no gateway catalog is configured",
		},
		"unknown effort": {
			orchestrator: config.OrchestratorConfig{Effort: "turbo"},
			want:         `invalid effort "turbo"`,
		},
		"model that is flag syntax": {
			orchestrator: config.OrchestratorConfig{Model: "--dangerously-skip-permissions"},
			want:         `invalid model "--dangerously-skip-permissions"`,
		},
		"model that is shell syntax": {
			orchestrator: config.OrchestratorConfig{Model: "opus;rm -rf /"},
			want:         `invalid model "opus;rm -rf /"`,
		},
		"model the gateway cannot drive": {
			orchestrator: config.OrchestratorConfig{UseGateway: true, Model: "vendor/echo-model"},
			catalog:      orchestratorLaunchTestCatalog(),
			want:         `model "vendor/echo-model" cannot be driven through the gateway`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ResolveOrchestratorLaunch(tc.orchestrator, tc.catalog)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestValidateOrchestratorLaunchSettingsAllowsAnEchoModelOffTheGateway(t *testing.T) {
	if err := ValidateOrchestratorLaunchSettings(config.OrchestratorConfig{Model: "vendor/echo-model"}, orchestratorLaunchTestCatalog()); err != nil {
		t.Fatalf("ValidateOrchestratorLaunchSettings: %v", err)
	}
}

// TestClaudeConfiguredModelIgnoresAModelThatIsFlagSyntax: a recorded model with a leading '-' must
// be ignored, not passed as a flag.
func TestClaudeConfiguredModelIgnoresAModelThatIsFlagSyntax(t *testing.T) {
	for _, model := range []string{"--dangerously-skip-permissions", "-p", "-"} {
		value := model
		if got := ailaunch.ClaudeConfiguredModel(config.EnvironmentClaudeConfig{DefaultModel: &value}); got != "" {
			t.Fatalf("claudeConfiguredModel(%q) = %q, want it ignored", model, got)
		}
	}
	for _, model := range []string{"opus", "claude-opus-5-5", "vendor/model-v1.2:beta"} {
		value := model
		if got := ailaunch.ClaudeConfiguredModel(config.EnvironmentClaudeConfig{DefaultModel: &value}); got != model {
			t.Fatalf("claudeConfiguredModel(%q) = %q, want it kept", model, got)
		}
	}
}

// TestResolveOrchestratorLaunchNeverLaunchesAnUnsafeCatalogModel: a catalog default model is
// spliced into the host launch line, so it gets the token check too.
func TestResolveOrchestratorLaunchNeverLaunchesAnUnsafeCatalogModel(t *testing.T) {
	catalog := &config.OpenRouterConfig{
		BaseURL:      "https://gateway.example/api",
		DefaultModel: "--dangerously-skip-permissions",
		Models: []config.OpenRouterModel{
			{ID: "x;touch${IFS}/tmp/pwned"},
			{ID: "--dangerously-skip-permissions"},
			{ID: "vendor/safe-model"},
		},
	}
	launch, err := ResolveOrchestratorLaunch(config.OrchestratorConfig{UseGateway: true}, catalog)
	if err != nil {
		t.Fatalf("ResolveOrchestratorLaunch: %v", err)
	}
	if launch.Model != "vendor/safe-model" {
		t.Fatalf("model = %q, want the first safe catalog model", launch.Model)
	}
	if got := catalog.ModelIDs(); !reflect.DeepEqual(got, []string{"vendor/safe-model"}) {
		t.Fatalf("ModelIDs() = %v, want only the safe id offered", got)
	}
	if got := ailaunch.ResolveGatewayLaunchModel(config.EnvironmentClaudeConfig{}, catalog); got != "vendor/safe-model" {
		t.Fatalf("resolveGatewayLaunchModel = %q, want the first safe catalog model", got)
	}
}

func TestValidateOrchestratorLaunchSettingsRefusesTheDefaultTokenAsAModel(t *testing.T) {
	for _, model := range []string{"default", " Default "} {
		err := ValidateOrchestratorLaunchSettings(config.OrchestratorConfig{Model: model}, nil)
		if err == nil || !strings.Contains(err.Error(), "token for clearing the model") {
			t.Fatalf("model %q: err = %v, want the clearing token refused", model, err)
		}
	}
}
