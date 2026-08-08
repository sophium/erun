package cmd

import (
	"context"

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
	})
}
