package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	common "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-common/infra/mcpauth"
	"github.com/sophium/erun/internal"
)

// mcpChannelUnreachableExitCode marks a channel that stayed unreachable even
// after one transparent reattach — distinct from a normal error so a caller
// looping on this command cannot misread "the channel is down" as "the job
// finished" or "the tool failed", which is exactly the misread a naive
// treat-anything-not-running-as-terminal watcher falls into.
const mcpChannelUnreachableExitCode = 126

// ReattachMCPChannel is the spawn the reattach path performs. It is a variable
// so the decision that keeps a --dry-run from starting real work can be tested
// by asserting the spawn is never invoked, which is the only observable a
// preview leaves behind.
var ReattachMCPChannel = ReattachEnvironmentMCPChannel

// CallMCPToolWithReattach is the shared choke point for every host-side call
// into an environment's MCP edge (mcp call and the job/idle/activity verbs
// via callEnvironmentTool): a channel that has dropped or gone stale gets
// exactly one transparent reattach via `erun open --reconnect` before the
// failure is surfaced, so a caller doesn't have to rediscover the two traps a
// hand-rolled retry loop falls into — probing the port binding is worthless
// (a stale forward still accepts the connection), and a bare `erun open`
// would silently start an environment the operator deliberately stopped.
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
		// A preview resolves and traces; it never executes. Reattaching spawns a
		// real `erun open --reconnect` child that keeps running past the
		// preview, so a --dry-run reports the channel as unreachable instead of
		// re-establishing it.
		if commandCtx.DryRun {
			return result, err
		}
		if reattachErr := ReattachMCPChannel(commandCtx, target.Tenant, target.Environment); reattachErr == nil {
			result, err = call()
		}
	}
	return result, err
}

// McpEdgeErrorWithExitCode is mcpEdgeError plus the exit code an unrecovered
// channel-unreachable failure must carry (see mcpChannelUnreachableExitCode).
func McpEdgeErrorWithExitCode(target McpEdgeTarget, err error) error {
	wrapped := McpEdgeError(target, err)
	if errors.Is(err, mcp.ErrMCPEndpointUnreachable) {
		return internal.WithExitCode(wrapped, mcpChannelUnreachableExitCode)
	}
	return wrapped
}

// McpEdgeTarget is one environment's resolved MCP edge: where it answers and
// which audience a bearer for it must carry.
type McpEdgeTarget struct {
	Tenant      string
	Environment string
	Port        int
	Endpoint    string
}

func ResolveMCPEdgeTarget(ctx core.Context, resolveOpen OpenResolver, params common.OpenParams) (McpEdgeTarget, error) {
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

// McpEdgeTokenMinter mints per request rather than once per command, so a tool
// call that runs longer than the token's lifetime still starts with a fresh one.
func McpEdgeTokenMinter(target McpEdgeTarget) mcp.MCPTokenMinter {
	return func() (string, error) {
		return mcpauth.MintDesktopMCPToken(core.DefaultDesktopIdentityDir(), target.Tenant, target.Environment, time.Now())
	}
}

// McpEdgeError turns the failures an operator can act on into instructions: a
// missing port-forward, an edge that does not trust this machine's identity,
// and the one that is not about the environment at all — a live edge that will
// not hold this client's session.
//
// The session loss is the case that has to say which side broke. Every other
// branch here sends the operator to the tunnel or to the environment, and that
// reading of a lost session is what leaves a dispatchable environment sitting
// idle: the edge answered this very call, so nothing about the environment is
// in question, and the CLI — which dispatches through the same edge without
// this client's session — keeps working meanwhile.
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
