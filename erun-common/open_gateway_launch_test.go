package eruncommon

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// openGatewayStore is an OpenStore whose root config carries a gateway catalog,
// the way an operator's erun-level OpenRouter settings do.
type openGatewayStore struct {
	catalog  *config.OpenRouterConfig
	claude   config.EnvironmentClaudeConfig
	repoPath string
}

func (s openGatewayStore) LoadERunConfig() (config.ERunConfig, string, error) {
	return config.ERunConfig{DefaultTenant: "team", OpenRouter: s.catalog}, "", nil
}

func (s openGatewayStore) LoadTenantConfig(name string) (config.TenantConfig, string, error) {
	return config.TenantConfig{Name: name, DefaultEnvironment: "dev"}, "", nil
}

func (s openGatewayStore) LoadEnvConfig(tenant, environment string) (config.EnvConfig, string, error) {
	return s.env(environment), "", nil
}

func (s openGatewayStore) ListTenantConfigs() ([]config.TenantConfig, error) {
	return []config.TenantConfig{{Name: "team", DefaultEnvironment: "dev"}}, nil
}

func (s openGatewayStore) ListEnvConfigs(string) ([]config.EnvConfig, error) {
	return []config.EnvConfig{s.env("dev")}, nil
}

func (s openGatewayStore) env(name string) config.EnvConfig {
	return config.EnvConfig{Name: name, LocalRepoPath: s.repoPath, KubernetesContext: "cluster", Claude: s.claude}
}

// TestOpenAILaunchHonoursTheEnvironmentsGateway is the reported flow from the
// pod's side: an environment AI tab is `erun open --ai`, and its session must
// launch on what the environment selected. The open path never handed the
// launch the erun-level catalog, so a gateway environment launched on the
// Anthropic default with Remote Control (which a gateway credential cannot
// pair), and per-environment gateway choices changed nothing at all.
func TestOpenAILaunchHonoursTheEnvironmentsGateway(t *testing.T) {
	catalog := &config.OpenRouterConfig{
		BaseURL:      "https://gateway.example/api",
		DefaultModel: "vendor/model-x",
		Models:       []config.OpenRouterModel{{ID: "vendor/model-x", Context: 200000}},
	}
	on, off := true, false
	low, opus := "low", "opus"
	cases := []struct {
		name          string
		claude        config.EnvironmentClaudeConfig
		model         string
		remoteControl bool
		context       bool
	}{
		{"inherits the gateway", config.EnvironmentClaudeConfig{Effort: &low}, "vendor/model-x", false, true},
		{"selects the gateway", config.EnvironmentClaudeConfig{UseGateway: &on, Effort: &low}, "vendor/model-x", false, true},
		{"left the gateway", config.EnvironmentClaudeConfig{UseGateway: &off, Effort: &low, DefaultModel: &opus}, "opus", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := open.ResolveOpen(openGatewayStore{catalog: catalog, claude: tc.claude, repoPath: t.TempDir()}, shared.OpenParams{Tenant: "team", Environment: "dev"})
			if err != nil {
				t.Fatalf("resolve open: %v", err)
			}
			req := cloud.ShellLaunchParamsFromResult(result)
			req.AI = true
			body := strings.Join(open.RemoteSessionLauncherBody(req, "/tmp/bashrc"), "\n")

			if !strings.Contains(body, "--model "+tc.model) {
				t.Fatalf("the AI session should launch on %s:\n%s", tc.model, body)
			}
			if !strings.Contains(body, "--effort low") {
				t.Fatalf("the AI session should launch at the environment's effort:\n%s", body)
			}
			if got := strings.Contains(body, "--remote-control"); got != tc.remoteControl {
				t.Fatalf("Remote Control = %v, want %v:\n%s", got, tc.remoteControl, body)
			}
			if got := strings.Contains(body, "CLAUDE_CODE_MAX_CONTEXT_TOKENS=200000"); got != tc.context {
				t.Fatalf("catalog context window = %v, want %v:\n%s", got, tc.context, body)
			}
		})
	}
}

// TestOpenWithoutAReadableRootConfigLaunchesWithoutAGateway keeps the tolerant
// read: a root config that cannot be read must not fail the open, and leaves
// the launch on its gateway-less answer.
func TestOpenWithoutAReadableRootConfigLaunchesWithoutAGateway(t *testing.T) {
	if got := shared.ResolveOpenGateway(openTenantResolutionStore{}); got != nil {
		t.Fatalf("an unreadable root config should resolve no gateway, got %+v", got)
	}
}
