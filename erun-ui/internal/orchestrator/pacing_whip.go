package orchestrator

import (
	"sort"
	"sync"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	orchcmd "github.com/sophium/erun/erun-common/command/orchestrator"
)

// orchestratorWhipConfig is reloaded from config each tick; the mutex guards
// reads from the manual whip-now goroutine.
var (
	orchestratorWhipConfigMu sync.RWMutex
	orchestratorWhipConfig   = orchcmd.ResolveWhipConfig(nil)
)

func setOrchestratorWhipConfig(cfg orchcmd.WhipConfig) {
	orchestratorWhipConfigMu.Lock()
	orchestratorWhipConfig = cfg
	orchestratorWhipConfigMu.Unlock()
}

func getOrchestratorWhipConfig() orchcmd.WhipConfig {
	orchestratorWhipConfigMu.RLock()
	defer orchestratorWhipConfigMu.RUnlock()
	return orchestratorWhipConfig
}

// refreshOrchestratorWhipConfig falls back to defaults on a missing or unreadable config.
func refreshOrchestratorWhipConfig() {
	config, _, _ := configpkg.LoadERunConfig()
	setOrchestratorWhipConfig(orchcmd.ResolveWhipConfig(config.Whip))
}

// decideOrchestratorWhip lets an explicit whip ignore staleness but never the cap.
func decideOrchestratorWhip(c orchestratorPacingCandidate, now time.Time, explicit bool) (orchestratorPacingDecision, orchestratorPacingReason) {
	candidate := model.WhipCandidate{
		Kind: model.WhipTargetOrchestrator,
		// Only an unmanaged orchestrator has no pty here to write into.
		Reachable:    !c.unmanaged,
		Alive:        c.alive,
		LastActiveAt: c.lastActiveAt,
		NudgeCount:   c.nudgeCount,
		Capped:       c.capped,
	}
	decision, reason := orchcmd.DecideWhip(candidate, now, getOrchestratorWhipConfig(), explicit)
	return orchestratorPacingDecisionFromWhip(decision), orchestratorPacingReasonFromWhip(reason)
}

func orchestratorPacingDecisionFromWhip(decision model.WhipDecision) orchestratorPacingDecision {
	switch decision {
	case model.WhipDecisionNudge:
		return orchestratorPacingNudge
	case model.WhipDecisionCap:
		return orchestratorPacingCap
	case model.WhipDecisionNone:
		fallthrough
	default:
		return orchestratorPacingNone
	}
}

// orchestratorPacingReasonFromWhip maps DecideWhip reasons; Unreachable means unmanaged.
func orchestratorPacingReasonFromWhip(reason model.WhipReason) orchestratorPacingReason {
	switch reason {
	case model.WhipReasonNotAlive:
		return orchestratorPacingReasonNotAlive
	case model.WhipReasonUnreachable:
		return orchestratorPacingReasonUnreachable
	case model.WhipReasonAlreadyCapped:
		return orchestratorPacingReasonAlreadyCapped
	case model.WhipReasonCapCrossed:
		return orchestratorPacingReasonCapCrossed
	case model.WhipReasonNudge:
		return orchestratorPacingReasonNudge
	case model.WhipReasonFresh, model.WhipReasonCallFailed, model.WhipReasonChannelDown:
		fallthrough
	default:
		return orchestratorPacingReasonFresh
	}
}

// whipDecisionFromOrchestratorPacing is orchestratorPacingDecisionFromWhip's inverse.
func whipDecisionFromOrchestratorPacing(decision orchestratorPacingDecision) model.WhipDecision {
	switch decision {
	case orchestratorPacingNudge:
		return model.WhipDecisionNudge
	case orchestratorPacingCap:
		return model.WhipDecisionCap
	case orchestratorPacingNone:
		fallthrough
	default:
		return model.WhipDecisionNone
	}
}

// whipReasonFromOrchestratorPacing is orchestratorPacingReasonFromWhip's inverse.
func whipReasonFromOrchestratorPacing(reason orchestratorPacingReason) model.WhipReason {
	switch reason {
	case orchestratorPacingReasonNotAlive:
		return model.WhipReasonNotAlive
	case orchestratorPacingReasonAlreadyCapped:
		return model.WhipReasonAlreadyCapped
	case orchestratorPacingReasonCapCrossed:
		return model.WhipReasonCapCrossed
	case orchestratorPacingReasonNudge:
		return model.WhipReasonNudge
	case orchestratorPacingReasonFresh, orchestratorPacingReasonUnreachable, orchestratorPacingReasonEnvBusy:
		fallthrough
	default:
		return model.WhipReasonFresh
	}
}

// orchestratorWhipOutcome is one orchestrator's result from an explicit whip.
type orchestratorWhipOutcome struct {
	id       string
	name     string
	decision orchestratorPacingDecision
	reason   orchestratorPacingReason
}

type orchestratorWhipTarget struct{ id, name string }

// listWhipOrchestratorTargets returns the same union whipOrchestratorsNow decides
// against, so selection and push never disagree about who exists.
func (s *Service) listWhipOrchestratorTargets() []orchestratorWhipTarget {
	rows := s.orchestratorPacingRows()
	seen := make(map[string]struct{}, len(rows))
	targets := make([]orchestratorWhipTarget, 0, len(rows))
	for _, row := range rows {
		targets = append(targets, orchestratorWhipTarget{id: row.id, name: row.name})
		seen[row.id] = struct{}{}
	}
	if configs, err := s.loadOrchestratorConfigs(); err == nil {
		for _, config := range configs {
			if _, ok := seen[config.ID]; ok {
				continue
			}
			targets = append(targets, orchestratorWhipTarget{id: config.ID, name: config.Name})
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].name != targets[j].name {
			return targets[i].name < targets[j].name
		}
		return targets[i].id < targets[j].id
	})
	return targets
}

// whipOrchestratorsNow pushes the requested ids regardless of staleness, each still capped.
func (s *Service) whipOrchestratorsNow(want map[string]struct{}) []orchestratorWhipOutcome {
	refreshOrchestratorWhipConfig()
	now := time.Now()
	rows := s.orchestratorPacingRows()
	outcomes := make([]orchestratorWhipOutcome, 0, len(want))
	whipped := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, ok := want[row.id]; !ok {
			continue
		}
		decision, reason := s.reconcileOrchestratorPacingOne(row, now, true)
		outcomes = append(outcomes, orchestratorWhipOutcome{id: row.id, name: row.name, decision: decision, reason: reason})
		whipped[row.id] = struct{}{}
	}
	// A requested orchestrator with no session must still be reported as skipped.
	configs, err := s.loadOrchestratorConfigs()
	if err != nil {
		// The sessions above are still a truthful partial answer.
		return outcomes
	}
	for _, config := range configs {
		if _, ok := want[config.ID]; !ok {
			continue
		}
		if _, ok := whipped[config.ID]; ok {
			continue
		}
		outcomes = append(outcomes, orchestratorWhipOutcome{
			id:       config.ID,
			name:     config.Name,
			decision: orchestratorPacingNone,
			reason:   orchestratorPacingReasonNotAlive,
		})
	}
	return outcomes
}
