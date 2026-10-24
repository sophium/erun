package observe

import (
	"context"
	"io"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/observe"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared"
	sharedpkg "github.com/sophium/erun/erun-mcp/tools/shared"
)

type ObserveSecretCheckInput struct {
	Name string `json:"name" jsonschema:"Secret name to check for existence"`
	Key  string `json:"key" jsonschema:"data key whose presence to check; the value itself is never read or returned"`
}

type ObserveInput struct {
	Tenant      string                    `json:"tenant,omitempty" jsonschema:"tenant whose environment should be observed; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string                    `json:"environment,omitempty" jsonschema:"environment to observe; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	Secrets     []ObserveSecretCheckInput `json:"secrets,omitempty" jsonschema:"named Secret/key pairs to check for presence, without reading their values"`
	Preview     bool                      `json:"preview,omitempty" jsonschema:"when true, resolve and trace the kubectl calls that would run without executing them"`
	Verbosity   int                       `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

// ObserveTool only runs `kubectl get`, so it needs erun:read, not erun:admin.
func ObserveTool(runtime sharedpkg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ObserveInput) (*mcp.CallToolResult, observe.ObserveResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ObserveInput) (*mcp.CallToolResult, observe.ObserveResult, error) {
		target, err := resolveObserveOpenResult(runtime, input)
		if err != nil {
			return nil, observe.ObserveResult{}, err
		}
		req := cloud.ShellLaunchParamsFromResult(target)
		runCtx := sharedpkg.RuntimeCallContext(input.Preview, input.Verbosity, nil, io.Discard, io.Discard)
		result, err := observe.RunObservation(runCtx, req, observe.ObserveParams{Secrets: observeSecretChecksFromInput(input.Secrets)})
		if err != nil {
			return nil, observe.ObserveResult{}, err
		}
		return nil, result, nil
	}
}

func observeSecretChecksFromInput(inputs []ObserveSecretCheckInput) []observe.ObserveSecretCheck {
	checks := make([]observe.ObserveSecretCheck, 0, len(inputs))
	for _, in := range inputs {
		checks = append(checks, observe.ObserveSecretCheck{Name: strings.TrimSpace(in.Name), Key: strings.TrimSpace(in.Key)})
	}
	return checks
}

// resolveObserveOpenResult pins tenant/environment to this pod's environment.
func resolveObserveOpenResult(runtime sharedpkg.RuntimeConfig, input ObserveInput) (config.OpenResult, error) {
	tenant, environment, err := sharedpkg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
	if err != nil {
		return config.OpenResult{}, err
	}
	return open.ResolveOpen(runtime.Store, shared.OpenParams{Tenant: tenant, Environment: environment})
}
