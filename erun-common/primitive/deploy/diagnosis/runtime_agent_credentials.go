package diagnosis

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/deploy/observe"
)

// A pod template predating the chart's gateway block drops the gateway values
// silently yet deploys healthy. This read flags only a disagreement with the
// gateway erun configured (via EffectiveGateway); an env without one is not broken.

// GatewayBaseURLEnv is rendered once a gateway is configured; the runtime settings relay keys off it too.
const GatewayBaseURLEnv = "ANTHROPIC_BASE_URL"

// RuntimeAgentCredentialParams carries ContainerEnvObserved so an unread template
// is distinguishable from an empty one.
type RuntimeAgentCredentialParams struct {
	Claude               config.EnvironmentClaudeConfig
	Gateway              *config.OpenRouterConfig
	Container            string
	ContainerEnvObserved bool
	// ContainerEnvNames holds names only; no value is read or reported.
	ContainerEnvNames []string
}

// ResolveRuntimeAgentCredentialStatus is pure and never infers Missing from an unread template.
func ResolveRuntimeAgentCredentialStatus(params RuntimeAgentCredentialParams) model.RuntimeAgentCredentialStatus {
	status := model.RuntimeAgentCredentialStatus{Container: strings.TrimSpace(params.Container)}
	gateway := config.EffectiveGateway(params.Claude, params.Gateway)
	if !gateway.Configured() {
		status.State = model.RuntimeAgentCredentialNotApplicable
		return status
	}
	status.GatewayBaseURL = gateway.Endpoint()
	if !params.ContainerEnvObserved {
		status.State = model.RuntimeAgentCredentialUnknown
		return status
	}
	for _, name := range params.ContainerEnvNames {
		if strings.TrimSpace(name) == GatewayBaseURLEnv {
			status.State = model.RuntimeAgentCredentialConfigured
			return status
		}
	}
	status.State = model.RuntimeAgentCredentialMissing
	return status
}

// runtimeDeploymentEnvArgs reads the Deployment, not a pod: its template is what
// the chart rendered, independent of rollout progress.
func runtimeDeploymentEnvArgs(req config.ShellLaunchParams) []string {
	args := config.KubectlTargetArgs(req)
	return append(args, "get", "deployment", config.RuntimeReleaseName(req.Tenant), "-o", "json")
}

type runtimeDeploymentDoc struct {
	Spec struct {
		Template struct {
			Spec struct {
				Containers []runtimeDeploymentContainer `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

type runtimeDeploymentContainer struct {
	Name string `json:"name"`
	Env  []struct {
		Name string `json:"name"`
	} `json:"env"`
}

// RuntimeDeploymentContainerEnvNames returns the container's explicit env names;
// found is false when no such container exists. The credential is a secretKeyRef,
// so names suffice without touching its value.
func RuntimeDeploymentContainerEnvNames(raw []byte, container string) (names []string, found bool) {
	var doc runtimeDeploymentDoc
	if json.Unmarshal(raw, &doc) != nil {
		return nil, false
	}
	for _, candidate := range doc.Spec.Template.Spec.Containers {
		if strings.TrimSpace(candidate.Name) != container {
			continue
		}
		names = make([]string, 0, len(candidate.Env))
		for _, entry := range candidate.Env {
			if name := strings.TrimSpace(entry.Name); name != "" {
				names = append(names, name)
			}
		}
		return names, true
	}
	return nil, false
}

// runtimeAgentCredentialApplicability resolves NotApplicable or Unknown without
// reading, so an unreachable cluster still distinguishes the two.
func runtimeAgentCredentialApplicability(req config.ShellLaunchParams) model.RuntimeAgentCredentialStatus {
	return ResolveRuntimeAgentCredentialStatus(RuntimeAgentCredentialParams{
		Claude:    req.Claude,
		Gateway:   req.Gateway,
		Container: config.DevopsComponentName,
	})
}

// inspectRuntimeAgentCredentials skips the read entirely when no gateway is configured.
func inspectRuntimeAgentCredentials(ctx core.Context, req config.ShellLaunchParams) model.RuntimeAgentCredentialStatus {
	params := RuntimeAgentCredentialParams{
		Claude:    req.Claude,
		Gateway:   req.Gateway,
		Container: config.DevopsComponentName,
	}
	if !config.EffectiveGateway(req.Claude, req.Gateway).Configured() {
		return ResolveRuntimeAgentCredentialStatus(params)
	}

	args := runtimeDeploymentEnvArgs(req)
	ctx.TraceCommand("", "kubectl", args...)
	stdout, stderr, err := observe.RunObserveKubectl(args)
	if err != nil {
		status := ResolveRuntimeAgentCredentialStatus(params)
		status.ReadError = fmt.Sprintf("read the runtime pod template: %v", observe.KubectlErrorMessage(err, stderr))
		return status
	}
	names, found := RuntimeDeploymentContainerEnvNames(stdout, config.DevopsComponentName)
	if !found {
		status := ResolveRuntimeAgentCredentialStatus(params)
		status.ReadError = fmt.Sprintf("the runtime deployment %q declares no %q container", config.RuntimeReleaseName(req.Tenant), config.DevopsComponentName)
		return status
	}
	params.ContainerEnvObserved = true
	params.ContainerEnvNames = names
	return ResolveRuntimeAgentCredentialStatus(params)
}
