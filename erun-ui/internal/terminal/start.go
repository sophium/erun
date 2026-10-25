package terminal

import (
	"context"
	"encoding/base64"
	"fmt"
	goruntime "runtime"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// StartSession spawns the ERun tab's `erun open` PTY through the per-env action
// runner, so a parallel AI-tab open cannot duplicate a build+deploy.
func (s *Service) StartSession(selection uimodel.Selection, slot, cols, rows int) (uimodel.StartSessionResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("start ERun session", selection.Tenant, selection.Environment); err != nil {
		return uimodel.StartSessionResult{}, err
	}
	return s.d.EnqueueGated(selection, "open", func(ctx context.Context) (uimodel.StartSessionResult, *Session, error) {
		return s.runOpenSession(ctx, selection, slot, cols, rows)
	})
}

func ClampSize(cols, rows int) (int, int) {
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 34
	}
	return cols, rows
}

func (s *Service) runOpenSession(ctx context.Context, selection uimodel.Selection, slot, cols, rows int) (uimodel.StartSessionResult, *Session, error) {
	cols, rows = ClampSize(cols, rows)

	key := openSessionKey(selection, slot)
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return uimodel.StartSessionResult{}, nil, err
	}

	s.mu.Lock()
	if existing := s.sessions[key]; existing != nil && !existing.Closed && existing.PTY != nil {
		s.mu.Unlock()
		go s.d.StartWorkspaceSync(selection)
		existing.SignalReady(nil)
		return uimodel.StartSessionResult{
			SessionID: existing.Serial,
			Selection: existing.Selection,
			Slot:      slot,
		}, existing, nil
	}
	s.mu.Unlock()

	// Opening never deploys; that is the caller's explicit action.
	s.d.EnsureRuntimeOnce(selection)
	openParams := StartParams{
		Dir:        ResolveStartDir(result.RepoPath),
		Executable: s.d.ResolveCLIPath(),
		Args:       WithAppSession(BuildOpenArgs(result.Tenant, result.Environment), fmt.Sprintf("open-%d", slot), false, false),
		Env:        []string{s.d.AppSessionEnv},
		Cols:       cols,
		Rows:       rows,
	}
	session, err := s.d.StartTerminal(openParams)
	if err != nil {
		return uimodel.StartSessionResult{}, nil, err
	}

	s.mu.Lock()
	s.nextSerial++
	serial := s.nextSerial
	managed := &Session{
		PTY:                    session,
		Selection:              selection,
		Key:                    key,
		Serial:                 serial,
		Slot:                   slot,
		Kind:                   KindOpen,
		AppSession:             fmt.Sprintf("open-%d", slot),
		BlocksIdleStop:         true,
		ClearIdleBlockOnOutput: true,
		Respawn: func() (PTY, error) {
			return s.d.StartTerminal(ReconnectParams(openParams))
		},
		StartedAt: time.Now(),
	}
	s.sessions[key] = managed
	s.busyEnvs[uimodel.SelectionKey(selection)]++
	s.mu.Unlock()

	s.RecordActivity(selection)
	s.d.RememberKubeContext(selection.KubernetesContext)
	s.d.LockIfDeployInFlight(selection, serial)
	s.SpawnStream(managed)
	go s.d.StartWorkspaceSync(selection)
	go s.d.StartCloudRefresher(selection)

	// Opening is the deliberate wake for a stopped runtime, so release the stop
	// latch or the reconnect gate keeps refusing.
	s.d.ClearRuntimeStopped(selection)
	s.emitEnvStatus(selection, "")
	s.LogSpawnedCommandToLocal(selection, "erun", FormatLocalCommandLog(FormatLaunchCommand(openParams), "ERun tab"))
	_ = ctx
	return uimodel.StartSessionResult{
		SessionID: serial,
		Selection: selection,
		Slot:      slot,
		Kind:      string(KindOpen),
	}, managed, nil
}

func (s *Service) StartLocalSession(selection uimodel.Selection, slot, cols, rows int) (uimodel.StartSessionResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("start local session", selection.Tenant, selection.Environment); err != nil {
		return uimodel.StartSessionResult{}, err
	}
	cols, rows = ClampSize(cols, rows)

	key := localSessionKey(selection, slot)

	repoPath := ""
	if result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	}); err == nil {
		repoPath = result.RepoPath
	}

	s.mu.Lock()
	if existing := s.sessions[key]; existing != nil && !existing.Closed && existing.PTY != nil {
		s.mu.Unlock()
		return uimodel.StartSessionResult{
			SessionID: existing.Serial,
			Selection: existing.Selection,
			Slot:      slot,
			Kind:      string(KindLocal),
		}, nil
	}
	s.mu.Unlock()

	executable, args := LocalShellCommand(goruntime.GOOS)
	params := StartParams{
		Dir:        ResolveStartDir(repoPath),
		Executable: executable,
		Args:       args,
		Env:        []string{s.d.AppSessionEnv},
		Cols:       cols,
		Rows:       rows,
	}
	session, err := s.d.StartTerminal(params)
	if err != nil {
		return uimodel.StartSessionResult{}, err
	}

	s.mu.Lock()
	s.nextSerial++
	serial := s.nextSerial
	managed := &Session{
		PTY:       session,
		Selection: selection,
		Key:       key,
		Serial:    serial,
		Slot:      slot,
		Kind:      KindLocal,
		StartedAt: time.Now(),
	}
	s.sessions[key] = managed
	s.mu.Unlock()

	if banner := localSessionBanner(selection); len(banner) > 0 {
		s.emitEvent(uimodel.TerminalOutputEvent, uimodel.TerminalOutputPayload{
			SessionID: serial,
			Data:      base64.StdEncoding.EncodeToString(banner),
		})
	}

	s.SpawnStream(managed)
	return uimodel.StartSessionResult{
		SessionID: serial,
		Selection: selection,
		Slot:      slot,
		Kind:      string(KindLocal),
	}, nil
}

// StartAISession spawns the AI tab's `erun open` PTY through the per-env queue.
// A fresh start held by another agent's activity lease reports occupied unless
// confirmed.
func (s *Service) StartAISession(selection uimodel.Selection, slot, cols, rows int, confirmed bool) (uimodel.StartSessionResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("start AI session", selection.Tenant, selection.Environment); err != nil {
		return uimodel.StartSessionResult{}, err
	}
	return s.d.EnqueueGated(selection, "ai", func(ctx context.Context) (uimodel.StartSessionResult, *Session, error) {
		return s.runAISession(ctx, selection, slot, cols, rows, confirmed)
	})
}

func (s *Service) runAISession(ctx context.Context, selection uimodel.Selection, slot, cols, rows int, confirmed bool) (uimodel.StartSessionResult, *Session, error) {
	cols, rows = ClampSize(cols, rows)

	key := aiSessionKey(selection, slot)
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return uimodel.StartSessionResult{}, nil, err
	}

	s.mu.Lock()
	if existing := s.sessions[key]; existing != nil && !existing.Closed && existing.PTY != nil {
		s.mu.Unlock()
		existing.SignalReady(nil)
		return uimodel.StartSessionResult{
			SessionID: existing.Serial,
			Selection: existing.Selection,
			Slot:      slot,
			Kind:      string(KindAI),
		}, existing, nil
	}
	s.mu.Unlock()

	if !confirmed {
		if occupants := s.aiSessionOccupants(selection); len(occupants) > 0 {
			return uimodel.StartSessionResult{
				Selection: selection,
				Slot:      slot,
				Kind:      string(KindAI),
				Occupancy: occupants,
			}, nil, nil
		}
	}

	s.d.EnsureRuntimeOnce(selection)
	params := StartParams{
		Dir:        ResolveStartDir(result.RepoPath),
		Executable: s.d.ResolveCLIPath(),
		// The pod session launches the AI tool once on create; reopening reattaches.
		Args: WithAppSession(BuildOpenArgs(result.Tenant, result.Environment), "ai", true, false),
		Env:  []string{s.d.AppSessionEnv},
		Cols: cols,
		Rows: rows,
	}
	session, err := s.d.StartTerminal(params)
	if err != nil {
		return uimodel.StartSessionResult{}, nil, err
	}

	s.mu.Lock()
	s.nextSerial++
	serial := s.nextSerial
	managed := &Session{
		PTY:        session,
		Selection:  selection,
		Key:        key,
		Serial:     serial,
		Slot:       slot,
		Kind:       KindAI,
		AppSession: "ai",
		Respawn: func() (PTY, error) {
			return s.d.StartTerminal(ReconnectParams(params))
		},
		StartedAt: time.Now(),
		LastCols:  cols,
		LastRows:  rows,
	}
	s.sessions[key] = managed
	s.mu.Unlock()

	s.d.RememberKubeContext(selection.KubernetesContext)
	s.d.LockIfDeployInFlight(selection, serial)
	s.SpawnStream(managed)

	s.LogSpawnedCommandToLocal(selection, "ai", FormatLocalCommandLog(FormatLaunchCommand(params), "AI tab"))
	_ = ctx
	return uimodel.StartSessionResult{
		SessionID: serial,
		Selection: selection,
		Slot:      slot,
		Kind:      string(KindAI),
	}, managed, nil
}

// aiSessionOccupants reads held leases, separately if the status read fails. A
// failed lease read returns none: this is a notice, not an access check.
func (s *Service) aiSessionOccupants(selection uimodel.Selection) []uimodel.EnvironmentLease {
	status, err := s.LoadIdleStatus(selection)
	if err == nil {
		return status.Leases
	}
	now := time.Now()
	leases, err := activity.LoadEnvironmentActivityLeases(selection.Tenant, selection.Environment, now)
	if err != nil {
		return nil
	}
	occupants := environmentLeasesToUI(leases, now)
	s.verifyLeaseJobIDs(selection.Tenant, selection.Environment, occupants)
	return occupants
}
