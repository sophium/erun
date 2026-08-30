package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// An operator-triggered pass that pushes every live orchestrator
// and every environment's own agent session with the pacing nudge, and
// reports who was pushed and who was skipped, and why. The automatic version
// of this already runs inside the desktop for orchestrators
// (erun-ui/orchestrator_pacing.go); this command is the manual, host-side
// fan-out over everything configured, reusing the same decide/report core
// (eruncommon.DecideWhip/WhipReport).

func newWhipCmd(store common.ListStore, resolveOpen OpenResolver) *cobra.Command {
	var tenant string
	var environment string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "whip [TENANT] [ENVIRONMENT]",
		Short: "Push every live orchestrator and environment agent to keep moving",
		Long: "Re-states the pacing contract into every live session it can reach: every\n" +
			"configured environment's own AI session (over that environment's MCP edge)\n" +
			"and every persisted orchestrator definition. Reports each target's outcome —\n" +
			"pushed, or skipped and why — rather than only reporting that it ran.\n\n" +
			"A CLI/MCP process has no channel into a desktop-held orchestrator's live PTY,\n" +
			"so every orchestrator is reported skipped as unreachable from this transport;\n" +
			"only the desktop's own automatic pass can push those. An environment with no\n" +
			"currently open MCP edge (nobody has it open in the desktop) reports skipped as\n" +
			"not alive, since there is no live session there to push.\n\n" +
			"Pass TENANT and ENVIRONMENT to whip one environment; omit both to whip every\n" +
			"configured environment plus every persisted orchestrator.",
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
			return runWhipCommand(cmd.Context(), commandContext(cmd), store, resolveOpen, tenant, environment, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "Whip only this tenant's environment (requires --environment)")
	cmd.Flags().StringVar(&environment, "environment", "", "Whip only this environment (requires --tenant)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write JSON output")
	addDryRunFlag(cmd)
	return cmd
}

func runWhipCommand(ctx context.Context, commandCtx common.Context, store common.ListStore, resolveOpen OpenResolver, tenant, environment string, jsonOutput bool) error {
	targets, err := resolveWhipEnvironmentTargets(store, tenant, environment)
	if err != nil {
		return err
	}

	report := common.WhipReport{DryRun: commandCtx.DryRun}
	for _, target := range targets {
		report.Results = append(report.Results, whipOneEnvironment(ctx, commandCtx, resolveOpen, target.tenant, target.environment))
	}

	globalConfig, _, _ := store.LoadERunConfig()
	whipConfig := common.ResolveWhipConfig(globalConfig.Whip)
	now := time.Now()
	for _, candidate := range common.ListWhipOrchestratorCandidates(globalConfig.Orchestrators) {
		decision, reason := common.DecideWhip(candidate, now, whipConfig, true)
		report.Results = append(report.Results, common.WhipResult{Candidate: candidate, Decision: decision, Reason: reason})
	}

	if jsonOutput {
		encoder := json.NewEncoder(commandCtx.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	return writeWhipReport(commandCtx, report)
}

type whipEnvironmentTarget struct {
	tenant      string
	environment string
}

// resolveWhipEnvironmentTargets lists every configured environment when
// neither TENANT nor ENVIRONMENT is given, or validates and returns the one
// named pair otherwise. Enumeration never defaults a bare tenant/environment
// to the ambient scope the way scopedOpenParams does elsewhere -- a whip with
// no arguments means "everything configured", not "the current directory's
// default".
func resolveWhipEnvironmentTargets(store common.ListStore, tenant, environment string) ([]whipEnvironmentTarget, error) {
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

// whipOneEnvironment calls the environment's own "whip" tool over its MCP
// edge, preview-guarded under --dry-run so the call itself is the read that
// resolves what would happen without ever writing into the session -- the
// same guarantee RunLocalEnvironmentWhip's dryRun parameter makes on the pod
// side. An environment with no reachable edge (nobody has it open in the
// desktop right now) resolves to "not alive": there is no live session there
// to push, whether or not this was a dry run.
func whipOneEnvironment(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, tenant, environment string) common.WhipResult {
	id := tenant + "/" + environment
	notAlive := common.WhipResult{
		Candidate: common.WhipCandidate{Kind: common.WhipTargetEnvironment, ID: id, Name: id, Reachable: true, Alive: false},
		Decision:  common.WhipDecisionNone,
		Reason:    common.WhipReasonNotAlive,
	}

	target, err := resolveMCPEdgeTarget(commandCtx, resolveOpen, scopedOpenParams(tenant, environment))
	if err != nil {
		notAlive.Error = err.Error()
		return notAlive
	}
	// Restate the tenant/environment this host already resolved rather than
	// leaving the tool to infer them from the server's own bound context: it is
	// the stronger assertion the issue this fixes asked for, and it is what lets
	// a stale edge pointed at the wrong environment surface as a named mismatch
	// instead of a silent act on the wrong one (resolveLocalTarget,
	// erun-mcp/runtime.go).
	arguments := map[string]any{"preview": commandCtx.DryRun, "tenant": target.tenant, "environment": target.environment}
	commandCtx.TraceCommand("", "mcp", "tools/call", target.endpoint, "whip", compactMCPArguments(arguments))

	// A failed attempt (network error, target refusal, malformed response) is
	// reported as failed(), not folded into notAlive: the whip tool itself
	// already reports a dead session as a *successful* result carrying
	// WhipReasonNotAlive, so an error here never means "no live session" --
	// it means the call itself did not work, which is a different problem
	// asking for different operator action (root AGENTS.md's "Smooth,
	// Seamless, No Dead Ends" -- distinguish causes before writing copy).
	failed := func(detail string) common.WhipResult {
		return common.WhipResult{
			Candidate: common.WhipCandidate{Kind: common.WhipTargetEnvironment, ID: id, Name: id, Reachable: true, Alive: false},
			Decision:  common.WhipDecisionNone,
			Reason:    common.WhipReasonCallFailed,
			Error:     detail,
		}
	}

	result, err := callMCPToolWithReattach(ctx, commandCtx, target, "whip", arguments, false)
	if err != nil {
		return failed(err.Error())
	}
	var decoded common.WhipResult
	if len(result.Structured) == 0 {
		return failed(fmt.Sprintf("%s/%s returned no result for whip", tenant, environment))
	}
	if err := json.Unmarshal(result.Structured, &decoded); err != nil {
		return failed(fmt.Sprintf("decode the whip result from %s/%s: %v", tenant, environment, err))
	}
	return decoded
}

func writeWhipReport(ctx common.Context, report common.WhipReport) error {
	for _, result := range report.Results {
		if err := writeLabeledValue(ctx, whipResultLabel(result), whipResultValue(result)); err != nil {
			return err
		}
	}
	return nil
}

func whipResultLabel(result common.WhipResult) string {
	switch result.Candidate.Kind {
	case common.WhipTargetOrchestrator:
		label := strings.TrimSpace(result.Candidate.Name)
		if label == "" {
			label = result.Candidate.ID
		}
		return "orchestrator " + label
	default:
		return result.Candidate.ID
	}
}

func whipResultValue(result common.WhipResult) string {
	switch result.Decision {
	case common.WhipDecisionNudge:
		if result.Pushed {
			return fmt.Sprintf("pushed (consecutive nudge %d)", result.Candidate.NudgeCount+1)
		}
		if result.Error != "" {
			return "push failed: " + result.Error
		}
		return "would push (dry-run)"
	case common.WhipDecisionCap:
		return "capped: stopped nudging after repeated silence"
	default:
		if result.Reason == common.WhipReasonCallFailed {
			return "call failed: " + result.Error
		}
		value := "skipped — " + string(result.Reason)
		if result.Error != "" {
			value += ": " + result.Error
		}
		return value
	}
}
