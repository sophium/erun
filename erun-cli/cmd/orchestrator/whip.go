package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/command/orchestrator"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/spf13/cobra"
)

// The manual, host-side fan-out of the desktop's automatic pacing nudge.

func NewWhipCmd(store lifecycle.ListStore, resolveOpen shared.OpenResolver) *cobra.Command {
	var tenant string
	var environment string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "whip [TENANT] [ENVIRONMENT]",
		Short: "Push every live orchestrator and environment agent to keep moving",
		Long: "Re-states the pacing contract into every live session it can reach. Scoped\n" +
			"to one TENANT/ENVIRONMENT, it pushes only that environment's own AI session\n" +
			"(over that environment's MCP edge) and targets no orchestrator. Given neither,\n" +
			"it fans out over every configured environment plus every persisted\n" +
			"orchestrator definition. Reports each target's outcome — pushed, or skipped\n" +
			"and why — rather than only reporting that it ran.\n\n" +
			"A CLI/MCP process has no channel into a desktop-held orchestrator's live PTY,\n" +
			"so every orchestrator is reported skipped as unreachable from this transport;\n" +
			"only the desktop's own automatic pass can push those. An environment whose MCP\n" +
			"edge is not answering (nobody has it open in the desktop, or its port-forward\n" +
			"dropped) is reported skipped as channel-down, with the `erun open` that brings\n" +
			"the edge back, and the sweep carries on to the remaining targets.\n\n" +
			"A --dry-run never reattaches a channel: the reattach starts a real\n" +
			"`erun open --reconnect`, and a preview does not start work that outlives it.\n\n" +
			"Pass TENANT and ENVIRONMENT to whip one environment only; omit both to whip\n" +
			"every configured environment plus every persisted orchestrator.",
		Example:       "  erun whip\n  erun whip --tenant team --environment dev\n  erun whip --dry-run",
		Args:          cobra.MaximumNArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				tenant = args[0]
			}
			if len(args) > 1 {
				environment = args[1]
			}
			return runWhipCommand(cmd.Context(), shared.CommandContext(cmd), store, resolveOpen, tenant, environment, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "Whip only this tenant's environment (requires --environment)")
	cmd.Flags().StringVar(&environment, "environment", "", "Whip only this environment (requires --tenant)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write JSON output (alias for --output json)")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runWhipCommand(ctx context.Context, commandCtx core.Context, store lifecycle.ListStore, resolveOpen shared.OpenResolver, tenant, environment string, jsonOutput bool) error {
	scoped := strings.TrimSpace(tenant) != "" || strings.TrimSpace(environment) != ""
	targets, err := resolveWhipEnvironmentTargets(store, tenant, environment)
	if err != nil {
		return err
	}

	report := model.WhipReport{DryRun: commandCtx.DryRun}
	// Text rows stream as decided since a slow reattach can stall the sweep; JSON is written once at the end.
	emit := func(result model.WhipResult) error {
		report.Results = append(report.Results, result)
		if shared.CommandWantsJSON(commandCtx, jsonOutput) {
			return nil
		}
		return writeWhipResult(commandCtx, result, commandCtx.DryRun)
	}
	for _, target := range targets {
		if err := emit(whipOneEnvironment(ctx, commandCtx, resolveOpen, target.tenant, target.environment)); err != nil {
			return err
		}
	}

	// A scoped call must never fan out into every orchestrator.
	if !scoped {
		globalConfig, _, _ := store.LoadERunConfig()
		whipConfig := orchestrator.ResolveWhipConfig(globalConfig.Whip)
		now := time.Now()
		for _, candidate := range orchestrator.ListWhipOrchestratorCandidates(globalConfig.Orchestrators) {
			decision, reason := orchestrator.DecideWhip(candidate, now, whipConfig, true)
			if err := emit(model.WhipResult{Candidate: candidate, Decision: decision, Reason: reason}); err != nil {
				return err
			}
		}
	}

	return WriteWhipReport(commandCtx, report, jsonOutput)
}

// WriteWhipReport keeps --output json and --json from diverging.
func WriteWhipReport(commandCtx core.Context, report model.WhipReport, jsonOutput bool) error {
	if shared.CommandWantsJSON(commandCtx, jsonOutput) {
		encoder := json.NewEncoder(commandCtx.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	return nil
}

type whipEnvironmentTarget struct {
	tenant      string
	environment string
}

// resolveWhipEnvironmentTargets lists every configured environment when unscoped,
// never defaulting to the ambient scope.
func resolveWhipEnvironmentTargets(store lifecycle.ListStore, tenant, environment string) ([]whipEnvironmentTarget, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant != "" || environment != "" {
		if tenant == "" || environment == "" {
			return nil, fmt.Errorf("whip: pass both --tenant and --environment to target one environment, or neither to whip everything configured")
		}
		return []whipEnvironmentTarget{{tenant: tenant, environment: environment}}, nil
	}

	tenants, err := store.ListTenantConfigs()
	if err != nil {
		return nil, fmt.Errorf("whip: listing configured tenants: %w", err)
	}
	var targets []whipEnvironmentTarget
	for _, t := range tenants {
		envs, err := store.ListEnvConfigs(t.Name)
		if err != nil {
			return nil, fmt.Errorf("whip: listing %s's environments: %w", t.Name, err)
		}
		for _, e := range envs {
			targets = append(targets, whipEnvironmentTarget{tenant: t.Name, environment: e.Name})
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].tenant != targets[j].tenant {
			return targets[i].tenant < targets[j].tenant
		}
		return targets[i].environment < targets[j].environment
	})
	return targets, nil
}

// whipOneEnvironment calls the env's "whip" tool, preview-guarded under --dry-run.
// An unreachable edge resolves to "not alive".
func whipOneEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen shared.OpenResolver, tenant, environment string) model.WhipResult {
	id := tenant + "/" + environment
	notAlive := model.WhipResult{
		Candidate: model.WhipCandidate{Kind: model.WhipTargetEnvironment, ID: id, Name: id, Reachable: true, Alive: false},
		Decision:  model.WhipDecisionNone,
		Reason:    model.WhipReasonNotAlive,
	}

	target, err := shared.ResolveMCPEdgeTarget(commandCtx, resolveOpen, shared.ScopedOpenParams(commandCtx.Command, tenant, environment))
	if err != nil {
		notAlive.Error = err.Error()
		return notAlive
	}
	// Restate the scope so a stale edge surfaces as a named mismatch.
	arguments := map[string]any{"preview": commandCtx.DryRun, "tenant": target.Tenant, "environment": target.Environment}
	commandCtx.TraceCommand("", "mcp", "tools/call", target.Endpoint, "whip", shared.CompactMCPArguments(arguments))

	// An error means the call failed; a dead session comes back as a successful WhipReasonNotAlive.
	failed := func(detail string) model.WhipResult {
		return model.WhipResult{
			Candidate: model.WhipCandidate{Kind: model.WhipTargetEnvironment, ID: id, Name: id, Reachable: true, Alive: false},
			Decision:  model.WhipDecisionNone,
			Reason:    model.WhipReasonCallFailed,
			Error:     detail,
		}
	}

	// An unanswered edge is a named skip with its remedy, not a failure.
	channelDown := func(detail string) model.WhipResult {
		return model.WhipResult{
			Candidate: model.WhipCandidate{Kind: model.WhipTargetEnvironment, ID: id, Name: id, Reachable: true, Alive: false},
			Decision:  model.WhipDecisionNone,
			Reason:    model.WhipReasonChannelDown,
			Error:     detail,
		}
	}

	result, err := shared.CallMCPToolWithReattach(ctx, commandCtx, target, "whip", arguments, false)
	if err != nil {
		if errors.Is(err, mcp.ErrMCPEndpointUnreachable) {
			return channelDown(shared.McpEdgeError(target, err).Error())
		}
		return failed(err.Error())
	}
	var decoded model.WhipResult
	if len(result.Structured) == 0 {
		return failed(fmt.Sprintf("%s/%s returned no result for whip", tenant, environment))
	}
	if err := json.Unmarshal(result.Structured, &decoded); err != nil {
		return failed(fmt.Sprintf("decode the whip result from %s/%s: %v", tenant, environment, err))
	}
	return decoded
}

func writeWhipResult(ctx core.Context, result model.WhipResult, dryRun bool) error {
	return shared.WriteLabeledValue(ctx, whipResultLabel(result), whipResultValue(result, dryRun))
}

func whipResultLabel(result model.WhipResult) string {
	switch result.Candidate.Kind {
	case model.WhipTargetOrchestrator:
		label := strings.TrimSpace(result.Candidate.Name)
		if label == "" {
			label = result.Candidate.ID
		}
		return "orchestrator " + label
	case model.WhipTargetEnvironment:
	}
	return result.Candidate.ID
}

func whipResultValue(result model.WhipResult, dryRun bool) string {
	switch result.Decision {
	case model.WhipDecisionNudge:
		if result.Pushed {
			return fmt.Sprintf("pushed (consecutive nudge %d)", result.Candidate.NudgeCount+1)
		}
		if result.Error != "" {
			return "push failed: " + result.Error
		}
		return "would push (dry-run)"
	case model.WhipDecisionCap:
		return "capped: stopped nudging after repeated silence"
	case model.WhipDecisionNone:
	}
	if result.Reason == model.WhipReasonCallFailed {
		return "call failed: " + result.Error
	}
	value := "skipped — " + string(result.Reason)
	if result.Error != "" {
		value += ": " + result.Error
	}
	// A dry run never reattaches, so a down channel is not a verdict.
	if dryRun && result.Reason == model.WhipReasonChannelDown {
		value += " (a real run reattaches the channel first)"
	}
	return value
}
