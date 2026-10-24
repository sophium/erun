package whip

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/command/orchestrator"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// WhipInput mirrors `erun whip`; tenant/environment must name this server's own environment.
type WhipInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment to whip; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to whip; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	Preview     bool   `json:"preview,omitempty" jsonschema:"when true, resolve and report what the whip would decide (pushed, skipped, capped) without writing anything into the session"`
}

// whipTool nudges this environment's AI session now. It ignores staleness but
// respects the consecutive-nudge cap.
func whipTool(runtime shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, WhipInput) (*mcp.CallToolResult, orchestrator.WhipResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input WhipInput) (*mcp.CallToolResult, orchestrator.WhipResult, error) {
		tenant, environment, err := shared.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, orchestrator.WhipResult{}, err
		}
		// Best-effort: an unreadable root config falls back to default whip settings.
		globalConfig, _, _ := runtime.Store.LoadERunConfig()
		cfg := orchestrator.ResolveWhipConfig(globalConfig.Whip)
		result, err := orchestrator.RunLocalEnvironmentWhip(time.Now(), orchestrator.RunWhipCommand, tenant, environment, cfg, true, input.Preview)
		if err != nil {
			return nil, orchestrator.WhipResult{}, err
		}
		return nil, result, nil
	}
}

func RegisterWhipTools(reg shared.ToolRegistrar, runtime shared.RuntimeConfig) {
	shared.AddTool(reg, &mcp.Tool{
		Name: "whip",
		Description: "Push this environment's own AI session with the pacing nudge, on demand. Reports what it decided " +
			"(pushed, skipped because the session isn't alive, or skipped because it already hit the consecutive-nudge cap) " +
			"rather than only reporting that it ran. Set preview=true to resolve the decision without writing anything.",
	}, whipTool(runtime))
}
