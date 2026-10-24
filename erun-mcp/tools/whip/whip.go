package whip

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/command/orchestrator"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// WhipInput mirrors the CLI's `erun whip` flags. tenant/environment must name
// this server's own environment (see resolveLocalTarget) -- an MCP server runs
// inside exactly one pod, so it can only ever push that pod's own AI session.
type WhipInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment to whip; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to whip; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	Preview     bool   `json:"preview,omitempty" jsonschema:"when true, resolve and report what the whip would decide (pushed, skipped, capped) without writing anything into the session"`
}

// whipTool re-states the pacing contract into this environment's own AI
// session, on demand rather than waiting for the desktop's own schedule-driven
// pass. It always acts explicitly (ignores staleness) but never bypasses the
// consecutive-nudge cap: a capped session stays capped until it shows fresh
// activity on its own.
func whipTool(runtime shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, WhipInput) (*mcp.CallToolResult, orchestrator.WhipResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input WhipInput) (*mcp.CallToolResult, orchestrator.WhipResult, error) {
		tenant, environment, err := shared.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, orchestrator.WhipResult{}, err
		}
		// Best-effort: a missing or unreadable root config resolves to the
		// zero ERunConfig, whose nil Whip override keeps ResolveWhipConfig on
		// today's defaults -- the same "unconfigured install is unaffected"
		// contract the CLI and the desktop both rely on.
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
