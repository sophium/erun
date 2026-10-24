package usage

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/podruntime"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

type UsageInput struct {
	Tenant          string  `json:"tenant,omitempty" jsonschema:"tenant whose environment's usage should be read; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment     string  `json:"environment,omitempty" jsonschema:"environment whose usage should be read; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	IntervalSeconds float64 `json:"intervalSeconds,omitempty" jsonschema:"CPU sample window in seconds (clamped to 0.1-30, default 1): usage is read, the window elapses, then it is read again so utilisation is a rate rather than a meaningless cumulative counter"`
	Preview         bool    `json:"preview,omitempty" jsonschema:"when true, resolve and trace the kubectl exec call that would run without executing it"`
	Verbosity       int     `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

// UsageOutput aliases the shared report so the reading and the sizing
// recommendation come from one body of evidence.
type UsageOutput = model.RuntimeUsageReport

// UsageSizingSurfaces is embedded in the tool description so it cannot drift from the surfaces.
var UsageSizingSurfaces = []string{
	"this tool",
	"`erun usage`",
	"`erun doctor`",
	"`erun list`'s `runtime-pod:` block",
}

// UsageSizingClaim is the description sentence; exported so the guard compares the same text.
func UsageSizingClaim() string {
	joined := strings.Join(UsageSizingSurfaces[:len(UsageSizingSurfaces)-1], ", ")
	joined += ", and " + UsageSizingSurfaces[len(UsageSizingSurfaces)-1]
	return "the block rides on every reading that can see it: " + joined +
		" all render the one recommendation resolved from the one body of evidence"
}

// UsageTool reads cgroup v2 usage directly (no metrics-server). It runs a fixed
// script, never caller argv, so it is erun:read. Builds run in the erun-dind
// sidecar, reported separately as `dind`.
func UsageTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, UsageInput) (*mcp.CallToolResult, UsageOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input UsageInput) (*mcp.CallToolResult, UsageOutput, error) {
		target, err := resolveUsageOpenResult(runtime, input)
		if err != nil {
			return nil, UsageOutput{}, err
		}
		req := cloud.ShellLaunchParamsFromResult(target)
		runCtx := toolio.RuntimeCallContext(input.Preview, input.Verbosity, nil, io.Discard, io.Discard)
		params := model.RuntimeUsageParams{Interval: usageIntervalFromSeconds(input.IntervalSeconds)}
		result, err := podruntime.RunRuntimeUsage(runCtx, nil, nil, req, params)
		if err != nil {
			return nil, UsageOutput{}, err
		}
		return nil, podruntime.ResolveRuntimeUsageReport(target.Tenant, target.EnvConfig, result), nil
	}
}

func usageIntervalFromSeconds(seconds float64) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

// resolveUsageOpenResult applies ResolveLocalTarget's refusal first so usage
// cannot target another environment.
func resolveUsageOpenResult(runtime runtimecfg.RuntimeConfig, input UsageInput) (config.OpenResult, error) {
	tenant, environment, err := runtimecfg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
	if err != nil {
		return config.OpenResult{}, err
	}
	return resolve.ResolveOpen(runtime.Store, model.OpenParams{Tenant: tenant, Environment: environment})
}
