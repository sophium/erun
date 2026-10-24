package main

import (
	"context"
	"encoding/base64"
	"fmt"
	goruntime "runtime"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/open"
)

// StartSession spawns the ERun tab's `erun open` PTY through the per-env action
// runner, so a parallel AI-tab open cannot duplicate a build+deploy.
func (a *App) StartSession(selection uiSelection, slot, cols, rows int) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("start ERun session", selection.Tenant, selection.Environment); err != nil {
		return startSessionResult{}, err
	}
	return a.enqueueGatedSession(selection, gatedSessionKindOpen, func(ctx context.Context) (startSessionResult, *managedTerminal, error) {
		return a.runOpenSession(ctx, selection, slot, cols, rows)
	})
}

func clampTerminalSize(cols, rows int) (int, int) {
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 34
	}
	return cols, rows
}

func (a *App) runOpenSession(ctx context.Context, selection uiSelection, slot, cols, rows int) (startSessionResult, *managedTerminal, error) {
	cols, rows = clampTerminalSize(cols, rows)

	key := openSessionKey(selection, slot)
	result, err := open.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return startSessionResult{}, nil, err
	}

	a.mu.Lock()
	if existing := a.sessions[key]; existing != nil && !existing.closed && existing.session != nil {
		a.mu.Unlock()
		go a.startWorkspaceSyncForSelection(selection)
		existing.signalReady(nil)
		return startSessionResult{
			SessionID: existing.serial,
			Selection: existing.selection,
			Slot:      slot,
		}, existing, nil
	}
	a.mu.Unlock()

	// Opening never deploys; that is the caller's explicit action.
	a.ensureEnvRuntimeOnce(selection)
	openParams := startTerminalSessionParams{
		Dir:        resolveTerminalStartDir(result.RepoPath),
		Executable: a.deps.resolveCLIPath(),
		Args:       withAppSession(buildOpenArgs(result.Tenant, result.Environment), fmt.Sprintf("open-%d", slot), false, false),
		Env:        []string{appSessionEnvVar + "=1"},
		Cols:       cols,
		Rows:       rows,
	}
	session, err := a.deps.startTerminal(openParams)
	if err != nil {
		return startSessionResult{}, nil, err
	}

	a.mu.Lock()
	a.nextSerial++
	serial := a.nextSerial
	managed := &managedTerminal{
		session:                session,
		selection:              selection,
		key:                    key,
		serial:                 serial,
		slot:                   slot,
		kind:                   sessionKindOpen,
		appSession:             fmt.Sprintf("open-%d", slot),
		blocksIdleStop:         true,
		clearIdleBlockOnOutput: true,
		respawn: func() (terminalSession, error) {
			return a.deps.startTerminal(reconnectSessionParams(openParams))
		},
		startedAt: time.Now(),
	}
	a.sessions[key] = managed
	a.busyEnvs[selectionKey(selection)]++
	a.mu.Unlock()

	a.recordTerminalActivity(selection)
	a.rememberKubeContextForActivity(selection.KubernetesContext)
	a.lockNewlyJoinedSessionIfDeployInFlight(selection, serial)
	a.spawnStreamSession(managed)
	go a.startWorkspaceSyncForSelection(selection)
	go a.startCloudCredentialsRefresherForSelection(selection)

	// Opening is the deliberate wake for a stopped runtime, so release the stop
	// latch or the reconnect gate keeps refusing.
	a.clearRuntimeStopped(selection)
	a.emitEnvStatus(selection, "")
	a.logSpawnedCommandToLocal(selection, "erun", formatLocalCommandLog(formatLaunchCommand(openParams), "ERun tab"))
	_ = ctx
	return startSessionResult{
		SessionID: serial,
		Selection: selection,
		Slot:      slot,
		Kind:      string(sessionKindOpen),
	}, managed, nil
}

func (a *App) StartLocalSession(selection uiSelection, slot, cols, rows int) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("start local session", selection.Tenant, selection.Environment); err != nil {
		return startSessionResult{}, err
	}
	cols, rows = clampTerminalSize(cols, rows)

	key := localSessionKey(selection, slot)

	repoPath := ""
	if result, err := open.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	}); err == nil {
		repoPath = result.RepoPath
	}

	a.mu.Lock()
	if existing := a.sessions[key]; existing != nil && !existing.closed && existing.session != nil {
		a.mu.Unlock()
		return startSessionResult{
			SessionID: existing.serial,
			Selection: existing.selection,
			Slot:      slot,
			Kind:      string(sessionKindLocal),
		}, nil
	}
	a.mu.Unlock()

	executable, args := resolveLocalShellCommand(goruntime.GOOS)
	params := startTerminalSessionParams{
		Dir:        resolveTerminalStartDir(repoPath),
		Executable: executable,
		Args:       args,
		Env:        []string{appSessionEnvVar + "=1"},
		Cols:       cols,
		Rows:       rows,
	}
	session, err := a.deps.startTerminal(params)
	if err != nil {
		return startSessionResult{}, err
	}

	a.mu.Lock()
	a.nextSerial++
	serial := a.nextSerial
	managed := &managedTerminal{
		session:   session,
		selection: selection,
		key:       key,
		serial:    serial,
		slot:      slot,
		kind:      sessionKindLocal,
		startedAt: time.Now(),
	}
	a.sessions[key] = managed
	a.mu.Unlock()

	if banner := localSessionBanner(selection); len(banner) > 0 {
		a.emitEvent(terminalOutputEvent, terminalOutputPayload{
			SessionID: serial,
			Data:      base64.StdEncoding.EncodeToString(banner),
		})
	}

	a.spawnStreamSession(managed)
	return startSessionResult{
		SessionID: serial,
		Selection: selection,
		Slot:      slot,
		Kind:      string(sessionKindLocal),
	}, nil
}

// StartAISession spawns the AI tab's `erun open` PTY through the per-env queue.
// A fresh start held by another agent's activity lease reports occupied unless
// confirmed.
func (a *App) StartAISession(selection uiSelection, slot, cols, rows int, confirmed bool) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("start AI session", selection.Tenant, selection.Environment); err != nil {
		return startSessionResult{}, err
	}
	return a.enqueueGatedSession(selection, gatedSessionKindAI, func(ctx context.Context) (startSessionResult, *managedTerminal, error) {
		return a.runAISession(ctx, selection, slot, cols, rows, confirmed)
	})
}

func (a *App) runAISession(ctx context.Context, selection uiSelection, slot, cols, rows int, confirmed bool) (startSessionResult, *managedTerminal, error) {
	cols, rows = clampTerminalSize(cols, rows)

	key := aiSessionKey(selection, slot)
	result, err := open.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return startSessionResult{}, nil, err
	}

	a.mu.Lock()
	if existing := a.sessions[key]; existing != nil && !existing.closed && existing.session != nil {
		a.mu.Unlock()
		existing.signalReady(nil)
		return startSessionResult{
			SessionID: existing.serial,
			Selection: existing.selection,
			Slot:      slot,
			Kind:      string(sessionKindAI),
		}, existing, nil
	}
	a.mu.Unlock()

	if !confirmed {
		if occupants := a.aiSessionOccupants(selection); len(occupants) > 0 {
			return startSessionResult{
				Selection: selection,
				Slot:      slot,
				Kind:      string(sessionKindAI),
				Occupancy: occupants,
			}, nil, nil
		}
	}

	a.ensureEnvRuntimeOnce(selection)
	params := startTerminalSessionParams{
		Dir:        resolveTerminalStartDir(result.RepoPath),
		Executable: a.deps.resolveCLIPath(),
		// The pod session launches the AI tool once on create; reopening reattaches.
		Args: withAppSession(buildOpenArgs(result.Tenant, result.Environment), "ai", true, false),
		Env:  []string{appSessionEnvVar + "=1"},
		Cols: cols,
		Rows: rows,
	}
	session, err := a.deps.startTerminal(params)
	if err != nil {
		return startSessionResult{}, nil, err
	}

	a.mu.Lock()
	a.nextSerial++
	serial := a.nextSerial
	managed := &managedTerminal{
		session:    session,
		selection:  selection,
		key:        key,
		serial:     serial,
		slot:       slot,
		kind:       sessionKindAI,
		appSession: "ai",
		respawn: func() (terminalSession, error) {
			return a.deps.startTerminal(reconnectSessionParams(params))
		},
		startedAt: time.Now(),
		lastCols:  cols,
		lastRows:  rows,
	}
	a.sessions[key] = managed
	a.mu.Unlock()

	a.rememberKubeContextForActivity(selection.KubernetesContext)
	a.lockNewlyJoinedSessionIfDeployInFlight(selection, serial)
	a.spawnStreamSession(managed)

	a.logSpawnedCommandToLocal(selection, "ai", formatLocalCommandLog(formatLaunchCommand(params), "AI tab"))
	_ = ctx
	return startSessionResult{
		SessionID: serial,
		Selection: selection,
		Slot:      slot,
		Kind:      string(sessionKindAI),
	}, managed, nil
}

// aiSessionOccupants reads held leases, separately if the status read fails. A
// failed lease read returns none: this is a notice, not an access check.
func (a *App) aiSessionOccupants(selection uiSelection) []uiEnvironmentLease {
	status, err := a.LoadIdleStatus(selection)
	if err == nil {
		return status.Leases
	}
	now := time.Now()
	leases, err := activity.LoadEnvironmentActivityLeases(selection.Tenant, selection.Environment, now)
	if err != nil {
		return nil
	}
	occupants := environmentLeasesToUI(leases, now)
	a.verifyLeaseJobIDs(selection.Tenant, selection.Environment, occupants)
	return occupants
}
