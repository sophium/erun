package main

import (
	"strings"
	"time"
)

const (
	// activityQueueStateEvent lets the frontend refresh the queue drawer and
	// per-card container statuses reactively instead of polling.
	activityQueueStateEvent = "activity:state"

	// activityQueueLockEvent drives the frontend's per-terminal lock overlay.
	activityQueueLockEvent = "activity:lock"

	// activityQueuePollInterval trades responsiveness against poll load: short
	// enough that the user sees pods transition, long enough to stay negligible
	// against modest clusters.
	activityQueuePollInterval = 2 * time.Second
)

// activityLockEvent carries a terminal lock transition; the frontend keys its
// overlays by SessionID.
type activityLockEvent struct {
	SessionID    int    `json:"sessionId"`
	Tenant       string `json:"tenant"`
	Environment  string `json:"environment"`
	Locked       bool   `json:"locked"`
	DeployID     string `json:"deployId,omitempty"`
	Reason       string `json:"reason,omitempty"`
	DeployTarget string `json:"deployTarget,omitempty"`
}

// ListDeploys returns the current and recent deploy entries (newest first).
// Wails-exported.
func (a *App) ListDeploys() []activityQueueEntry {
	if a.activityQueue == nil {
		return nil
	}
	return a.activityQueue.list()
}

// GetDeploy returns a single deploy entry by ID, or zero value if not found.
// Wails-exported.
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

// DismissDeploy removes a finished deploy from history. Returns false when
// the ID names an active deploy (which the user must wait for) or no longer
// exists. Wails-exported.
func (a *App) DismissDeploy(id string) bool {
	if a.activityQueue == nil {
		return false
	}
	return a.activityQueue.dismiss(id)
}

// ForceDismissActivity removes any entry — active or finished — from the
// queue. Used for stuck active entries the user has confirmed are no
// longer running (e.g. a stale shell whose PID exited unnoticed, or a
// deploy entry whose helm release was deleted out from under the
// poller). Wails-exported.
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

// FindActiveDeployForSelection returns the active entry for (tenant, env)
// when one exists; the frontend uses this for deploy-button gating without
// having to walk the full ListDeploys snapshot. Wails-exported.
func (a *App) FindActiveDeployForSelection(selection uiSelection) activityQueueEntry {
	if a.activityQueue == nil {
		return activityQueueEntry{}
	}
	selection = normalizeSelection(selection)
	if entry, ok := a.activityQueue.findActive(selection.Tenant, selection.Environment); ok {
		return entry
	}
	return activityQueueEntry{}
}

// finishActivityTracking finalizes the active "deploy" entry for the
// selection. Both callers observe a deploy-failure trace line that carries no
// tenant/env (a bare "==> Deploy failed after <elapsed>", or a
// "==> Deployed"/"==> Deploy failed" line finishDeployByTenantEnv could not
// parse tenant/env out of), so the command is always "deploy" here — keyed by
// command, like finishCommandBySelection, rather than plain findActive, whose
// Go map iteration order picks an arbitrary entry when a build and a deploy
// are both active for the same tenant/env — the wrong card finalizing.
func (a *App) finishActivityTracking(selection uiSelection, status activityQueueStatus, errMsg string) {
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

// lockTerminalsForActivity locks the terminals that exist now for the deploy's
// selection; sessions that join later are locked individually by
// lockTerminalForActivity.
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
		// A deploy just started, so retire any env-scoped warning that told the
		// operator to act — that guidance is now stale.
		a.emitClearEnvNotification(entry.Tenant, entry.Environment, "")
	}
}

// lockTerminalForActivity locks a session that joined an in-flight deploy after
// it had already started.
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
		// A late-joining session locked onto an in-flight deploy — the runtime is
		// being (re)deployed, so clear any env-scoped warning for it.
		a.emitClearEnvNotification(entry.Tenant, entry.Environment, "")
	}
}

// lockNewlyJoinedSessionIfDeployInFlight locks a session the instant it joins,
// rather than leaving it unlocked until the deploy's poller or completion
// handler next touches the queue: a session created after a deploy has
// already started otherwise shows no lock overlay at all, since
// lockTerminalsForActivity only locks the sessions that existed at deploy
// start. Callers are runOpenSession/runAISession, the only two kinds
// sessionMatchesActivity locks for a deploy.
func (a *App) lockNewlyJoinedSessionIfDeployInFlight(selection uiSelection, sessionID int) {
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

// sessionMatchesActivity gates which activities lock sibling terminals. Only a
// deploy does: it rolls out the runtime hosting the env/AI shells, so those
// shells are meaningless until it finishes. Builds, releases, and other
// activities leave the running runtime intact, so they never lock.
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

// releaseNameForTenant must match the runtime chart's helm release name,
// `<tenant>-devops`.
func releaseNameForTenant(tenant string) string {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return ""
	}
	return tenant + "-devops"
}

// namespaceForTenantEnv must mirror the deploy resolver's per-tenant namespace
// pattern, `<tenant>-<environment>`.
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
