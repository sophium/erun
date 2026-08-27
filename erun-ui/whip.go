package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// WhipNow is the desktop's own operator-triggered whip pass -- the control
// the CLI and MCP surfaces already carried and the desktop did not. It reaches both
// populations `erun whip` reaches, over the two channels the desktop already
// holds open: each configured environment's own MCP edge, and each live
// orchestrator's own PTY. Every target lands in the returned report, pushed
// or skipped with its reason, so a click answers "did it run" without the
// operator hunting for a refresh.
func (a *App) WhipNow() (uiWhipReport, error) {
	results, err := a.whipAllEnvironmentsNow(a.backgroundContext())
	if err != nil {
		return uiWhipReport{}, err
	}
	for _, outcome := range a.whipAllOrchestratorsNow() {
		results = append(results, orchestratorWhipOutcomeToResult(outcome))
	}
	return whipReportToUI(results), nil
}

// whipAllEnvironmentsNow whips every configured environment, one WhipResult
// each. An environment with no MCP edge currently open in the desktop reports
// skipped as not-alive -- the same semantics erun-cli's own
// `whipOneEnvironment` reports for an environment nobody has opened.
func (a *App) whipAllEnvironmentsNow(ctx context.Context) ([]eruncommon.WhipResult, error) {
	tenants, err := a.deps.store.ListTenantConfigs()
	if err != nil {
		return nil, fmt.Errorf("whip: listing tenants: %w", err)
	}
	type whipTarget struct{ tenant, environment string }
	var targets []whipTarget
	for _, tenant := range tenants {
		envs, err := a.deps.store.ListEnvConfigs(tenant.Name)
		if err != nil {
			return nil, fmt.Errorf("whip: listing %s's environments: %w", tenant.Name, err)
		}
		for _, env := range envs {
			targets = append(targets, whipTarget{tenant: tenant.Name, environment: env.Name})
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].tenant != targets[j].tenant {
			return targets[i].tenant < targets[j].tenant
		}
		return targets[i].environment < targets[j].environment
	})

	results := make([]eruncommon.WhipResult, 0, len(targets))
	for _, target := range targets {
		results = append(results, a.whipOneEnvironmentNow(ctx, target.tenant, target.environment))
	}
	return results, nil
}

// whipOneEnvironmentNow pushes one environment's own AI session through its
// MCP edge, reusing the pod's own decision (eruncommon.RunLocalEnvironmentWhip,
// run by the "whip" MCP tool) rather than re-deriving it host-side. An
// environment the desktop has no live edge to at all -- nobody has opened it
// in this session -- is reported not-alive without attempting the call. A
// stale forward (something holds the port, but the edge behind it never
// answers) is reported unreachable instead: a pod may well be running a
// session behind it, so reporting not-alive there would tell the operator
// there is nothing to push when the true answer is "cannot tell" -- the same
// distinction jobsReachability (environment_jobs.go) already draws.
func (a *App) whipOneEnvironmentNow(ctx context.Context, tenant, environment string) eruncommon.WhipResult {
	id := tenant + "/" + environment
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{Tenant: tenant, Environment: environment})
	if err != nil {
		return eruncommon.WhipResult{
			Candidate: eruncommon.WhipCandidate{Kind: eruncommon.WhipTargetEnvironment, ID: id, Name: id, Reachable: true, Alive: false},
			Decision:  eruncommon.WhipDecisionNone,
			Reason:    eruncommon.WhipReasonNotAlive,
			Error:     err.Error(),
		}
	}
	mcpPort := eruncommon.MCPPortForResult(result)
	if !a.deps.canReachMCPEndpoint(mcpPort) {
		if a.classifyMCPUnreachable(mcpPort) == eruncommon.LocalMCPStaleForward {
			return eruncommon.WhipResult{
				Candidate: eruncommon.WhipCandidate{Kind: eruncommon.WhipTargetEnvironment, ID: id, Name: id, Reachable: false},
				Decision:  eruncommon.WhipDecisionNone,
				Reason:    eruncommon.WhipReasonUnreachable,
				Error:     eruncommon.DescribeLocalMCPUnreachable(tenant, environment, mcpPort),
			}
		}
		return eruncommon.WhipResult{
			Candidate: eruncommon.WhipCandidate{Kind: eruncommon.WhipTargetEnvironment, ID: id, Name: id, Reachable: true, Alive: false},
			Decision:  eruncommon.WhipDecisionNone,
			Reason:    eruncommon.WhipReasonNotAlive,
		}
	}

	decoded, err := a.deps.whipEnvironment(ctx, mcpEndpointForOpenResult(result), a.mcpBearer(tenant, environment))
	if err != nil {
		return eruncommon.WhipResult{
			Candidate: eruncommon.WhipCandidate{Kind: eruncommon.WhipTargetEnvironment, ID: id, Name: id, Reachable: true, Alive: false},
			Decision:  eruncommon.WhipDecisionNone,
			Reason:    eruncommon.WhipReasonNotAlive,
			Error:     err.Error(),
		}
	}
	return decoded
}

// orchestratorWhipOutcomeToResult folds one orchestratorWhipOutcome into the
// same eruncommon.WhipResult shape the environment side already reports in,
// so WhipNow's report is one flat list rather than two differently-shaped
// ones. Pushed is inferred from the decision: the reconciler does not
// separately surface a nudge write failure or an in-flight typing deferral to
// its caller today, so a decided nudge is reported pushed, matching what the
// automatic pass has always assumed.
func orchestratorWhipOutcomeToResult(outcome orchestratorWhipOutcome) eruncommon.WhipResult {
	name := strings.TrimSpace(outcome.name)
	if name == "" {
		name = outcome.id
	}
	decision := whipDecisionFromOrchestratorPacing(outcome.decision)
	return eruncommon.WhipResult{
		Candidate: eruncommon.WhipCandidate{
			Kind:      eruncommon.WhipTargetOrchestrator,
			ID:        outcome.id,
			Name:      name,
			Reachable: true,
			Alive:     outcome.reason != orchestratorPacingReasonNotAlive,
		},
		Decision: decision,
		Reason:   whipReasonFromOrchestratorPacing(outcome.reason),
		Pushed:   decision == eruncommon.WhipDecisionNudge,
	}
}
