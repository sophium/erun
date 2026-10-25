package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
)

// WhipNow whips an explicit target list; an empty list is refused rather than
// read as "every target", to bound a single click's blast radius.
func (s *Service) WhipNow(targets []WhipTargetRef) (WhipReport, error) {
	if len(targets) == 0 {
		return WhipReport{}, fmt.Errorf("whip: no targets selected")
	}
	wantEnvironments := make(map[string]struct{}, len(targets))
	wantOrchestrators := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		switch target.Kind {
		case whipTargetKindEnvironment:
			wantEnvironments[target.ID] = struct{}{}
		case whipTargetKindOrchestrator:
			wantOrchestrators[target.ID] = struct{}{}
		default:
			return WhipReport{}, fmt.Errorf("whip: unknown target kind %q", target.Kind)
		}
	}

	results, err := s.whipEnvironmentsNow(s.d.Context(), wantEnvironments)
	if err != nil {
		return WhipReport{}, err
	}
	for _, outcome := range s.whipOrchestratorsNow(wantOrchestrators) {
		results = append(results, orchestratorWhipOutcomeToResult(outcome))
	}
	return whipReportToUI(results), nil
}

type whipEnvironmentTarget struct{ tenant, environment string }

// listWhipEnvironmentTargets is the single source for both selection and push,
// so the two never disagree on eligibility.
func (s *Service) listWhipEnvironmentTargets() ([]whipEnvironmentTarget, error) {
	tenants, err := s.d.Store.ListTenantConfigs()
	if err != nil {
		return nil, fmt.Errorf("whip: listing tenants: %w", err)
	}
	var targets []whipEnvironmentTarget
	for _, tenant := range tenants {
		envs, err := s.d.Store.ListEnvConfigs(tenant.Name)
		if err != nil {
			return nil, fmt.Errorf("whip: listing %s's environments: %w", tenant.Name, err)
		}
		for _, env := range envs {
			// A host env has no pod, so no AI session to push.
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

// whipEnvironmentsNow silently drops requested ids that no longer resolve.
func (s *Service) whipEnvironmentsNow(ctx context.Context, want map[string]struct{}) ([]model.WhipResult, error) {
	targets, err := s.listWhipEnvironmentTargets()
	if err != nil {
		return nil, err
	}
	results := make([]model.WhipResult, 0, len(want))
	for _, target := range targets {
		id := target.tenant + "/" + target.environment
		if _, ok := want[id]; !ok {
			continue
		}
		results = append(results, s.whipOneEnvironmentNow(ctx, target.tenant, target.environment))
	}
	return results, nil
}

// whipOneEnvironmentNow reuses the pod's own whip decision. A stale forward is
// unreachable, not not-alive: a session may still run behind it.
func (s *Service) whipOneEnvironmentNow(ctx context.Context, tenant, environment string) model.WhipResult {
	id := tenant + "/" + environment
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{Tenant: tenant, Environment: environment})
	if err != nil {
		return model.WhipResult{
			Candidate: model.WhipCandidate{Kind: model.WhipTargetEnvironment, ID: id, Name: id, Reachable: true, Alive: false},
			Decision:  model.WhipDecisionNone,
			Reason:    model.WhipReasonNotAlive,
			Error:     err.Error(),
		}
	}
	mcpPort := config.MCPPortForResult(result)
	if !s.d.CanReachMCPEndpoint(mcpPort) {
		if s.d.MCPForwardStale(mcpPort) {
			return model.WhipResult{
				Candidate: model.WhipCandidate{Kind: model.WhipTargetEnvironment, ID: id, Name: id, Reachable: false},
				Decision:  model.WhipDecisionNone,
				Reason:    model.WhipReasonUnreachable,
				Error:     mcp.DescribeLocalMCPUnreachable(tenant, environment, mcpPort),
			}
		}
		return model.WhipResult{
			Candidate: model.WhipCandidate{Kind: model.WhipTargetEnvironment, ID: id, Name: id, Reachable: true, Alive: false},
			Decision:  model.WhipDecisionNone,
			Reason:    model.WhipReasonNotAlive,
		}
	}

	decoded, err := s.d.WhipEnvironment(ctx, tenant, environment, s.d.MCPEndpoint(result), s.d.MCPBearer(tenant, environment))
	if err != nil {
		// A dead session is a successful result, so an error is a call failure.
		return model.WhipResult{
			Candidate: model.WhipCandidate{Kind: model.WhipTargetEnvironment, ID: id, Name: id, Reachable: true, Alive: false},
			Decision:  model.WhipDecisionNone,
			Reason:    model.WhipReasonCallFailed,
			Error:     err.Error(),
		}
	}
	// Host-side identity never depends on what the pod echoes back.
	decoded.Candidate.Kind = model.WhipTargetEnvironment
	decoded.Candidate.ID = id
	decoded.Candidate.Name = id
	return decoded
}

// orchestratorWhipOutcomeToResult reports a decided nudge as pushed: the
// reconciler does not surface write failures.
func orchestratorWhipOutcomeToResult(outcome orchestratorWhipOutcome) model.WhipResult {
	name := strings.TrimSpace(outcome.name)
	if name == "" {
		name = outcome.id
	}
	decision := whipDecisionFromOrchestratorPacing(outcome.decision)
	return model.WhipResult{
		Candidate: model.WhipCandidate{
			Kind:      model.WhipTargetOrchestrator,
			ID:        outcome.id,
			Name:      name,
			Reachable: true,
			Alive:     outcome.reason != orchestratorPacingReasonNotAlive,
		},
		Decision: decision,
		Reason:   whipReasonFromOrchestratorPacing(outcome.reason),
		Pushed:   decision == model.WhipDecisionNudge,
	}
}
