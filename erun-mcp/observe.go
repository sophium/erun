package erunmcp

import (
	"context"
	"io"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type ObserveSecretCheckInput struct {
	Name string `json:"name" jsonschema:"Secret name to check for existence"`
	Key  string `json:"key" jsonschema:"data key whose presence to check; the value itself is never read or returned"`
}

type ObserveInput struct {
	Tenant      string                    `json:"tenant,omitempty" jsonschema:"tenant whose environment should be observed; defaults to the server tenant context"`
	Environment string                    `json:"environment,omitempty" jsonschema:"environment to observe; defaults to the server environment context"`
	Secrets     []ObserveSecretCheckInput `json:"secrets,omitempty" jsonschema:"named Secret/key pairs to check for presence, without reading their values"`
	Preview     bool                      `json:"preview,omitempty" jsonschema:"when true, resolve and trace the kubectl calls that would run without executing them"`
	Verbosity   int                       `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

// observeTool is read-only by construction: RunObservation issues only
// `kubectl get`, so this tool carries the erun:read capability rather than
// erun:admin (see mcpReadOnlyTools), unlike raw or doctor's recovery actions.
func observeTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ObserveInput) (*mcp.CallToolResult, eruncommon.ObserveResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ObserveInput) (*mcp.CallToolResult, eruncommon.ObserveResult, error) {
		target, err := resolveObserveOpenResult(runtime, input)
		if err != nil {
			return nil, eruncommon.ObserveResult{}, err
		}
		req := eruncommon.ShellLaunchParamsFromResult(target)
		runCtx := runtimeCallContext(input.Preview, input.Verbosity, nil, io.Discard, io.Discard)
		result, err := eruncommon.RunObservation(runCtx, req, eruncommon.ObserveParams{Secrets: observeSecretChecksFromInput(input.Secrets)})
		if err != nil {
			return nil, eruncommon.ObserveResult{}, err
		}
		return nil, result, nil
	}
}

func observeSecretChecksFromInput(inputs []ObserveSecretCheckInput) []eruncommon.ObserveSecretCheck {
	checks := make([]eruncommon.ObserveSecretCheck, 0, len(inputs))
	for _, in := range inputs {
		checks = append(checks, eruncommon.ObserveSecretCheck{Name: strings.TrimSpace(in.Name), Key: strings.TrimSpace(in.Key)})
	}
	return checks
}

// resolveObserveOpenResult mirrors resolveDoctorOpenResult's explicit ->
// partial -> runtime-context -> default fallback chain, so `observe` resolves
// tenant/environment/namespace the same way every other typed MCP tool does.
func resolveObserveOpenResult(runtime RuntimeConfig, input ObserveInput) (eruncommon.OpenResult, error) {
	tenant := strings.TrimSpace(input.Tenant)
	environment := strings.TrimSpace(input.Environment)
	switch {
	case tenant != "" && environment != "":
		return eruncommon.ResolveOpen(runtime.Store, eruncommon.OpenParams{Tenant: tenant, Environment: environment})
	case tenant != "":
		return eruncommon.ResolveOpen(runtime.Store, eruncommon.OpenParams{Tenant: tenant, UseDefaultEnvironment: true})
	case environment != "":
		return eruncommon.ResolveOpen(runtime.Store, eruncommon.OpenParams{Environment: environment, UseDefaultTenant: true})
	}

	runtimeTenant := strings.TrimSpace(runtime.Context.Tenant)
	runtimeEnvironment := strings.TrimSpace(runtime.Context.Environment)
	if runtimeTenant != "" && runtimeEnvironment != "" {
		return eruncommon.ResolveOpen(runtime.Store, eruncommon.OpenParams{Tenant: runtimeTenant, Environment: runtimeEnvironment})
	}

	return eruncommon.ResolveOpen(runtime.Store, eruncommon.OpenParams{UseDefaultTenant: true, UseDefaultEnvironment: true})
}
