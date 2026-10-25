package gatewayrouting

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/openrouter"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/core"
)

// GatewayRouting compares where the running pod sends Claude Code with where config says it
// should; the gateway is rendered at deploy time, so config changes apply only on redeploy.
type GatewayRouting struct {
	// Deployed is "" when the pod routes to Anthropic directly.
	Deployed string
	// Wanted is "" when the environment should not use a gateway.
	Wanted string
}

// Drifted reports whether the running pod routes somewhere config no longer asks for.
func (r GatewayRouting) Drifted() bool {
	return normalizeGatewayEndpoint(r.Deployed) != normalizeGatewayEndpoint(r.Wanted)
}

// WantedGatewayEndpoint is the base URL deploy renders for an environment.
func WantedGatewayEndpoint(claude config.EnvironmentClaudeConfig, catalog *config.OpenRouterConfig) string {
	return openrouter.EffectiveGateway(claude, catalog).Endpoint()
}

func normalizeGatewayEndpoint(endpoint string) string {
	return strings.TrimRight(strings.TrimSpace(endpoint), "/")
}

func deploymentGatewayEndpoint(deploymentJSON []byte) (string, error) {
	var deployment struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Env []struct {
							Name  string `json:"name"`
							Value string `json:"value"`
						} `json:"env"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(deploymentJSON, &deployment); err != nil {
		return "", fmt.Errorf("decode deployment: %w", err)
	}
	for _, container := range deployment.Spec.Template.Spec.Containers {
		for _, env := range container.Env {
			if env.Name == "ANTHROPIC_BASE_URL" {
				return strings.TrimSpace(env.Value), nil
			}
		}
	}
	return "", nil
}

// found is false when no runtime is deployed.
var loadRuntimeDeploymentJSON = func(ctx context.Context, kubernetesContext, namespace, deployment string) ([]byte, bool, error) {
	args := []string{}
	if c := strings.TrimSpace(kubernetesContext); c != "" {
		args = append(args, "--context", c)
	}
	args = append(args, "-n", namespace, "get", "deployment", deployment, "--ignore-not-found", "-o", "json")
	cmd := core.CommandContext(ctx, "kubectl", args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, false, fmt.Errorf("read deployment %s/%s: %w", namespace, deployment, err)
	}
	if strings.TrimSpace(string(output)) == "" {
		return nil, false, nil
	}
	return output, true, nil
}

// ReadGatewayRouting compares the running runtime pod with its saved configuration.
func ReadGatewayRouting(ctx context.Context, env config.EnvConfig, tenant string, catalog *config.OpenRouterConfig) (routing GatewayRouting, found bool, err error) {
	routing.Wanted = WantedGatewayEndpoint(env.Claude, catalog)
	namespace := runtimetarget.KubernetesNamespaceName(tenant, env.Name)
	raw, found, err := loadRuntimeDeploymentJSON(ctx, env.KubernetesContext, namespace, runtimetarget.RuntimeReleaseName(tenant))
	if err != nil || !found {
		return routing, found, err
	}
	routing.Deployed, err = deploymentGatewayEndpoint(raw)
	return routing, err == nil, err
}
