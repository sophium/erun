package erunmcp

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

type UsageInput struct {
	Tenant          string  `json:"tenant,omitempty" jsonschema:"tenant whose environment's usage should be read; defaults to the server tenant context"`
	Environment     string  `json:"environment,omitempty" jsonschema:"environment whose usage should be read; defaults to the server environment context"`
	IntervalSeconds float64 `json:"intervalSeconds,omitempty" jsonschema:"CPU sample window in seconds (clamped to 0.1-30, default 1): usage is read, the window elapses, then it is read again so utilisation is a rate rather than a meaningless cumulative counter"`
	Preview         bool    `json:"preview,omitempty" jsonschema:"when true, resolve and trace the kubectl exec call that would run without executing it"`
	Verbosity       int     `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

// usageTool reads CPU quota utilisation, memory against the container's own
// cgroup limit, and disk usage for the workspace mount, straight from the
// runtime container's cgroup v2 files -- no metrics-server required, unlike
// `kubectl top`. It carries the erun:read capability (see mcpReadOnlyTools):
// the exec runs a single fixed diagnostic script, never caller-supplied argv,
// so it is safe to grant an orchestrator that must never reach `exec raw`.
func usageTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, UsageInput) (*mcp.CallToolResult, eruncommon.RuntimeUsage, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input UsageInput) (*mcp.CallToolResult, eruncommon.RuntimeUsage, error) {
		target, err := resolveUsageOpenResult(runtime, input)
		if err != nil {
			return nil, eruncommon.RuntimeUsage{}, err
		}
		req := eruncommon.ShellLaunchParamsFromResult(target)
		runCtx := runtimeCallContext(input.Preview, input.Verbosity, nil, io.Discard, io.Discard)
		params := eruncommon.RuntimeUsageParams{Interval: usageIntervalFromSeconds(input.IntervalSeconds)}
		result, err := eruncommon.RunRuntimeUsage(runCtx, nil, req, params)
		if err != nil {
			return nil, eruncommon.RuntimeUsage{}, err
		}
		return nil, result, nil
	}
}

func usageIntervalFromSeconds(seconds float64) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

// resolveUsageOpenResult mirrors resolveObserveOpenResult's explicit ->
// partial -> runtime-context -> default fallback chain, so `usage` resolves
// tenant/environment the same way every other typed MCP tool does.
func resolveUsageOpenResult(runtime RuntimeConfig, input UsageInput) (eruncommon.OpenResult, error) {
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
