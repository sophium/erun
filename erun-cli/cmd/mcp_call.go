package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/internal"
	"github.com/spf13/cobra"
)

// mcpChannelUnreachableExitCode marks a channel that stayed unreachable even
// after one transparent reattach — distinct from a normal error so a caller
// looping on this command cannot misread "the channel is down" as "the job
// finished" or "the tool failed", which is exactly the misread a naive
// treat-anything-not-running-as-terminal watcher falls into.
const mcpChannelUnreachableExitCode = 126

// callMCPToolWithReattach is the shared choke point for every host-side call
// into an environment's MCP edge (mcp call and the job/idle/activity verbs
// via callEnvironmentTool): a channel that has dropped or gone stale gets
// exactly one transparent reattach via `erun open --reconnect` before the
// failure is surfaced, so a caller doesn't have to rediscover the two traps a
// hand-rolled retry loop falls into — probing the port binding is worthless
// (a stale forward still accepts the connection), and a bare `erun open`
// would silently start an environment the operator deliberately stopped.
func callMCPToolWithReattach(ctx context.Context, commandCtx common.Context, target mcpEdgeTarget, tool string, arguments map[string]any, idleProbe bool) (common.MCPToolCallResult, error) {
	call := func() (common.MCPToolCallResult, error) {
		return common.CallMCPTool(ctx, common.MCPToolCallParams{
			Endpoint:      target.endpoint,
			MintToken:     mcpEdgeTokenMinter(target),
			ClientVersion: currentBuildInfo().Version,
			Tool:          tool,
			Arguments:     arguments,
			IdleProbe:     idleProbe,
		})
	}
	result, err := call()
	if err != nil && errors.Is(err, common.ErrMCPEndpointUnreachable) {
		if reattachErr := reattachEnvironmentMCPChannel(commandCtx, target.tenant, target.environment); reattachErr == nil {
			result, err = call()
		}
	}
	return result, err
}

// mcpEdgeErrorWithExitCode is mcpEdgeError plus the exit code an unrecovered
// channel-unreachable failure must carry (see mcpChannelUnreachableExitCode).
func mcpEdgeErrorWithExitCode(target mcpEdgeTarget, err error) error {
	wrapped := mcpEdgeError(target, err)
	if errors.Is(err, common.ErrMCPEndpointUnreachable) {
		return internal.WithExitCode(wrapped, mcpChannelUnreachableExitCode)
	}
	return wrapped
}

// mcpEdgeTarget is one environment's resolved MCP edge: where it answers and
// which audience a bearer for it must carry.
type mcpEdgeTarget struct {
	tenant      string
	environment string
	port        int
	endpoint    string
}

func newMCPCallCmd(resolveOpen OpenResolver) *cobra.Command {
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
			return runMCPCallCommand(cmd.Context(), commandContext(cmd), resolveOpen, scopedOpenParams(tenant, environment), tool, arguments)
		},
	}
	addDryRunFlag(cmd)
	addMCPEdgeScopeFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&tool, "tool", "", "Name of the tool to call, as listed by 'erun mcp tools'")
	cmd.Flags().StringVar(&arguments, "args", "", "Tool arguments as a JSON object (default: no arguments)")
	return cmd
}

func newMCPToolsCmd(resolveOpen OpenResolver) *cobra.Command {
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
			return runMCPToolsCommand(cmd.Context(), commandContext(cmd), resolveOpen, scopedOpenParams(tenant, environment))
		},
	}
	addDryRunFlag(cmd)
	addMCPEdgeScopeFlags(cmd, &tenant, &environment)
	return cmd
}

func newMCPTokenCmd(resolveOpen OpenResolver) *cobra.Command {
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
			return runMCPTokenCommand(commandContext(cmd), resolveOpen, scopedOpenParams(tenant, environment))
		},
	}
	addDryRunFlag(cmd)
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

func runMCPCallCommand(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.OpenParams, tool, arguments string) error {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return fmt.Errorf("--tool is required; run `erun mcp tools` to list the environment's tools")
	}
	toolArguments, err := parseMCPToolArguments(arguments)
	if err != nil {
		return err
	}
	target, err := resolveMCPEdgeTarget(commandCtx, resolveOpen, params)
	if err != nil {
		return err
	}

	commandCtx.TraceCommand("", "mcp", "tools/call", target.endpoint, tool, compactMCPArguments(toolArguments))
	if commandCtx.DryRun {
		return nil
	}

	// Not an idle probe: this command calls a caller-named tool, and whether that
	// tool is read-only is not something the CLI can know generically — its own
	// help text says as much ("A tool can change the environment").
	result, err := callMCPToolWithReattach(ctx, commandCtx, target, tool, toolArguments, false)
	if err != nil {
		return mcpEdgeErrorWithExitCode(target, err)
	}
	if commandCtx.Output == common.OutputJSON {
		return commandCtx.WriteResult(result)
	}
	return writeMCPCallText(commandCtx, result)
}

func runMCPToolsCommand(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.OpenParams) error {
	target, err := resolveMCPEdgeTarget(commandCtx, resolveOpen, params)
	if err != nil {
		return err
	}

	commandCtx.TraceCommand("", "mcp", "tools/list", target.endpoint)
	if commandCtx.DryRun {
		return nil
	}

	list, err := listMCPToolsWithReattach(ctx, commandCtx, target)
	if err != nil {
		return mcpEdgeErrorWithExitCode(target, err)
	}
	if commandCtx.Output == common.OutputJSON {
		return commandCtx.WriteResult(list)
	}
	return writeMCPToolsText(commandCtx, target, list)
}

// listMCPToolsWithReattach is listMCPTools's counterpart to
// callMCPToolWithReattach: the same one-shot reattach-then-retry for a
// dropped or stale channel.
func listMCPToolsWithReattach(ctx context.Context, commandCtx common.Context, target mcpEdgeTarget) (common.MCPToolListResult, error) {
	list := func() (common.MCPToolListResult, error) {
		return common.ListMCPTools(ctx, common.MCPToolListParams{
			Endpoint:      target.endpoint,
			MintToken:     mcpEdgeTokenMinter(target),
			ClientVersion: currentBuildInfo().Version,
			IdleProbe:     true,
		})
	}
	result, err := list()
	if err != nil && errors.Is(err, common.ErrMCPEndpointUnreachable) {
		if reattachErr := reattachEnvironmentMCPChannel(commandCtx, target.tenant, target.environment); reattachErr == nil {
			result, err = list()
		}
	}
	return result, err
}

func runMCPTokenCommand(commandCtx common.Context, resolveOpen OpenResolver, params common.OpenParams) error {
	target, err := resolveMCPEdgeTarget(commandCtx, resolveOpen, params)
	if err != nil {
		return err
	}

	commandCtx.TraceCommand("", "mcp", "mint-token", common.MCPTokenAudience(target.tenant, target.environment))
	if commandCtx.DryRun {
		return nil
	}

	now := time.Now()
	token, err := common.MintDesktopMCPToken(common.DefaultDesktopIdentityDir(), target.tenant, target.environment, now)
	if err != nil {
		return err
	}
	if commandCtx.Output == common.OutputJSON {
		return commandCtx.WriteResult(mcpTokenResult{
			Tenant:      target.tenant,
			Environment: target.environment,
			Endpoint:    target.endpoint,
			Issuer:      common.DesktopMCPIssuer(),
			Audience:    common.MCPTokenAudience(target.tenant, target.environment),
			ExpiresAt:   now.Add(common.DesktopMCPTokenTTL).UTC().Format(time.RFC3339),
			Token:       token,
		})
	}
	_, err = fmt.Fprintln(commandCtx.Stdout, token)
	return err
}

func resolveMCPEdgeTarget(ctx common.Context, resolveOpen OpenResolver, params common.OpenParams) (mcpEdgeTarget, error) {
	result, err := resolveOpen(params)
	if err != nil {
		return mcpEdgeTarget{}, err
	}
	port := common.MCPPortForResult(result)
	if port <= 0 {
		return mcpEdgeTarget{}, fmt.Errorf("no local MCP port is configured for %s/%s", result.Tenant, result.Environment)
	}
	target := mcpEdgeTarget{
		tenant:      result.Tenant,
		environment: result.Environment,
		port:        port,
		endpoint:    common.MCPLocalEndpoint(port),
	}
	ctx.Trace(fmt.Sprintf("mcp: %s/%s edge resolved to %s", target.tenant, target.environment, target.endpoint))
	return target, nil
}

// mcpEdgeTokenMinter mints per request rather than once per command, so a tool
// call that runs longer than the token's lifetime still starts with a fresh one.
func mcpEdgeTokenMinter(target mcpEdgeTarget) common.MCPTokenMinter {
	return func() (string, error) {
		return common.MintDesktopMCPToken(common.DefaultDesktopIdentityDir(), target.tenant, target.environment, time.Now())
	}
}

// mcpEdgeError turns the two failures an operator can act on into instructions:
// a missing port-forward and an edge that does not trust this machine's identity.
func mcpEdgeError(target mcpEdgeTarget, err error) error {
	switch {
	case errors.Is(err, common.ErrMCPEndpointUnreachable):
		return fmt.Errorf("%w; run `erun open %s %s` so the local MCP port-forward is up", err, target.tenant, target.environment)
	case errors.Is(err, common.ErrMCPUnauthorized):
		return fmt.Errorf("%w; %s/%s was deployed without this machine's MCP public key, so redeploy it from the ERun desktop app", err, target.tenant, target.environment)
	}
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

func compactMCPArguments(arguments map[string]any) string {
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func writeMCPCallText(ctx common.Context, result common.MCPToolCallResult) error {
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

func writeMCPToolsText(ctx common.Context, target mcpEdgeTarget, list common.MCPToolListResult) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Tools on %s/%s (%s):\n", target.tenant, target.environment, target.endpoint); err != nil {
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

// mcpToolArgumentSummary renders a tool's input schema as its property names,
// required ones marked, so text output stays scannable while --output json keeps
// the full schema.
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
