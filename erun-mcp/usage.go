package erunmcp

import (
	"context"
	"io"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type UsageInput struct {
	Tenant          string  `json:"tenant,omitempty" jsonschema:"tenant whose environment's usage should be read; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment     string  `json:"environment,omitempty" jsonschema:"environment whose usage should be read; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	IntervalSeconds float64 `json:"intervalSeconds,omitempty" jsonschema:"CPU sample window in seconds (clamped to 0.1-30, default 1): usage is read, the window elapses, then it is read again so utilisation is a rate rather than a meaningless cumulative counter"`
	Preview         bool    `json:"preview,omitempty" jsonschema:"when true, resolve and trace the kubectl exec call that would run without executing it"`
	Verbosity       int     `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

// UsageOutput carries the live reading plus the environment's standing sizing
// recommendation -- the same verdict and evidence `erun list` reports under
// `runtime-pod:` -- so a caller checking on an environment's health learns
// both numbers in one call instead of a separate `resize --preview` just to
// see the reasoning. Embeds RuntimeUsage so every existing field stays at the
// top level; Sizing is additive.
type UsageOutput struct {
	eruncommon.RuntimeUsage
	Sizing *eruncommon.RuntimeSizingRecommendation `json:"sizing,omitempty"`
}

// usageTool reads CPU quota utilisation, memory against the container's own
// cgroup limit, and disk usage for the workspace mount, straight from the
// runtime container's cgroup v2 files -- no metrics-server required, unlike
// `kubectl top`. It carries the erun:read capability (see mcpReadOnlyTools):
// the exec runs a single fixed diagnostic script, never caller-supplied argv,
// so it is safe to grant an orchestrator that must never reach `exec raw`.
//
// On a build-capable environment, this reading cannot see the erun-dind
// sidecar an image build actually runs in -- a separate cgroup, not a
// descendant of this container's -- so `excludesBuilds` is true in the
// output on every environment that carries one, naming the gap instead of
// letting the reading imply the environment is idle.
func usageTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, UsageInput) (*mcp.CallToolResult, UsageOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input UsageInput) (*mcp.CallToolResult, UsageOutput, error) {
		target, err := resolveUsageOpenResult(runtime, input)
		if err != nil {
			return nil, UsageOutput{}, err
		}
		req := eruncommon.ShellLaunchParamsFromResult(target)
		runCtx := runtimeCallContext(input.Preview, input.Verbosity, nil, io.Discard, io.Discard)
		params := eruncommon.RuntimeUsageParams{Interval: usageIntervalFromSeconds(input.IntervalSeconds)}
		result, err := eruncommon.RunRuntimeUsage(runCtx, nil, req, params)
		if err != nil {
			return nil, UsageOutput{}, err
		}
		sizing := eruncommon.EnvironmentRuntimeSizing(target.Tenant, target.EnvConfig)
		return nil, UsageOutput{RuntimeUsage: result, Sizing: sizing}, nil
	}
}

func usageIntervalFromSeconds(seconds float64) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

// resolveUsageOpenResult resolves tenant/environment through
// resolveLocalTarget -- the same refusal every other typed MCP tool in this
// module applies -- before asking the store to resolve the rest of the
// OpenResult, so `usage` can never be pointed at a different environment than
// the one this server's pod actually runs in.
func resolveUsageOpenResult(runtime RuntimeConfig, input UsageInput) (eruncommon.OpenResult, error) {
	tenant, environment, err := resolveLocalTarget(runtime, input.Tenant, input.Environment)
	if err != nil {
		return eruncommon.OpenResult{}, err
	}
	return eruncommon.ResolveOpen(runtime.Store, eruncommon.OpenParams{Tenant: tenant, Environment: environment})
}
