package eruncommon

import (
	"encoding/json"
	"fmt"
	"strings"
)

// An environment's ability to run an in-pod agent is a property of its deployed
// pod template, not of anything erun records about the environment. The runtime
// chart renders the model-credential wiring into the container's environment
// only from a template new enough to consume the gateway values erun already
// passes it (erun-devops/k8s/erun-devops/templates/service.yaml, whose gateway
// block is gated on claude.openRouterBaseURL). A template that predates that
// block drops those values silently: the deploy succeeds, the release reports
// deployed, the pod is Running and Ready, and the environment can start no
// agent at all -- a state an operator then meets as whatever error the model
// client emits with no provider configured, which is a statement about the
// client's guess rather than about this environment.
//
// Nothing in the deploy diagnosis read a container's environment, so no surface
// could tell that state apart from a healthy one. This file is that read. It is
// deliberately a *disagreement* test rather than a presence test: an environment
// with no gateway catalog is not broken -- it may run on Bedrock, on injected
// host credentials, or on a sign-in stored in its own home volume -- and erun
// has no business asserting such an environment cannot run an agent. What erun
// does know is the gateway it configured for this environment, so the honest
// finding is the disagreement between that intent and the deployed template.
// The intent is resolved through EffectiveGateway, the same resolver deploy
// itself uses, so the diagnosis cannot drift from what a deploy would do.

// GatewayBaseURLEnv is the container environment variable the runtime chart
// renders once a gateway is configured for the environment. Its presence is the
// pod-side signal that the deployed template consumed the gateway values; the
// image's own config relay keys off the same variable (initialize_claude_config
// computes configureGateway from it), so there is no separate flag to add here.
const GatewayBaseURLEnv = "ANTHROPIC_BASE_URL"

// RuntimeAgentCredentialState is the resolved verdict on whether an
// environment's runtime pod carries the model-provider wiring erun configured
// for it.
type RuntimeAgentCredentialState string

const (
	// RuntimeAgentCredentialNotApplicable is the answer for an environment no
	// gateway routes. It asserts nothing about agent capability: an environment
	// outside the catalog may reach a provider by other means, and a diagnosis
	// has no standing to call that broken.
	RuntimeAgentCredentialNotApplicable RuntimeAgentCredentialState = "not-applicable"
	// RuntimeAgentCredentialConfigured is the healthy answer for a
	// gateway-routed environment: the deployed pod template carries the routing
	// variable.
	RuntimeAgentCredentialConfigured RuntimeAgentCredentialState = "configured"
	// RuntimeAgentCredentialMissing is the defect this read exists for: a
	// gateway routes this environment, and the deployed pod template carries no
	// routing variable. The container has no provider and no credential, so no
	// agent job in it can serve a turn.
	RuntimeAgentCredentialMissing RuntimeAgentCredentialState = "missing"
	// RuntimeAgentCredentialUnknown is "could not tell" -- the template could
	// not be read, or was not the shape this read recognises. It is never
	// reported as Missing: an unread template is not evidence of absence.
	RuntimeAgentCredentialUnknown RuntimeAgentCredentialState = "unknown"
)

// RuntimeAgentCredentialStatus is the read-only answer to "can this
// environment's runtime pod reach the model provider erun configured for it?".
type RuntimeAgentCredentialStatus struct {
	State RuntimeAgentCredentialState `json:"state"`
	// GatewayBaseURL is the gateway this environment is routed through, empty
	// when none applies. It is a routing address, never a credential.
	GatewayBaseURL string `json:"gatewayBaseURL,omitempty"`
	// Container is the pod-template container the environment was read from,
	// empty when no read was needed.
	Container string `json:"container,omitempty"`
	// ReadError is non-empty when the template's environment could not be
	// observed at all -- a failed read, or a pod template with no such
	// container. It is never set for a template that was read and genuinely
	// carries nothing; that is Missing.
	ReadError string `json:"readError,omitempty"`
}

// RuntimeAgentCredentialParams is the already-resolved input set
// ResolveRuntimeAgentCredentialStatus composes. ContainerEnvObserved carries its
// own bit so a caller that could not read the template says so, instead of
// leaving an empty name list indistinguishable from a template that really
// carries nothing.
type RuntimeAgentCredentialParams struct {
	Claude  EnvironmentClaudeConfig
	Gateway *OpenRouterConfig
	// Container names the container whose environment was read.
	Container string
	// ContainerEnvObserved is true only when the deployed pod template's
	// container environment was actually read.
	ContainerEnvObserved bool
	// ContainerEnvNames holds the variable names that environment sets. Names
	// only: a name is what decides routing, and no value is read, kept, or
	// reported anywhere along this path.
	ContainerEnvNames []string
}

// ResolveRuntimeAgentCredentialStatus is pure: it reaches for nothing it was
// not given, and in particular never infers a missing credential from an unread
// template.
func ResolveRuntimeAgentCredentialStatus(params RuntimeAgentCredentialParams) RuntimeAgentCredentialStatus {
	status := RuntimeAgentCredentialStatus{Container: strings.TrimSpace(params.Container)}
	gateway := EffectiveGateway(params.Claude, params.Gateway)
	if !gateway.Configured() {
		status.State = RuntimeAgentCredentialNotApplicable
		return status
	}
	status.GatewayBaseURL = gateway.Endpoint()
	if !params.ContainerEnvObserved {
		status.State = RuntimeAgentCredentialUnknown
		return status
	}
	for _, name := range params.ContainerEnvNames {
		if strings.TrimSpace(name) == GatewayBaseURLEnv {
			status.State = RuntimeAgentCredentialConfigured
			return status
		}
	}
	status.State = RuntimeAgentCredentialMissing
	return status
}

// runtimeDeploymentEnvArgs builds the read-only `kubectl get deployment` that
// returns the deployed pod template. The Deployment is the right subject rather
// than a running pod: the template is what the chart rendered, so it is the
// artifact whose age is the whole question, and it does not depend on which
// ReplicaSet a rollout happens to have reached.
func runtimeDeploymentEnvArgs(req ShellLaunchParams) []string {
	args := kubectlTargetArgs(req)
	return append(args, "get", "deployment", RuntimeReleaseName(req.Tenant), "-o", "json")
}

// runtimeDeploymentDoc is the deliberately partial parse of that read: the only
// thing wanted from a Deployment here is its containers' environment variable
// names.
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

// runtimeDeploymentContainerEnvNames returns the environment variable names the
// named container's template declares. found is false when the template carries
// no such container, which is a shape this read does not recognise rather than
// an environment that carries nothing.
//
// It reads explicit `env` entries only. That is the whole contract the chart
// has with this read: the credential itself arrives as a secretKeyRef, so the
// container's environment is the one place erun can check for the routing
// variable without ever handling the secret's value.
func runtimeDeploymentContainerEnvNames(raw []byte, container string) (names []string, found bool) {
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

// runtimeAgentCredentialApplicability answers whether the check applies to this
// environment at all, without reading anything: an environment no gateway
// routes resolves NotApplicable, and a routed one is left Unknown -- "not
// observed" -- for the read to resolve. It is what a diagnosis that never got
// to read (an unreachable cluster) still reports, so a caller can tell a check
// that does not apply from one that could not run.
func runtimeAgentCredentialApplicability(req ShellLaunchParams) RuntimeAgentCredentialStatus {
	return ResolveRuntimeAgentCredentialStatus(RuntimeAgentCredentialParams{
		Claude:    req.Claude,
		Gateway:   req.Gateway,
		Container: DevopsComponentName,
	})
}

// inspectRuntimeAgentCredentials reads the deployed pod template's container
// environment and resolves the verdict.
//
// An install with no gateway configured pays nothing: the read is skipped
// outright rather than resolved from an environment that was never fetched.
func inspectRuntimeAgentCredentials(ctx Context, req ShellLaunchParams) RuntimeAgentCredentialStatus {
	params := RuntimeAgentCredentialParams{
		Claude:    req.Claude,
		Gateway:   req.Gateway,
		Container: DevopsComponentName,
	}
	if !EffectiveGateway(req.Claude, req.Gateway).Configured() {
		return ResolveRuntimeAgentCredentialStatus(params)
	}

	args := runtimeDeploymentEnvArgs(req)
	ctx.TraceCommand("", "kubectl", args...)
	stdout, stderr, err := runObserveKubectl(args)
	if err != nil {
		status := ResolveRuntimeAgentCredentialStatus(params)
		status.ReadError = fmt.Sprintf("read the runtime pod template: %v", kubectlErrorMessage(err, stderr))
		return status
	}
	names, found := runtimeDeploymentContainerEnvNames(stdout, DevopsComponentName)
	if !found {
		status := ResolveRuntimeAgentCredentialStatus(params)
		status.ReadError = fmt.Sprintf("the runtime deployment %q declares no %q container", RuntimeReleaseName(req.Tenant), DevopsComponentName)
		return status
	}
	params.ContainerEnvObserved = true
	params.ContainerEnvNames = names
	return ResolveRuntimeAgentCredentialStatus(params)
}
