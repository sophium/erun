package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-common/infra/mcpauth"
	"github.com/sophium/erun/erun-common/primitive/shared"
	"github.com/sophium/erun/internal"
)

// mcpChannelUnreachableExitCode marks a channel still unreachable after one reattach,
// so a polling caller cannot mistake it for a finished job or failed tool.
const mcpChannelUnreachableExitCode = 126

// ReattachMCPChannel is a variable so tests can assert --dry-run never spawns it.
var ReattachMCPChannel = ReattachEnvironmentMCPChannel

// CallMCPToolWithReattach calls an environment's MCP edge, reattaching once via
// `erun open --reconnect` on a dropped channel; a bare `erun open` would restart a stopped env.
func CallMCPToolWithReattach(ctx context.Context, commandCtx core.Context, target McpEdgeTarget, tool string, arguments map[string]any, idleProbe bool) (mcp.MCPToolCallResult, error) {
	call := func() (mcp.MCPToolCallResult, error) {
		return mcp.CallMCPTool(ctx, mcp.MCPToolCallParams{
			Endpoint:      target.Endpoint,
			MintToken:     McpEdgeTokenMinter(target),
			ClientVersion: CurrentBuildInfo().Version,
			Tool:          tool,
			Arguments:     arguments,
			IdleProbe:     idleProbe,
		})
	}
	result, err := call()
	if err != nil && errors.Is(err, mcp.ErrMCPEndpointUnreachable) {
		// Reattaching spawns a real child process, so a dry run reports unreachable instead.
		if commandCtx.DryRun {
			return result, err
		}
		if reattachErr := ReattachMCPChannel(commandCtx, target.Tenant, target.Environment); reattachErr == nil {
			result, err = call()
		}
	}
	return result, err
}

// McpEdgeErrorWithExitCode is McpEdgeError carrying mcpChannelUnreachableExitCode.
func McpEdgeErrorWithExitCode(target McpEdgeTarget, err error) error {
	wrapped := McpEdgeError(target, err)
	if errors.Is(err, mcp.ErrMCPEndpointUnreachable) {
		return internal.WithExitCode(wrapped, mcpChannelUnreachableExitCode)
	}
	return wrapped
}

// McpEdgeTarget is one environment's resolved MCP endpoint and token audience.
type McpEdgeTarget struct {
	Tenant      string
	Environment string
	Port        int
	Endpoint    string
}

func ResolveMCPEdgeTarget(ctx core.Context, resolveOpen OpenResolver, params shared.OpenParams) (McpEdgeTarget, error) {
	result, err := resolveOpen(params)
	if err != nil {
		return McpEdgeTarget{}, err
	}
	port := config.MCPPortForResult(result)
	if port <= 0 {
		return McpEdgeTarget{}, fmt.Errorf("no local MCP port is configured for %s/%s", result.Tenant, result.Environment)
	}
	target := McpEdgeTarget{
		Tenant:      result.Tenant,
		Environment: result.Environment,
		Port:        port,
		Endpoint:    mcp.MCPLocalEndpoint(port),
	}
	ctx.Trace(fmt.Sprintf("mcp: %s/%s edge resolved to %s", target.Tenant, target.Environment, target.Endpoint))
	return target, nil
}

// McpEdgeTokenMinter mints per request so long tool calls still start with a fresh token.
func McpEdgeTokenMinter(target McpEdgeTarget) mcp.MCPTokenMinter {
	return func() (string, error) {
		return mcpauth.MintDesktopMCPToken(core.DefaultDesktopIdentityDir(), target.Tenant, target.Environment, time.Now())
	}
}

// McpEdgeError turns actionable edge failures into instructions. A lost session is
// reported as a client-side problem: the edge answered, so the environment is fine.
func McpEdgeError(target McpEdgeTarget, err error) error {
	switch {
	case errors.Is(err, mcp.ErrMCPTargetNotAnswering):
		return fmt.Errorf("%w; the port-forward is up, so retry in a few seconds once %s/%s has finished starting — re-establish the forward with `erun open %s %s --reconnect` only if it stays unresponsive", err, target.Tenant, target.Environment, target.Tenant, target.Environment)
	case errors.Is(err, mcp.ErrMCPSessionLost):
		return fmt.Errorf("%w; %s/%s is up and dispatchable — what is broken is this MCP client's session with its edge, not the environment. Calling again re-handshakes; to dispatch work now, use the CLI, which needs no MCP session of its own: `erun exec job start --tenant %s --environment %s --id <job id> --name <name> -- <command>`", err, target.Tenant, target.Environment, target.Tenant, target.Environment)
	case errors.Is(err, mcp.ErrMCPEndpointUnreachable):
		return fmt.Errorf("%w; run `erun open %s %s` so the local MCP port-forward is up", err, target.Tenant, target.Environment)
	case errors.Is(err, mcp.ErrMCPUnauthorized):
		return fmt.Errorf("%w; %s/%s was deployed without this machine's MCP public key, so redeploy it from the ERun desktop app", err, target.Tenant, target.Environment)
	}
	return err
}

func CompactMCPArguments(arguments map[string]any) string {
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}
