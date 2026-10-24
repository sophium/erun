package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
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

	store, ok := a.deps.store.(lifecycle.DeleteStore)
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
	result, err := lifecycle.RunDeleteEnvironment(core.Context{}, lifecycle.DeleteEnvironmentParams{
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
		// An explicit close of a custom terminal tab is the user removing
		// that terminal, not just detaching: end the pod session too, or
		// detection rebuilds the tab on the next env open. Default tabs stay
		// detach-only (their long-running sessions are the feature), and a
		// taken-over tab must never kill the session another window now owns.
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
	// Route the teardown through closeManaged so this session is marked closed
	// under a.mu before its PTY is touched, the same way every other deliberate
	// teardown in this file does. The stale-shell detector reads that mark: the
	// process is already reaped the moment the close lands, so without it a
	// session the operator just closed is offered to the detector and reported
	// as a shell that exited unexpectedly. The mark claims the exit, it does not
	// swallow it — the reader still emits the terminal-exit and deregisters the
	// session, from whichever arm it reaches first (see finalizeDeliberateClose).
	return a.closeManaged(managed)
}

// CloseEnvironmentSessions tears down every managed PTY bound to the
// (tenant, environment) pair and returns the closed serial IDs so the frontend
// can drop them in one round-trip. Backs the sidebar's "close env" affordance;
// it is desktop-only and does NOT touch the cloud context's AWS state (see
// StopCloudContext).
//
// Targets are collected under a.mu and closed after releasing it: session.Close
// can call back into bookkeeping that re-acquires a.mu, so closing under the
// lock would deadlock.
func (a *App) CloseEnvironmentSessions(selection uiSelection) ([]int, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("close environment sessions", selection.Tenant, selection.Environment); err != nil {
		return nil, err
	}
	targets := a.collectAndMarkClosedForSelection(selection)
	// Close is a real teardown: stop the env's workspace-sync worker so a
	// closed env stops mirroring its worktree. Startup starts one sync worker
	// per configured env, so without this a closed env keeps an rsync-over-ssh
	// poller running; reopening the env restarts it.
	a.stopWorkspaceSyncForSelection(selection)
	// The env's pod observation describes sessions that no longer have a tab.
	// Dropping it means reopening the env starts from a fresh reading rather
	// than one taken before the close (or before a deploy replaced the pod).
	a.forgetSessionHeartbeats(selection)
	return a.closeManagedTerminals(targets)
}

// collectAndMarkClosedForSelection gathers the env's live sessions and marks
// each closed under a.mu BEFORE the caller shuts their PTYs down. Marking
// closed here is what makes "close env" a genuine teardown rather than a
// reconnect: tryReconnect and the spawn-reuse branch both refuse a closed
// session, so streamSession reaches finalizeSessionExit — which emits the
// terminal-exit and the AI-activity busy=false that clears the sidebar
// spinner — instead of reconnecting the still-live pod session and leaving the
// row spinning forever. Mirrors EndAISessions' mark-closed-under-lock.
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

// finalizeDeliberateClose finalizes the exit of a session whose reader found
// the deliberate-close mark instead of a read error: the close landed between
// iterations, or before the reader's first Read. streamSession calls it from the
// arm that used to return silently, so marking a session closed stays what keeps
// it out of the stale-shell detector without also silencing its exit.
//
// Only the registration's own live entry is finalized. finalizeSessionExit
// deregisters the session, so an entry no longer under its own key has already
// been finalized by its reader — or was replaced at that key on a respawn — and
// reporting it again would emit a second terminal-exit for a session the
// frontend has already dropped. A close that was never registered has no reader
// to report its exit.
//
// The reason comes from the session's own Wait, the same source the read-error
// arm reads, so a deliberately closed session reports the reason it reported
// before the mark existed.
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

// closeManagedLocked marks managed closed and hands back its underlying
// session for the caller to tear down. The `closed` field (and `session`,
// which callers read alongside it) is read everywhere else in this file under
// a.mu — currentSessionFor, tryReconnect, finalizeSessionExit, and more all
// take the lock before touching either — so mutating them takes the same lock
// rather than growing a second one; every caller here already holds a.mu.
func (a *App) closeManagedLocked(managed *managedTerminal) terminalSession {
	if managed == nil || managed.session == nil {
		return nil
	}
	markClosedByTeardownLocked(managed)
	return managed.session
}

// markClosedByTeardownLocked records that the desktop is ending this session on
// purpose, and is the only way a teardown path may mark one: the two facts are
// one decision. Callers hold a.mu and must call it before touching the PTY —
// see managedTerminal.teardownOwnsExit for why the claim cannot wait for the
// kill's outcome.
func markClosedByTeardownLocked(managed *managedTerminal) {
	managed.closed = true
	managed.teardownOwnsExit = true
}

// withdrawExitOwnershipLocked takes that claim back when a teardown's own Close
// reports it did not kill the process. Withdrawing is not the same as not
// having claimed: the claim is what carries a session killed before the frontend
// ever saw it, and it can only be made before the outcome exists. The session
// is still running when this lands, so its exit is still ahead of it.
//
// Callers hold a.mu, and must call this before anything can wake the session's
// reader: the claim has to be gone by the time the survivor's exit is finalized.
func withdrawExitOwnershipLocked(managed *managedTerminal, closeErr error) {
	if managed != nil && errors.Is(closeErr, errKillDidNotLand) {
		managed.teardownOwnsExit = false
	}
}

// withdrawExitOwnership is withdrawExitOwnershipLocked for callers that do not
// already hold a.mu.
func (a *App) withdrawExitOwnership(managed *managedTerminal, closeErr error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	withdrawExitOwnershipLocked(managed, closeErr)
}

// closeManaged is closeManagedLocked for callers that do not already hold
// a.mu. The session's real teardown (session.Close(), a file/process
// operation) happens outside the lock, matching every other place in this
// file that only holds a.mu around the field mutation and not around the
// blocking I/O that follows it.
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
