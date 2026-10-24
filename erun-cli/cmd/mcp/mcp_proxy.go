package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/inputs"
	"github.com/sophium/erun/erun-common/command/workspace"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/spf13/cobra"
)

func newMCPProxyCmd(resolveOpen shared.OpenResolver) *cobra.Command {
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
			return runMCPProxyCommand(cmd.Context(), shared.CommandContext(cmd), resolveOpen, shared.ScopedOpenParams(cmd.CommandPath(), tenant, environment))
		},
	}
	shared.AddDryRunFlag(cmd)
	addMCPEdgeScopeFlags(cmd, &tenant, &environment)
	return cmd
}

func runMCPProxyCommand(ctx context.Context, commandCtx core.Context, resolveOpen shared.OpenResolver, params model.OpenParams) error {
	target, err := shared.ResolveMCPEdgeTarget(commandCtx, resolveOpen, params)
	if err != nil {
		return err
	}

	commandCtx.TraceCommand("", "mcp", "proxy", target.Endpoint)
	if commandCtx.DryRun {
		return nil
	}

	return mcp.RunMCPStdioProxy(ctx, mcp.MCPStdioProxyParams{
		Endpoint:      target.Endpoint,
		MintToken:     shared.McpEdgeTokenMinter(target),
		ClientVersion: shared.CurrentBuildInfo().Version,
		In:            commandCtx.Stdin,
		Out:           commandCtx.Stdout,
		Diagnostics:   commandCtx.Stderr,
		DescribeError: func(err error) string { return shared.McpEdgeError(target, err).Error() },
		LocalTools: []mcp.MCPLocalTool{
			sshdSyncLocalTool(resolveOpen, params),
			// Deprecated alias for one release.
			deprecatedAlias(sshdSyncLocalTool(resolveOpen, params), "workspace_sync", "sshd_sync"),
			inputsUploadLocalTool(resolveOpen, params),
		},
	})
}

// deprecatedAlias keeps a retired tool name callable, saying so in its description.
func deprecatedAlias(tool mcp.MCPLocalTool, retired, replacement string) mcp.MCPLocalTool {
	tool.Name = retired
	tool.Description = "Deprecated: use " + replacement + ". Retained for one release; this name will be removed."
	return tool
}

// inputsUploadLocalTool is host-served: the source file is on this host, out of the edge's reach.
func inputsUploadLocalTool(resolveOpen shared.OpenResolver, params model.OpenParams) mcp.MCPLocalTool {
	return mcp.MCPLocalTool{
		Name: "inputs_upload",
		Description: "Stream a local file on this host into the environment's runtime pod at an explicit destination, byte-identical, without the bytes passing through this call's arguments or your context. " +
			"Runs on the host, where the source file lives. remotePath is the full absolute destination inside the pod, including the file name — there is no default, so it can never silently land somewhere a background process (such as sshd_sync's mirror) reconciles away. Set preview to resolve the transfer and trace what would happen without sending anything. Refuses clearly, naming why, when the local file is missing, remotePath is not absolute, the channel to the pod is down, or the destination directory is not writable.",
		InputSchema: json.RawMessage(`{"type":"object","required":["localPath","remotePath"],"properties":{"localPath":{"type":"string","description":"path to the file on this host"},"remotePath":{"type":"string","description":"absolute destination path inside the pod, including the file name"},"preview":{"type":"boolean","description":"when true, resolve and describe the transfer without sending anything"}}}`),
		Call: func(_ context.Context, arguments json.RawMessage) (string, error) {
			var call struct {
				LocalPath  string `json:"localPath"`
				RemotePath string `json:"remotePath"`
				Preview    bool   `json:"preview"`
			}
			if len(arguments) == 0 {
				return "", fmt.Errorf("inputs_upload requires localPath and remotePath")
			}
			if err := json.Unmarshal(arguments, &call); err != nil {
				return "", fmt.Errorf("read inputs_upload arguments: %w", err)
			}
			result, err := resolveOpen(params)
			if err != nil {
				return "", err
			}
			req := cloud.ShellLaunchParamsFromResult(result)
			var trace bytes.Buffer
			toolCtx := core.Context{
				Logger: core.NewLoggerWithWriters(core.VerbosityTrace, &trace, &trace),
				DryRun: call.Preview,
			}
			out, err := inputs.UploadRuntimeInput(toolCtx, req, model.UploadRuntimeInputParams{LocalPath: call.LocalPath, RemotePath: call.RemotePath}, inputs.RunRemoteCommandWithStdin)
			if err != nil {
				return "", err
			}
			if call.Preview {
				return trace.String(), nil
			}
			return fmt.Sprintf("Uploaded %s (%d bytes, sha256 %s) to %s", call.LocalPath, out.Bytes, out.SHA256, out.RemotePath), nil
		},
	}
}

// sshdSyncLocalTool is host-served because the mirror is on this host; the env
// resolves per call so a mid-session enable is picked up.
func sshdSyncLocalTool(resolveOpen shared.OpenResolver, params model.OpenParams) mcp.MCPLocalTool {
	return mcp.MCPLocalTool{
		Name: "sshd_sync",
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
			syncParams, err := workspace.ResolveWorkspaceSyncParams(result, nil)
			if err != nil {
				return "", fmt.Errorf("workspace sync for %s/%s: %w", result.Tenant, result.Environment, err)
			}
			if err := workspace.WorkspaceSyncSSHReady(ctx, syncParams.HostAlias); err != nil {
				return "", fmt.Errorf("workspace sync for %s/%s: the SSH channel to the pod is not up (%s): %w", result.Tenant, result.Environment, syncParams.HostAlias, err)
			}
			pass := workspace.SyncWorkspaceOnce
			if call.Preview {
				pass = workspace.PreviewWorkspaceSync
			}
			synced, err := pass(ctx, syncParams)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%s\n%s", shared.SshdSyncSummary(call.Preview, synced), syncParams.RemotePath+" -> "+syncParams.LocalPath), nil
		},
	}
}
