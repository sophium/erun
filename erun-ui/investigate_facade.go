package main

import (
	"time"

	"github.com/sophium/erun/erun-ui/internal/investigate"
	orchestratorsvc "github.com/sophium/erun/erun-ui/internal/orchestrator"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// The failure-investigation surface; the bounds live in internal/investigate.

// InvestigateFailure stages a failure report to a host file and, within the
// investigation bounds, spawns a transient orchestrator to investigate it. A
// refusal is returned as an error naming the bound.
func (a *App) InvestigateFailure(report, tenant, environment string, cols, rows int) (orchestratorsvc.Info, error) {
	return a.spawnFailureAgent(investigate.InvestigateRequest(report, tenant, environment, cols, rows))
}

// spawnFailureAgent is the bounded spawn shared by investigations and
// bug-report drafts; the session itself is the orchestrator service's.
func (a *App) spawnFailureAgent(request investigate.Request) (orchestratorsvc.Info, error) {
	var info orchestratorsvc.Info
	err := a.investigations.Spawn(request, func(spawn investigate.Transient) error {
		var err error
		info, err = a.orchestrator.SpawnTransient(orchestratorsvc.TransientSpawn(spawn))
		return err
	})
	if err != nil {
		return orchestratorsvc.Info{}, err
	}
	return info, nil
}

// newInvestigationService takes the clock and lifetime timer so a test can
// drive the bounds without sleeping; nil keeps the wall clock. Its seams are
// read per call because the orchestrator, terminal and envops services are
// built after it.
func (a *App) newInvestigationService(now func() time.Time, after func(time.Duration, func()) investigate.Timer) *investigate.Service {
	return investigate.New(investigate.Deps{
		Running:     func(id string) bool { return a.orchestrator.Running(id) },
		StopSession: func(id string) { a.orchestrator.StopOrchestratorSession(id) },
		SessionPID:  a.orchestratorSessionPID,
		HostWorkspace: func(selection uimodel.Selection) (string, error) {
			_, path, err := a.envops.ResolveHostWorkspace(selection)
			return path, err
		},
		Emit:      a.emit,
		ReportDir: a.deps.investigationReportDir,
		Now:       now,
		After:     after,
	})
}

func (a *App) orchestratorSessionPID(id string) int {
	pid := 0
	a.terminal.WithLock(func(l *terminal.Locked) {
		managed := l.Session(terminal.OrchestratorSessionKey(id))
		if managed == nil || managed.Closed || managed.PTY == nil {
			return
		}
		pid = managed.PTY.Pid()
	})
	return pid
}
