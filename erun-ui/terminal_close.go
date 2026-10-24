package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/command/lifecycle"
)

func (a *App) DeleteEnvironment(selection uiSelection, confirmation string) (deleteEnvironmentResult, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("delete environment", selection.Tenant, selection.Environment); err != nil {
		return deleteEnvironmentResult{}, err
	}
	expected := lifecycle.DeleteEnvironmentConfirmation(selection.Tenant, selection.Environment)
	if strings.TrimSpace(confirmation) != expected {
		return deleteEnvironmentResult{}, fmt.Errorf("delete confirmation did not match %q", expected)
	}

	store, ok := a.deps.store.(seam.DeleteStore)
	if !ok {
		return deleteEnvironmentResult{}, fmt.Errorf("environment deletion is not supported by the configured store")
	}
	envConfig, _, err := store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return deleteEnvironmentResult{}, err
	}
	linkedContext, hasLinkedContext, err := a.ensureLinkedCloudContextRunning(envConfig)
	if err != nil {
		return deleteEnvironmentResult{}, err
	}

	// A later re-create of this env must fire environment-initialized again.
	a.clearInitEmitted(selection.Tenant, selection.Environment)
	result, err := lifecycle.RunDeleteEnvironment(core.Context{}, model.DeleteEnvironmentParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	}, store, a.deps.deleteNamespace)
	stopError := ""
	if hasLinkedContext {
		if _, stopErr := a.stopCloudContext(linkedContext.Name); stopErr != nil {
			stopError = stopErr.Error()
		}
	}
	if err != nil {
		return deleteEnvironmentResult{}, err
	}
	a.stopCloudCredentialsRefresherForSelection(selection)
	a.closeSessionsForSelection(selection)
	return deleteEnvironmentResult{
		Tenant:                result.Tenant,
		Environment:           result.Environment,
		Namespace:             result.Namespace,
		KubernetesContext:     result.KubernetesContext,
		NamespaceDeleteError:  result.NamespaceDeleteError,
		CloudContextStopError: stopError,
	}, nil
}

func (a *App) CloseSession(sessionID int) error {
	a.mu.Lock()
	managed := a.sessionBySerialLocked(sessionID)
	var endRemote bool
	var endSelection uiSelection
	var endID string
	if managed != nil && managed.kind == sessionKindOpen && managed.slot > 0 && !managed.takenOver {
		// Closing a custom tab removes it, so end the pod session too or detection
		// rebuilds it; default and taken-over tabs only detach.
		endRemote = true
		endSelection = managed.selection
		endID = fmt.Sprintf("open-%d", managed.slot)
	}
	a.mu.Unlock()
	if managed == nil || managed.session == nil {
		return nil
	}
	if endRemote {
		go a.endRemoteAppSession(endSelection, endID)
	}
	// closeManaged marks the session closed before the PTY dies, so the stale-shell
	// detector does not report it as an unexpected exit.
	return a.closeManaged(managed)
}

// CloseEnvironmentSessions closes every PTY of the env and returns their serials.
// Sessions close after a.mu is released: Close re-acquires it.
func (a *App) CloseEnvironmentSessions(selection uiSelection) ([]int, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("close environment sessions", selection.Tenant, selection.Environment); err != nil {
		return nil, err
	}
	targets := a.collectAndMarkClosedForSelection(selection)
	// Stop the env's workspace-sync worker; reopening restarts it.
	a.stopWorkspaceSyncForSelection(selection)
	// Reopening must start from a fresh pod reading.
	a.forgetSessionHeartbeats(selection)
	return a.closeManagedTerminals(targets)
}

// collectAndMarkClosedForSelection marks sessions closed under a.mu so they
// finalize instead of reconnecting, which clears the sidebar spinner.
func (a *App) collectAndMarkClosedForSelection(selection uiSelection) []*managedTerminal {
	var targets []*managedTerminal
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, managed := range a.sessions {
		if managed == nil || managed.closed {
			continue
		}
		if managed.selection.Tenant != selection.Tenant {
			continue
		}
		if managed.selection.Environment != selection.Environment {
			continue
		}
		markClosedByTeardownLocked(managed)
		a.releaseIdleBlockLocked(managed)
		targets = append(targets, managed)
	}
	return targets
}

func (a *App) closeManagedTerminals(targets []*managedTerminal) ([]int, error) {
	closed := make([]int, 0, len(targets))
	var firstErr error
	for _, managed := range targets {
		if managed.session == nil {
			continue
		}
		err := managed.session.Close()
		a.withdrawExitOwnership(managed, err)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		closed = append(closed, managed.serial)
	}
	return closed, firstErr
}

// finalizeDeliberateClose reports the exit of a session whose reader found the
// close mark rather than a read error. Only the registration's own live entry is
// finalized, so the frontend never gets a second terminal-exit.
func (a *App) finalizeDeliberateClose(managed *managedTerminal) {
	a.mu.Lock()
	var session terminalSession
	registered := managed != nil && managed.closed && a.sessions[managed.key] == managed
	if registered {
		session = managed.session
	}
	a.mu.Unlock()
	if session == nil {
		return
	}
	a.finalizeSessionExit(managed, terminalSessionExitReason(session, nil))
}

// closeManagedLocked marks managed closed and returns its session. Caller holds a.mu.
func (a *App) closeManagedLocked(managed *managedTerminal) terminalSession {
	if managed == nil || managed.session == nil {
		return nil
	}
	markClosedByTeardownLocked(managed)
	return managed.session
}

// markClosedByTeardownLocked is the only way a teardown may mark a session. Callers
// hold a.mu and call it before touching the PTY.
func markClosedByTeardownLocked(managed *managedTerminal) {
	managed.closed = true
	managed.teardownOwnsExit = true
}

// withdrawExitOwnershipLocked retracts the claim when Close did not kill the
// process. Callers hold a.mu and call it before the reader can wake.
func withdrawExitOwnershipLocked(managed *managedTerminal, closeErr error) {
	if managed != nil && errors.Is(closeErr, errKillDidNotLand) {
		managed.teardownOwnsExit = false
	}
}

func (a *App) withdrawExitOwnership(managed *managedTerminal, closeErr error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	withdrawExitOwnershipLocked(managed, closeErr)
}

// closeManaged runs session.Close outside a.mu.
func (a *App) closeManaged(managed *managedTerminal) error {
	a.mu.Lock()
	session := a.closeManagedLocked(managed)
	a.mu.Unlock()
	if session == nil {
		return nil
	}
	err := session.Close()
	a.withdrawExitOwnership(managed, err)
	return err
}

func (a *App) closeAllSessionsLocked() {
	for _, managed := range a.sessions {
		if session := a.closeManagedLocked(managed); session != nil {
			withdrawExitOwnershipLocked(managed, session.Close())
		}
	}
	a.sessions = make(map[string]*managedTerminal)
}

func (a *App) closeSessionsForSelection(selection uiSelection) {
	selection = normalizeSelection(selection)
	prefixes := []string{
		selectionKey(selection) + "\x00",
		"init\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00",
		"deploy\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00",
		"local\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00",
		"ai\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00",
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for key, managed := range a.sessions {
		if managed == nil {
			continue
		}
		matches := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(key, prefix) {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		if session := a.closeManagedLocked(managed); session != nil {
			withdrawExitOwnershipLocked(managed, session.Close())
		}
		delete(a.sessions, key)
	}
}
