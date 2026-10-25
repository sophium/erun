package terminal

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/envstore"
	"github.com/sophium/erun/erun-common/command/lifecycle/envdelete"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func (s *Service) DeleteEnvironment(selection uimodel.Selection, confirmation string) (DeleteEnvironmentResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("delete environment", selection.Tenant, selection.Environment); err != nil {
		return DeleteEnvironmentResult{}, err
	}
	expected := envdelete.DeleteEnvironmentConfirmation(selection.Tenant, selection.Environment)
	if strings.TrimSpace(confirmation) != expected {
		return DeleteEnvironmentResult{}, fmt.Errorf("delete confirmation did not match %q", expected)
	}

	store, ok := s.d.Store.(envstore.DeleteStore)
	if !ok {
		return DeleteEnvironmentResult{}, fmt.Errorf("environment deletion is not supported by the configured store")
	}
	envConfig, _, err := store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return DeleteEnvironmentResult{}, err
	}
	linkedContext, hasLinkedContext, err := s.d.EnsureLinkedCloudContextRunning(envConfig)
	if err != nil {
		return DeleteEnvironmentResult{}, err
	}

	// A later re-create of this env must fire environment-initialized again.
	s.d.ClearInitEmitted(selection.Tenant, selection.Environment)
	result, err := envdelete.RunDeleteEnvironment(core.Context{}, model.DeleteEnvironmentParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	}, store, s.d.DeleteNamespace)
	stopError := ""
	if hasLinkedContext {
		if _, stopErr := s.d.StopCloudContext(linkedContext.Name); stopErr != nil {
			stopError = stopErr.Error()
		}
	}
	if err != nil {
		return DeleteEnvironmentResult{}, err
	}
	s.d.StopCloudRefresher(selection)
	s.closeSessionsForSelection(selection)
	return DeleteEnvironmentResult{
		Tenant:                result.Tenant,
		Environment:           result.Environment,
		Namespace:             result.Namespace,
		KubernetesContext:     result.KubernetesContext,
		NamespaceDeleteError:  result.NamespaceDeleteError,
		CloudContextStopError: stopError,
	}, nil
}

func (s *Service) CloseSession(sessionID int) error {
	s.mu.Lock()
	managed := s.sessionBySerialLocked(sessionID)
	var endRemote bool
	var endSelection uimodel.Selection
	var endID string
	if managed != nil && managed.Kind == KindOpen && managed.Slot > 0 && !managed.takenOver {
		// Closing a custom tab removes it, so end the pod session too or detection
		// rebuilds it; default and taken-over tabs only detach.
		endRemote = true
		endSelection = managed.Selection
		endID = fmt.Sprintf("open-%d", managed.Slot)
	}
	s.mu.Unlock()
	if managed == nil || managed.PTY == nil {
		return nil
	}
	if endRemote {
		go s.endRemoteAppSession(endSelection, endID)
	}
	// CloseManaged marks the session closed before the PTY dies, so the stale-shell
	// detector does not report it as an unexpected exit.
	return s.CloseManaged(managed)
}

// CloseEnvironmentSessions closes every PTY of the env and returns their serials.
// Sessions close after s.mu is released: Close re-acquires it.
func (s *Service) CloseEnvironmentSessions(selection uimodel.Selection) ([]int, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("close environment sessions", selection.Tenant, selection.Environment); err != nil {
		return nil, err
	}
	targets := s.collectAndMarkClosedForSelection(selection)
	// Stop the env's workspace-sync worker; reopening restarts it.
	s.d.StopWorkspaceSync(selection)
	// Reopening must start from a fresh pod reading.
	s.forgetSessionHeartbeats(selection)
	return s.closeManagedTerminals(targets)
}

// collectAndMarkClosedForSelection marks sessions closed under s.mu so they
// finalize instead of reconnecting, which clears the sidebar spinner.
func (s *Service) collectAndMarkClosedForSelection(selection uimodel.Selection) []*Session {
	var targets []*Session
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, managed := range s.sessions {
		if managed == nil || managed.Closed {
			continue
		}
		if managed.Selection.Tenant != selection.Tenant {
			continue
		}
		if managed.Selection.Environment != selection.Environment {
			continue
		}
		markClosedByTeardownLocked(managed)
		s.releaseIdleBlockLocked(managed)
		targets = append(targets, managed)
	}
	return targets
}

func (s *Service) closeManagedTerminals(targets []*Session) ([]int, error) {
	closed := make([]int, 0, len(targets))
	var firstErr error
	for _, managed := range targets {
		if managed.PTY == nil {
			continue
		}
		err := managed.PTY.Close()
		s.withdrawExitOwnership(managed, err)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		closed = append(closed, managed.Serial)
	}
	return closed, firstErr
}

// finalizeDeliberateClose reports the exit of a session whose reader found the
// close mark rather than a read error. Only the registration's own live entry is
// finalized, so the frontend never gets a second terminal-exit.
func (s *Service) finalizeDeliberateClose(managed *Session) {
	s.mu.Lock()
	var session PTY
	registered := managed != nil && managed.Closed && s.sessions[managed.Key] == managed
	if registered {
		session = managed.PTY
	}
	s.mu.Unlock()
	if session == nil {
		return
	}
	s.finalizeSessionExit(managed, terminalSessionExitReason(session, nil))
}

// closeManagedLocked marks managed closed and returns its session. Caller holds s.mu.
func (s *Service) closeManagedLocked(managed *Session) PTY {
	if managed == nil || managed.PTY == nil {
		return nil
	}
	markClosedByTeardownLocked(managed)
	return managed.PTY
}

// markClosedByTeardownLocked is the only way a teardown may mark a session. Callers
// hold s.mu and call it before touching the PTY.
func markClosedByTeardownLocked(managed *Session) {
	managed.Closed = true
	managed.teardownOwnsExit = true
}

// withdrawExitOwnershipLocked retracts the claim when Close did not kill the
// process. Callers hold s.mu and call it before the reader can wake.
func withdrawExitOwnershipLocked(managed *Session, closeErr error) {
	if managed != nil && errors.Is(closeErr, errKillDidNotLand) {
		managed.teardownOwnsExit = false
	}
}

func (s *Service) withdrawExitOwnership(managed *Session, closeErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	withdrawExitOwnershipLocked(managed, closeErr)
}

// CloseManaged runs session.Close outside s.mu.
func (s *Service) CloseManaged(managed *Session) error {
	s.mu.Lock()
	session := s.closeManagedLocked(managed)
	s.mu.Unlock()
	if session == nil {
		return nil
	}
	err := session.Close()
	s.withdrawExitOwnership(managed, err)
	return err
}

// CloseAll closes every session. Wait then waits for their readers.
func (s *Service) CloseAll() {
	s.mu.Lock()
	s.closeAllSessionsLocked()
	s.mu.Unlock()
}

// Wait blocks until every stream reader has exited. Readers take the lock on
// exit, so it must be called without it.
func (s *Service) Wait() {
	s.sessionWG.Wait()
}

func (s *Service) closeAllSessionsLocked() {
	for _, managed := range s.sessions {
		if session := s.closeManagedLocked(managed); session != nil {
			withdrawExitOwnershipLocked(managed, session.Close())
		}
	}
	s.sessions = make(map[string]*Session)
}

func (s *Service) closeSessionsForSelection(selection uimodel.Selection) {
	selection = uimodel.NormalizeSelection(selection)
	prefixes := []string{
		uimodel.SelectionKey(selection) + "\x00",
		"init\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00",
		"deploy\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00",
		"local\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00",
		"ai\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00",
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for key, managed := range s.sessions {
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
		if session := s.closeManagedLocked(managed); session != nil {
			withdrawExitOwnershipLocked(managed, session.Close())
		}
		delete(s.sessions, key)
	}
}
