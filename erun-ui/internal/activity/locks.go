package activity

import (
	"strings"
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

const (
	stateEvent = "activity:state"

	lockEvent = "activity:lock"

	containerPollInterval = 2 * time.Second
)

// lockPayload is keyed by SessionID on the frontend.
type lockPayload struct {
	SessionID    int    `json:"sessionId"`
	Tenant       string `json:"tenant"`
	Environment  string `json:"environment"`
	Locked       bool   `json:"locked"`
	DeployID     string `json:"deployId,omitempty"`
	Reason       string `json:"reason,omitempty"`
	DeployTarget string `json:"deployTarget,omitempty"`
}

// ListDeploys returns current and recent entries, newest first.
func (s *Service) ListDeploys() []Entry {
	return s.queue.list()
}

// GetDeploy returns an entry by ID, or the zero value.
func (s *Service) GetDeploy(id string) Entry {
	for _, entry := range s.queue.list() {
		if entry.ID == id {
			return entry
		}
	}
	return Entry{}
}

// DismissDeploy removes a finished deploy; false for an active or unknown ID.
func (s *Service) DismissDeploy(id string) bool {
	return s.queue.dismiss(id)
}

// ForceDismissActivity removes any entry, for stuck entries the user confirmed are dead.
func (s *Service) ForceDismissActivity(id string) bool {
	entry, _, ok := s.queue.forceDismiss(id)
	if !ok {
		return false
	}
	s.unlockTerminalsForActivity(entry)
	s.emitState(entry)
	return true
}

// FindActiveDeployForSelection returns the active entry for the selection, if any.
func (s *Service) FindActiveDeployForSelection(selection uimodel.Selection) Entry {
	selection = uimodel.NormalizeSelection(selection)
	if entry, ok := s.queue.findActive(selection.Tenant, selection.Environment); ok {
		return entry
	}
	return Entry{}
}

// finishActivityTracking finalizes the selection's "deploy" entry, keyed by command
// so a concurrent build's entry is never finalized instead.
func (s *Service) finishActivityTracking(selection uimodel.Selection, status Status, errMsg string) {
	entry, ok := s.queue.findActiveByCommand("deploy", strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment))
	if !ok {
		return
	}
	if final, ok := s.queue.finish(entry.ID, status, errMsg); ok {
		s.unlockTerminalsForActivity(final)
	}
}

func (s *Service) emitState(entry Entry) {
	s.emitEvent(stateEvent, entry)
}

// lockTerminalsForActivity locks existing sessions; later joiners go through lockTerminalForActivity.
func (s *Service) lockTerminalsForActivity(entry Entry) {
	if entry.ID == "" {
		return
	}
	target := activityTargetForRuntime(entry)
	locked := s.d.Sessions.LockMatching(entry.ID, func(session Session) bool {
		return sessionMatchesActivity(session, entry)
	})
	for _, session := range locked {
		s.emitEvent(lockEvent, lockPayload{
			SessionID:    session.Serial,
			Tenant:       entry.Tenant,
			Environment:  entry.Environment,
			Locked:       true,
			DeployID:     entry.ID,
			Reason:       "Waiting for deploy to complete",
			DeployTarget: target,
		})
	}
	if len(locked) > 0 {
		// The deploy makes any env-scoped warning stale.
		s.emitClearEnvNotification(entry.Tenant, entry.Environment, "")
	}
}

func (s *Service) lockTerminalForActivity(sessionID int, entry Entry) {
	if entry.ID == "" || sessionID <= 0 {
		return
	}
	target := activityTargetForRuntime(entry)
	session, locked := s.d.Sessions.LockSerial(sessionID, entry.ID)
	if !locked {
		return
	}
	s.emitEvent(lockEvent, lockPayload{
		SessionID:    session.Serial,
		Tenant:       entry.Tenant,
		Environment:  entry.Environment,
		Locked:       true,
		DeployID:     entry.ID,
		Reason:       "Waiting for deploy to complete",
		DeployTarget: target,
	})
	s.emitClearEnvNotification(entry.Tenant, entry.Environment, "")
}

// LockIfDeployInFlight locks a session as it joins, since
// lockTerminalsForActivity only covered sessions present at deploy start.
func (s *Service) LockIfDeployInFlight(selection uimodel.Selection, sessionID int) {
	if entry, ok := s.queue.findActiveByCommand("deploy", selection.Tenant, selection.Environment); ok {
		s.lockTerminalForActivity(sessionID, entry)
	}
}

// LatestDeployFailed reports whether the env's latest deploy failed.
func (s *Service) LatestDeployFailed(tenant, environment string) bool {
	return s.queue.latestDeployFailed(tenant, environment)
}

func (s *Service) unlockTerminalsForActivity(entry Entry) {
	if entry.ID == "" {
		return
	}
	for _, session := range s.d.Sessions.Unlock(entry.ID) {
		s.emitEvent(lockEvent, lockPayload{
			SessionID:   session.Serial,
			Tenant:      entry.Tenant,
			Environment: entry.Environment,
			Locked:      false,
			DeployID:    entry.ID,
		})
	}
}

// sessionMatchesActivity: only a deploy locks sibling terminals, since it
// replaces the runtime hosting them.
func sessionMatchesActivity(session Session, entry Entry) bool {
	if entry.Command != "deploy" {
		return false
	}
	if session.Selection.Tenant != entry.Tenant || session.Selection.Environment != entry.Environment {
		return false
	}
	return session.Kind == KindOpen || session.Kind == KindAI
}

// releaseNameForTenant must match the runtime chart's release name.
func releaseNameForTenant(tenant string) string {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return ""
	}
	return tenant + "-devops"
}

// namespaceForTenantEnv must match the deploy resolver's namespace pattern.
func namespaceForTenantEnv(tenant, environment string) string {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	switch {
	case tenant == "" && environment == "":
		return ""
	case tenant == "":
		return environment
	case environment == "":
		return tenant
	default:
		return tenant + "-" + environment
	}
}
