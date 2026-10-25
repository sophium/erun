package main

import (
	"strings"
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

const (
	activityQueueStateEvent = "activity:state"

	activityQueueLockEvent = "activity:lock"

	activityQueuePollInterval = 2 * time.Second
)

// activityLockEvent is keyed by SessionID on the frontend.
type activityLockEvent struct {
	SessionID    int    `json:"sessionId"`
	Tenant       string `json:"tenant"`
	Environment  string `json:"environment"`
	Locked       bool   `json:"locked"`
	DeployID     string `json:"deployId,omitempty"`
	Reason       string `json:"reason,omitempty"`
	DeployTarget string `json:"deployTarget,omitempty"`
}

// ListDeploys returns current and recent entries, newest first.
func (a *App) ListDeploys() []activityQueueEntry {
	if a.activityQueue == nil {
		return nil
	}
	return a.activityQueue.list()
}

// GetDeploy returns an entry by ID, or the zero value.
func (a *App) GetDeploy(id string) activityQueueEntry {
	if a.activityQueue == nil {
		return activityQueueEntry{}
	}
	for _, entry := range a.activityQueue.list() {
		if entry.ID == id {
			return entry
		}
	}
	return activityQueueEntry{}
}

// DismissDeploy removes a finished deploy; false for an active or unknown ID.
func (a *App) DismissDeploy(id string) bool {
	if a.activityQueue == nil {
		return false
	}
	return a.activityQueue.dismiss(id)
}

// ForceDismissActivity removes any entry, for stuck entries the user confirmed are dead.
func (a *App) ForceDismissActivity(id string) bool {
	if a.activityQueue == nil {
		return false
	}
	entry, _, ok := a.activityQueue.forceDismiss(id)
	if !ok {
		return false
	}
	a.unlockTerminalsForActivity(entry)
	a.emitActivityState(entry)
	return true
}

// FindActiveDeployForSelection returns the active entry for the selection, if any.
func (a *App) FindActiveDeployForSelection(selection uimodel.Selection) activityQueueEntry {
	if a.activityQueue == nil {
		return activityQueueEntry{}
	}
	selection = uimodel.NormalizeSelection(selection)
	if entry, ok := a.activityQueue.findActive(selection.Tenant, selection.Environment); ok {
		return entry
	}
	return activityQueueEntry{}
}

// finishActivityTracking finalizes the selection's "deploy" entry, keyed by command
// so a concurrent build's entry is never finalized instead.
func (a *App) finishActivityTracking(selection uimodel.Selection, status activityQueueStatus, errMsg string) {
	if a.activityQueue == nil {
		return
	}
	entry, ok := a.activityQueue.findActiveByCommand("deploy", strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment))
	if !ok {
		return
	}
	if final, ok := a.activityQueue.finish(entry.ID, status, errMsg); ok {
		a.unlockTerminalsForActivity(final)
	}
}

func (a *App) emitActivityState(entry activityQueueEntry) {
	a.emitEvent(activityQueueStateEvent, entry)
}

// lockTerminalsForActivity locks existing sessions; later joiners go through lockTerminalForActivity.
func (a *App) lockTerminalsForActivity(entry activityQueueEntry) {
	if entry.ID == "" {
		return
	}
	target := activityTargetForRuntime(entry)
	a.mu.Lock()
	var events []activityLockEvent
	for _, managed := range a.sessions {
		if managed == nil || managed.closed {
			continue
		}
		if !sessionMatchesActivity(managed, entry) {
			continue
		}
		if managed.lockedByActivity == entry.ID {
			continue
		}
		managed.lockedByActivity = entry.ID
		events = append(events, activityLockEvent{
			SessionID:    managed.serial,
			Tenant:       entry.Tenant,
			Environment:  entry.Environment,
			Locked:       true,
			DeployID:     entry.ID,
			Reason:       "Waiting for deploy to complete",
			DeployTarget: target,
		})
	}
	a.mu.Unlock()
	for _, ev := range events {
		a.emitEvent(activityQueueLockEvent, ev)
	}
	if len(events) > 0 {
		// The deploy makes any env-scoped warning stale.
		a.emitClearEnvNotification(entry.Tenant, entry.Environment, "")
	}
}

func (a *App) lockTerminalForActivity(sessionID int, entry activityQueueEntry) {
	if entry.ID == "" || sessionID <= 0 {
		return
	}
	target := activityTargetForRuntime(entry)
	a.mu.Lock()
	var event *activityLockEvent
	for _, managed := range a.sessions {
		if managed == nil || managed.closed || managed.serial != sessionID {
			continue
		}
		if managed.lockedByActivity != entry.ID {
			managed.lockedByActivity = entry.ID
			event = &activityLockEvent{
				SessionID:    managed.serial,
				Tenant:       entry.Tenant,
				Environment:  entry.Environment,
				Locked:       true,
				DeployID:     entry.ID,
				Reason:       "Waiting for deploy to complete",
				DeployTarget: target,
			}
		}
		break
	}
	a.mu.Unlock()
	if event != nil {
		a.emitEvent(activityQueueLockEvent, *event)
		a.emitClearEnvNotification(entry.Tenant, entry.Environment, "")
	}
}

// lockNewlyJoinedSessionIfDeployInFlight locks a session as it joins, since
// lockTerminalsForActivity only covered sessions present at deploy start.
func (a *App) lockNewlyJoinedSessionIfDeployInFlight(selection uimodel.Selection, sessionID int) {
	if a.activityQueue == nil {
		return
	}
	if entry, ok := a.activityQueue.findActiveByCommand("deploy", selection.Tenant, selection.Environment); ok {
		a.lockTerminalForActivity(sessionID, entry)
	}
}

func (a *App) unlockTerminalsForActivity(entry activityQueueEntry) {
	if entry.ID == "" {
		return
	}
	a.mu.Lock()
	var events []activityLockEvent
	for _, managed := range a.sessions {
		if managed == nil {
			continue
		}
		if managed.lockedByActivity != entry.ID {
			continue
		}
		managed.lockedByActivity = ""
		events = append(events, activityLockEvent{
			SessionID:   managed.serial,
			Tenant:      entry.Tenant,
			Environment: entry.Environment,
			Locked:      false,
			DeployID:    entry.ID,
		})
	}
	a.mu.Unlock()
	for _, ev := range events {
		a.emitEvent(activityQueueLockEvent, ev)
	}
}

// sessionMatchesActivity: only a deploy locks sibling terminals, since it
// replaces the runtime hosting them.
func sessionMatchesActivity(managed *managedTerminal, entry activityQueueEntry) bool {
	if managed == nil {
		return false
	}
	if entry.Command != "deploy" {
		return false
	}
	if managed.selection.Tenant != entry.Tenant || managed.selection.Environment != entry.Environment {
		return false
	}
	switch managed.kind {
	case sessionKindOpen, sessionKindAI:
		return true
	case sessionKindLocal, sessionKindCommand, sessionKindContributeERun, sessionKindContributeAI, sessionKindOrchestrator:
		fallthrough
	default:
		return false
	}
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
