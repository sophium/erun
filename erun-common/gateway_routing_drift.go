package eruncommon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// GatewayRouting is where an environment's running pod sends Claude Code
// compared with where its saved configuration says it should.
//
// The gateway is rendered into the runtime Deployment at deploy time, so
// changing the erun-level catalog or an environment's opt-out only edits
// config: the pod keeps routing where it was deployed to until it is
// redeployed. This is what lets a caller say so instead of leaving the switch
// looking like it did nothing.
type GatewayRouting struct {
	// Deployed is the base URL the running Deployment hands Claude Code, "" when
	// it routes to Anthropic directly.
	Deployed string
	// Wanted is the base URL the saved configuration resolves to, "" when the
	// environment should not use a gateway.
	Wanted string
}

// Drifted reports whether the running pod routes somewhere the saved
// configuration no longer asks for, which only a redeploy applies.
func (r GatewayRouting) Drifted() bool {
	return normalizeGatewayEndpoint(r.Deployed) != normalizeGatewayEndpoint(r.Wanted)
}

// WantedGatewayEndpoint is the base URL deploy renders for an environment, the
// same resolution the chart values use.
func WantedGatewayEndpoint(claude config.EnvironmentClaudeConfig, catalog *config.OpenRouterConfig) string {
	return config.EffectiveGateway(claude, catalog).Endpoint()
}

func normalizeGatewayEndpoint(endpoint string) string {
	return strings.TrimRight(strings.TrimSpace(endpoint), "/")
}

// deploymentGatewayEndpoint reads ANTHROPIC_BASE_URL from a Deployment's pod
// template. A template that sets none routes to Anthropic directly.
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

// loadRuntimeDeploymentJSON fetches the runtime Deployment. found is false when
// the environment has no runtime deployed, which leaves nothing to drift.
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

// ReadGatewayRouting compares the running runtime pod of tenant/environment
// with its saved configuration. found is false when no runtime is deployed.
func ReadGatewayRouting(ctx context.Context, env config.EnvConfig, tenant string, catalog *config.OpenRouterConfig) (routing GatewayRouting, found bool, err error) {
	routing.Wanted = WantedGatewayEndpoint(env.Claude, catalog)
	namespace := config.KubernetesNamespaceName(tenant, env.Name)
	raw, found, err := loadRuntimeDeploymentJSON(ctx, env.KubernetesContext, namespace, config.RuntimeReleaseName(tenant))
	if err != nil || !found {
		return routing, found, err
	}
	routing.Deployed, err = deploymentGatewayEndpoint(raw)
	return routing, err == nil, err
}
