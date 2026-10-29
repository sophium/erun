package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/cmd/shared/envcall"
	"github.com/sophium/erun/cmd/shared/envscope"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity/versionskew"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-common/infra/mcpauth"
	"github.com/spf13/cobra"
)

func newMCPCallCmd(resolveOpen envscope.OpenResolver) *cobra.Command {
	var tenant, environment, tool, arguments string
	cmd := &cobra.Command{
		Use:   "call",
		Short: "Call one tool on an environment's MCP edge",
		Long: "Call a single tool on an environment's MCP edge and print its result.\n\n" +
			"The bearer token is minted for this call from the desktop identity and never " +
			"handled by the caller, so a script or agent driving an environment cannot be " +
			"stopped by an expired token. The call goes to the environment's local MCP port, " +
			"which requires the port-forward `erun open` establishes.\n\n" +
			"A tool can change the environment: whatever the named tool does, this runs it.",
		Example: "  erun mcp call --tool version\n" +
			"  erun mcp call --tenant acme --environment dev --tool raw --args '{\"command\":[\"git\",\"status\"]}'\n" +
			"  erun mcp call --tool list --output json",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPCallCommand(cmd.Context(), clicmd.CommandContext(cmd), resolveOpen, envscope.ScopedOpenParams(cmd.CommandPath(), tenant, environment), tool, arguments)
		},
	}
	clicmd.AddDryRunFlag(cmd)
	addMCPEdgeScopeFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&tool, "tool", "", "Name of the tool to call, as listed by 'erun mcp tools'")
	cmd.Flags().StringVar(&arguments, "args", "", "Tool arguments as a JSON object (default: no arguments)")
	return cmd
}

func newMCPToolsCmd(resolveOpen envscope.OpenResolver) *cobra.Command {
	var tenant, environment string
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "List the tools an environment's MCP edge exposes",
		Long: "List the tools an environment's MCP edge exposes, with the arguments each one takes.\n\n" +
			"Use it to discover what `erun mcp call --tool` accepts. Full JSON input schemas come " +
			"with --output json. Reaching the edge requires the port-forward `erun open` establishes.",
		Example:       "  erun mcp tools\n  erun mcp tools --tenant acme --environment dev --output json",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPToolsCommand(cmd.Context(), clicmd.CommandContext(cmd), resolveOpen, envscope.ScopedOpenParams(cmd.CommandPath(), tenant, environment))
		},
	}
	clicmd.AddDryRunFlag(cmd)
	addMCPEdgeScopeFlags(cmd, &tenant, &environment)
	return cmd
}

func newMCPTokenCmd(resolveOpen envscope.OpenResolver) *cobra.Command {
	var tenant, environment string
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Mint a bearer token for an environment's MCP edge",
		Long: "Print a freshly minted bearer token for an environment's MCP edge, for a caller " +
			"that speaks MCP itself.\n\n" +
			"The token is signed by this machine's desktop identity, scoped to one environment, " +
			"and short-lived — mint a new one per request rather than storing it. Prefer " +
			"`erun mcp call`, which mints its own token, when a single tool call is all you need.",
		Example:       "  erun mcp token\n  erun mcp token --tenant acme --environment dev --output json",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPTokenCommand(clicmd.CommandContext(cmd), resolveOpen, envscope.ScopedOpenParams(cmd.CommandPath(), tenant, environment))
		},
	}
	clicmd.AddDryRunFlag(cmd)
	addMCPEdgeScopeFlags(cmd, &tenant, &environment)
	return cmd
}

func addMCPEdgeScopeFlags(cmd *cobra.Command, tenant, environment *string) {
	cmd.Flags().StringVar(tenant, "tenant", "", "Target a specific tenant (default: the current scope)")
	cmd.Flags().StringVar(environment, "environment", "", "Target a specific environment; requires --tenant")
}

type mcpTokenResult struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Endpoint    string `json:"endpoint"`
	Issuer      string `json:"issuer"`
	Audience    string `json:"audience"`
	ExpiresAt   string `json:"expiresAt"`
	Token       string `json:"token"`
}

func runMCPCallCommand(ctx context.Context, commandCtx core.Context, resolveOpen envscope.OpenResolver, params model.OpenParams, tool, arguments string) error {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return fmt.Errorf("--tool is required; run `erun mcp tools` to list the environment's tools")
	}
	toolArguments, err := parseMCPToolArguments(arguments)
	if err != nil {
		return err
	}
	target, err := envcall.ResolveMCPEdgeTarget(commandCtx, resolveOpen, params)
	if err != nil {
		return err
	}

	commandCtx.TraceCommand("", "mcp", "tools/call", target.Endpoint, tool, envcall.CompactMCPArguments(toolArguments))
	if commandCtx.DryRun {
		return nil
	}

	// Not an idle probe: the named tool may change the environment.
	result, err := envcall.CallMCPToolWithReattach(ctx, commandCtx, target, tool, toolArguments, false)
	if err != nil {
		if tool == "activity_lease_take" {
			exclusive, _ := toolArguments["exclusive"].(bool)
			err = versionskew.DescribeExclusiveActivityLeaseVersionSkew(target.Tenant, target.Environment, exclusive, err)
		}
		return envcall.McpEdgeErrorWithExitCode(target, err)
	}
	if commandCtx.Output == model.OutputJSON {
		return commandCtx.WriteResult(result)
	}
	return writeMCPCallText(commandCtx, result)
}

func runMCPToolsCommand(ctx context.Context, commandCtx core.Context, resolveOpen envscope.OpenResolver, params model.OpenParams) error {
	target, err := envcall.ResolveMCPEdgeTarget(commandCtx, resolveOpen, params)
	if err != nil {
		return err
	}

	commandCtx.TraceCommand("", "mcp", "tools/list", target.Endpoint)
	if commandCtx.DryRun {
		return nil
	}

	list, err := listMCPToolsWithReattach(ctx, commandCtx, target)
	if err != nil {
		return envcall.McpEdgeErrorWithExitCode(target, err)
	}
	if commandCtx.Output == model.OutputJSON {
		return commandCtx.WriteResult(list)
	}
	return writeMCPToolsText(commandCtx, target, list)
}

// listMCPToolsWithReattach reattaches once on a dropped or stale channel.
func listMCPToolsWithReattach(ctx context.Context, commandCtx core.Context, target envcall.McpEdgeTarget) (model.MCPToolListResult, error) {
	list := func() (model.MCPToolListResult, error) {
		return mcp.ListMCPTools(ctx, mcp.MCPToolListParams{
			Endpoint:      target.Endpoint,
			MintToken:     envcall.McpEdgeTokenMinter(target),
			ClientVersion: shared.CurrentBuildInfo().Version,
			IdleProbe:     true,
		})
	}
	result, err := list()
	if err != nil && errors.Is(err, mcp.ErrMCPEndpointUnreachable) {
		if reattachErr := envcall.ReattachEnvironmentMCPChannel(commandCtx, target.Tenant, target.Environment); reattachErr == nil {
			result, err = list()
		}
	}
	return result, err
}

func runMCPTokenCommand(commandCtx core.Context, resolveOpen envscope.OpenResolver, params model.OpenParams) error {
	target, err := envcall.ResolveMCPEdgeTarget(commandCtx, resolveOpen, params)
	if err != nil {
		return err
	}

	commandCtx.TraceCommand("", "mcp", "mint-token", mcpauth.MCPTokenAudience(target.Tenant, target.Environment))
	if commandCtx.DryRun {
		return nil
	}

	now := time.Now()
	token, err := mcpauth.MintDesktopMCPToken(core.DefaultDesktopIdentityDir(), target.Tenant, target.Environment, now)
	if err != nil {
		return err
	}
	if commandCtx.Output == model.OutputJSON {
		return commandCtx.WriteResult(mcpTokenResult{
			Tenant:      target.Tenant,
			Environment: target.Environment,
			Endpoint:    target.Endpoint,
			Issuer:      mcpauth.DesktopMCPIssuer(),
			Audience:    mcpauth.MCPTokenAudience(target.Tenant, target.Environment),
			ExpiresAt:   now.Add(core.DesktopMCPTokenTTL).UTC().Format(time.RFC3339),
			Token:       token,
		})
	}
	_, err = fmt.Fprintln(commandCtx.Stdout, token)
	return err
}

func parseMCPToolArguments(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	var arguments map[string]any
	if err := json.Unmarshal([]byte(raw), &arguments); err != nil {
		return nil, fmt.Errorf("--args must be a JSON object: %w", err)
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	return arguments, nil
}

func writeMCPCallText(ctx core.Context, result model.MCPToolCallResult) error {
	if text := strings.TrimRight(result.Text, "\n"); text != "" {
		if _, err := fmt.Fprintln(ctx.Stdout, text); err != nil {
			return err
		}
		return nil
	}
	if len(result.Structured) > 0 {
		_, err := fmt.Fprintln(ctx.Stdout, string(result.Structured))
		return err
	}
	_, err := fmt.Fprintf(ctx.Stdout, "%s returned no content\n", result.Tool)
	return err
}

func writeMCPToolsText(ctx core.Context, target envcall.McpEdgeTarget, list model.MCPToolListResult) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Tools on %s/%s (%s):\n", target.Tenant, target.Environment, target.Endpoint); err != nil {
		return err
	}
	if len(list.Tools) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "  (none)")
		return err
	}
	for _, tool := range list.Tools {
		if _, err := fmt.Fprintf(ctx.Stdout, "  %s\n", strings.TrimSpace(tool.Name)); err != nil {
			return err
		}
		if description := firstMCPDescriptionLine(tool.Description); description != "" {
			if _, err := fmt.Fprintf(ctx.Stdout, "      %s\n", description); err != nil {
				return err
			}
		}
		if arguments := mcpToolArgumentSummary(tool.InputSchema); arguments != "" {
			if _, err := fmt.Fprintf(ctx.Stdout, "      arguments: %s\n", arguments); err != nil {
				return err
			}
		}
	}
	return nil
}

func firstMCPDescriptionLine(description string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(description), "\n")
	return strings.TrimSpace(line)
}

func mcpToolArgumentSummary(schema json.RawMessage) string {
	if len(schema) == 0 {
		return ""
	}
	var decoded struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(schema, &decoded); err != nil || len(decoded.Properties) == 0 {
		return ""
	}
	required := make(map[string]bool, len(decoded.Required))
	for _, name := range decoded.Required {
		required[name] = true
	}
	names := make([]string, 0, len(decoded.Properties))
	for name := range decoded.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for index, name := range names {
		if required[name] {
			names[index] = name + " (required)"
		}
	}
	return strings.Join(names, ", ")
}
