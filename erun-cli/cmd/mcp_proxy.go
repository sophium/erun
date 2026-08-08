package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newMCPProxyCmd(resolveOpen OpenResolver) *cobra.Command {
	var tenant, environment string
	cmd := &cobra.Command{
		Use:   "proxy",
		Short: "Bridge a stdio MCP client to an environment's MCP edge",
		Long: "Speak MCP on stdin/stdout and relay every message to an environment's MCP edge, " +
			"minting a fresh bearer for each request.\n\n" +
			"Point an MCP client at this command instead of writing a bearer into the client's " +
			"config. A client reads that config once at launch and cannot refresh a header, so a " +
			"token in it ages out mid-session and every tool for the environment fails at once; " +
			"nothing this command needs is a credential, so a session keeps working for as long as " +
			"it runs. Reaching the edge still requires the port-forward `erun open` establishes — " +
			"while it is down, each request is answered with a JSON-RPC error saying so rather than " +
			"a dead pipe.\n\n" +
			"stdout carries JSON-RPC and nothing else; every diagnostic goes to stderr.",
		Example: "  erun mcp proxy --tenant acme --environment dev\n" +
			"  claude --mcp-config '{\"mcpServers\":{\"acme-dev\":{\"type\":\"stdio\"," +
			"\"command\":\"erun\",\"args\":[\"mcp\",\"proxy\",\"--tenant\",\"acme\",\"--environment\",\"dev\"]}}}'",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPProxyCommand(cmd.Context(), commandContext(cmd), resolveOpen, scopedOpenParams(tenant, environment))
		},
	}
	addDryRunFlag(cmd)
	addMCPEdgeScopeFlags(cmd, &tenant, &environment)
	return cmd
}

func runMCPProxyCommand(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.OpenParams) error {
	target, err := resolveMCPEdgeTarget(commandCtx, resolveOpen, params)
	if err != nil {
		return err
	}

	commandCtx.TraceCommand("", "mcp", "proxy", target.endpoint)
	if commandCtx.DryRun {
		return nil
	}

	return common.RunMCPStdioProxy(ctx, common.MCPStdioProxyParams{
		Endpoint:      target.endpoint,
		MintToken:     mcpEdgeTokenMinter(target),
		ClientVersion: currentBuildInfo().Version,
		In:            commandCtx.Stdin,
		Out:           commandCtx.Stdout,
		Diagnostics:   commandCtx.Stderr,
		DescribeError: func(err error) string { return mcpEdgeError(target, err).Error() },
		LocalTools:    []common.MCPLocalTool{workspaceSyncLocalTool(resolveOpen, params)},
	})
}

// workspaceSyncLocalTool exposes the host mirror's refresh over the same MCP an
// orchestrator already drives the environment through. It is served here rather
// than by the edge because the mirror is on this host: the edge runs in the pod
// and has nothing to write to. The environment is resolved per call, so a mirror
// enabled mid-session is picked up without restarting the session.
func workspaceSyncLocalTool(resolveOpen OpenResolver, params common.OpenParams) common.MCPLocalTool {
	return common.MCPLocalTool{
		Name: "workspace_sync",
		Description: "Run one workspace-sync pass for this environment: mirror the pod's git-visible worktree into the host review directory, delete what the pod no longer has, and deliver the pod's cross-built artifacts. " +
			"Runs on the host, where the mirror lives. Set preview to report what a pass would change without touching the mirror. Refuses, naming which, when the environment is not a remote-agent env, has workspace sync disabled, has no configured local path, or its SSH channel is down.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"preview":{"type":"boolean","description":"when true, report what one pass would change without changing it"}}}`),
		Call: func(ctx context.Context, arguments json.RawMessage) (string, error) {
			var call struct {
				Preview bool `json:"preview"`
			}
			if len(arguments) > 0 {
				if err := json.Unmarshal(arguments, &call); err != nil {
					return "", fmt.Errorf("read workspace_sync arguments: %w", err)
				}
			}
			result, err := resolveOpen(params)
			if err != nil {
				return "", err
			}
			syncParams, err := common.ResolveWorkspaceSyncParams(result, nil)
			if err != nil {
				return "", fmt.Errorf("workspace sync for %s/%s: %w", result.Tenant, result.Environment, err)
			}
			if err := common.WorkspaceSyncSSHReady(ctx, syncParams.HostAlias); err != nil {
				return "", fmt.Errorf("workspace sync for %s/%s: the SSH channel to the pod is not up (%s): %w", result.Tenant, result.Environment, syncParams.HostAlias, err)
			}
			pass := common.SyncWorkspaceOnce
			if call.Preview {
				pass = common.PreviewWorkspaceSync
			}
			synced, err := pass(ctx, syncParams)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%s\n%s", sshdSyncSummary(call.Preview, synced), syncParams.RemotePath+" -> "+syncParams.LocalPath), nil
		},
	}
}
