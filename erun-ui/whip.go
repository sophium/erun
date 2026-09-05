package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// WhipNow is the desktop's own operator-triggered whip pass -- the control
// the CLI and MCP surfaces already carried and the desktop did not. Unlike
// those transports (which whip everything configured when given no target), a
// desktop click always resolves an explicit target list first -- the focused
// environment/orchestrator by default, or whatever the operator checked in the
// selection surface -- and passes it here. An empty list is refused rather
// than read as "every target": fanning a live-session write across every
// configured environment and orchestrator on a single click is exactly the
// blast radius this replaces (erun#1700). Every requested target still lands
// in the returned report, pushed or skipped with its reason, so a click
// answers "did it run" without the operator hunting for a refresh.
func (a *App) WhipNow(targets []uiWhipTargetRef) (uiWhipReport, error) {
	if len(targets) == 0 {
		return uiWhipReport{}, fmt.Errorf("whip: no targets selected")
	}
	wantEnvironments := make(map[string]struct{}, len(targets))
	wantOrchestrators := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		switch target.Kind {
		case uiWhipTargetKindEnvironment:
			wantEnvironments[target.ID] = struct{}{}
		case uiWhipTargetKindOrchestrator:
			wantOrchestrators[target.ID] = struct{}{}
		default:
			return uiWhipReport{}, fmt.Errorf("whip: unknown target kind %q", target.Kind)
		}
	}

	results, err := a.whipEnvironmentsNow(a.backgroundContext(), wantEnvironments)
	if err != nil {
		return uiWhipReport{}, err
	}
	for _, outcome := range a.whipOrchestratorsNow(wantOrchestrators) {
		results = append(results, orchestratorWhipOutcomeToResult(outcome))
	}
	return whipReportToUI(results), nil
}

// whipEnvironmentTarget is one environment the whip selection surface can
// offer -- tenant/name gathered without deciding or pushing anything.
type whipEnvironmentTarget struct{ tenant, environment string }

// listWhipEnvironmentTargets enumerates every environment eligible to be
// whipped, sorted for stable rendering. It is the single source both
// WhipTargets (the selection surface's population) and whipEnvironmentsNow
// (the actual push) enumerate from, so "select all environments" and "push
// the selected environments" can never disagree about what is eligible.
func (a *App) listWhipEnvironmentTargets() ([]whipEnvironmentTarget, error) {
	tenants, err := a.deps.store.ListTenantConfigs()
	if err != nil {
		return nil, fmt.Errorf("whip: listing tenants: %w", err)
	}
	var targets []whipEnvironmentTarget
	for _, tenant := range tenants {
		envs, err := a.deps.store.ListEnvConfigs(tenant.Name)
		if err != nil {
			return nil, fmt.Errorf("whip: listing %s's environments: %w", tenant.Name, err)
		}
		for _, env := range envs {
			// A host-type env has no pod and no cluster contact at all
			// (EnvConfig.HasPod's doc comment), so it can never carry an AI
			// session to push -- listing it as a selectable target, or
			// reporting it every pass, is pure noise, not a skip the operator
			// can act on.
			if !env.HasPod() {
				continue
			}
			targets = append(targets, whipEnvironmentTarget{tenant: tenant.Name, environment: env.Name})
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

// whipEnvironmentsNow pushes only the requested environments (keyed
// "tenant/environment"), one WhipResult each. An environment with no MCP edge
// currently open in the desktop reports skipped as not-alive -- the same
// semantics erun-cli's own `whipOneEnvironment` reports for an environment
// nobody has opened. A requested id that no longer resolves against the
// configured population is silently excluded rather than attempted, since
// there is nothing real behind it to push.
func (a *App) whipEnvironmentsNow(ctx context.Context, want map[string]struct{}) ([]eruncommon.WhipResult, error) {
	targets, err := a.listWhipEnvironmentTargets()
	if err != nil {
		return nil, err
	}
	results := make([]eruncommon.WhipResult, 0, len(want))
	for _, target := range targets {
		id := target.tenant + "/" + target.environment
		if _, ok := want[id]; !ok {
			continue
		}
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

	decoded, err := a.deps.whipEnvironment(ctx, tenant, environment, mcpEndpointForOpenResult(result), a.mcpBearer(tenant, environment))
	if err != nil {
		// A failed call is not the same claim as WhipReasonNotAlive: the whip
		// tool itself already reports a dead session as a *successful* result
		// carrying that reason, so an error here always means the call itself
		// did not work (a target mismatch, a stale runtime image, a transport
		// failure) -- a different problem asking for different operator action
		// than "go start the session" (root AGENTS.md's "Smooth, Seamless, No
		// Dead Ends" -- distinguish causes before writing copy).
		return eruncommon.WhipResult{
			Candidate: eruncommon.WhipCandidate{Kind: eruncommon.WhipTargetEnvironment, ID: id, Name: id, Reachable: true, Alive: false},
			Decision:  eruncommon.WhipDecisionNone,
			Reason:    eruncommon.WhipReasonCallFailed,
			Error:     err.Error(),
		}
	}
	// The desktop already knows which target this is -- it built id above and
	// used it in every other return path. Stamp it here too rather than
	// trusting the remote payload for host-side identity: a report row must
	// never depend on what a pod chose to echo back.
	decoded.Candidate.Kind = eruncommon.WhipTargetEnvironment
	decoded.Candidate.ID = id
	decoded.Candidate.Name = id
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
