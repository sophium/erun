package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
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

// activityDeployingLineRe matches the `==> Deploying tenant/env [· release]
// [version]` trace emitted at deploy start. A non-runtime component names
// itself after a ` · ` separator (e.g. `erun/local · erun-backend-postgres`);
// the runtime chart omits it, which is how an empty release is read as the
// runtime deploy downstream.
var activityDeployingLineRe = regexp.MustCompile(`^==> Deploying ([^/\s]+)/([^/\s]+)(?: · (\S+))?(?:\s+(\S+))?\s*$`)

// activityDeployedLineRe matches the `==> Deployed tenant/env [version] in
// <elapsed>` trace emitted on successful completion.
var activityDeployedLineRe = regexp.MustCompile(`^==> Deployed ([^/\s]+)/([^/\s]+)\b`)

// activitySkippingLineRe matches the `==> Skipping tenant/env` trace emitted
// when dedup decides this caller duplicates an in-progress deploy.
var activitySkippingLineRe = regexp.MustCompile(`^==> Skipping ([^/\s]+)/([^/\s]+)\b`)

// activityDeployFailedLineRe matches the `==> Deploy failed tenant/env: reason`
// trace emitted on any failure — including a pre-rollout failure like spec
// resolution that fails before `==> Deploying`, which would otherwise leave the
// desktop with no signal to surface.
var activityDeployFailedLineRe = regexp.MustCompile(`^==> Deploy failed ([^/\s]+)/([^/\s]+)(?::\s*(.*))?$`)

// activityInitializingLineRe matches the umbrella `==> Initializing tenant/env`
// trace. Its entry covers the whole bootstrap; the `==> Deploying` line inside
// init still registers its own deploy entry, finalized independently.
var activityInitializingLineRe = regexp.MustCompile(`^==> Initializing ([^/\s]+)/([^/\s]+)\b`)

// activityInitializedLineRe matches the `==> Initialized tenant/env` trace.
// This line, not PTY exit, is init's completion signal: `erun init` runs piped
// into the shared Local shell, which does not exit when init finishes (see
// erun-ui/AGENTS.md § "Command Completion And State-Refresh Wiring").
var activityInitializedLineRe = regexp.MustCompile(`^==> Initialized ([^/\s]+)/([^/\s]+)\b`)

// activityInitFailedLineRe matches the umbrella `==> Initialization failed
// tenant/env` trace emitted when a step after Initializing errors.
var activityInitFailedLineRe = regexp.MustCompile(`^==> Initialization failed ([^/\s]+)/([^/\s]+)\b`)

// activityDoctorLineRe matches the `==> Doctor tenant/env` trace emitted at
// the start of a target-scoped `erun doctor` run.
var activityDoctorLineRe = regexp.MustCompile(`^==> Doctor ([^/\s]+)/([^/\s]+)\s*$`)

// activityDoctorDoneLineRe matches the `==> Doctor done tenant/env` trace.
// This line, not PTY exit, is doctor's completion signal: like `erun init`,
// `erun doctor` runs piped into the shared Local shell (see erun-ui/AGENTS.md
// § "Command Completion And State-Refresh Wiring").
var activityDoctorDoneLineRe = regexp.MustCompile(`^==> Doctor done ([^/\s]+)/([^/\s]+)\b`)

// activityDoctorFailedLineRe matches the `==> Doctor failed tenant/env:
// reason` trace emitted when any step of the target-scoped run errors.
var activityDoctorFailedLineRe = regexp.MustCompile(`^==> Doctor failed ([^/\s]+)/([^/\s]+)(?::\s*(.*))?$`)

// activitySSHDInitLineRe matches the `==> SSHD init tenant/env` trace emitted
// at the start of `erun sshd init`.
var activitySSHDInitLineRe = regexp.MustCompile(`^==> SSHD init ([^/\s]+)/([^/\s]+)\s*$`)

// activitySSHDInitDoneLineRe matches the `==> SSHD init done tenant/env`
// trace. This line, not PTY exit, is sshd init's completion signal: like
// `erun doctor`, `erun sshd init` runs piped into the shared Local shell (see
// erun-ui/AGENTS.md § "Command Completion And State-Refresh Wiring").
var activitySSHDInitDoneLineRe = regexp.MustCompile(`^==> SSHD init done ([^/\s]+)/([^/\s]+)\b`)

// activitySSHDInitFailedLineRe matches the `==> SSHD init failed tenant/env:
// reason` trace emitted when any step of the run errors.
var activitySSHDInitFailedLineRe = regexp.MustCompile(`^==> SSHD init failed ([^/\s]+)/([^/\s]+)(?::\s*(.*))?$`)

// activityBuildingLineRe matches the umbrella `==> Building` trace
// emitted by RunBuildExecution at the top of the build pipeline.
// Unlike deploy/init the line carries no tenant/env — build has no
// deploy target the way RunHelmDeploy does, so the handler attaches
// the activity to the session selection (the terminal tab the user
// is looking at) instead of parsing identity out of the line.
var activityBuildingLineRe = regexp.MustCompile(`^==> Building\b`)

// activityBuiltLineRe matches the `==> Built in <elapsed>` trace
// emitted on successful completion of the build pipeline.
var activityBuiltLineRe = regexp.MustCompile(`^==> Built\b`)

// activityBuildFailedLineRe matches the `==> Build failed after
// <elapsed>` trace emitted when any step of runBuildExecution
// returns an error.
var activityBuildFailedLineRe = regexp.MustCompile(`^==> Build failed\b`)

// activityReleasing/Released/ReleaseFailed match the umbrella traces
// RunReleaseExecution emits for a standalone `erun release` (mirrors
// `==> Building`). Like build they carry no tenant/env, so the handler
// keys the activity off the session selection. `erun build --release`
// runs the same execution but does not emit these, keeping that flow
// under the single `==> Building` umbrella.
var (
	activityReleasingLineRe     = regexp.MustCompile(`^==> Releasing\b`)
	activityReleasedLineRe      = regexp.MustCompile(`^==> Released\b`)
	activityReleaseFailedLineRe = regexp.MustCompile(`^==> Release failed\b`)
)

// activityPushing/Pushed/PushFailed match the umbrella traces
// RunPushCommand emits for a standalone `erun push`. Build-internal
// pushes stay under the `==> Building` umbrella, so these only fire for
// the push command itself.
var (
	activityPushingLineRe    = regexp.MustCompile(`^==> Pushing\b`)
	activityPushedLineRe     = regexp.MustCompile(`^==> Pushed\b`)
	activityPushFailedLineRe = regexp.MustCompile(`^==> Push failed\b`)
)

// activityFailureElapsedClauseRe strips the trailing " after <duration>"
// clause the build/push/release/deploy failure traces carry (e.g. "after
// 3m12s"). It is timing information for the log, not part of what failed.
var activityFailureElapsedClauseRe = regexp.MustCompile(`\s+after\s+\S+\s*$`)

// cleanActivityFailureLine turns a raw `==> ... failed [after <elapsed>]`
// trace line into a short human label for entry.Error — e.g. "==> Build
// failed after 3m12s" becomes "Build failed". The raw marker is still fully
// captured in entry.Detail via recordOutputLine; this only cleans the
// one-line summary the activity card headlines.
func cleanActivityFailureLine(line string) string {
	cleaned := strings.TrimPrefix(strings.TrimSpace(line), "==> ")
	cleaned = activityFailureElapsedClauseRe.ReplaceAllString(cleaned, "")
	return strings.TrimSpace(cleaned)
}

// newActivityTraceLineHandler scans PTY output for trace lines emitted by
// erun deploy and updates the activity queue accordingly.
//
// Two channels feed the queue:
//
//   - PTY trace observation — fast-path early-detection: as soon as
//     `==> Deploying tenant/env [version]` is observed in any session
//     (host-side Local/Command tabs OR in-pod Open/AI tabs), an entry
//     is registered. The trace handler is the responsive signal; the
//     user sees activity in the drawer before any cluster state has
//     to settle.
//   - Helm release poller (activity_helm_poller.go) — authoritative
//     for deploys reflected in cluster state. It collapses onto the
//     same entry by ID, and finalizes the entry when helm reports
//     deployed/failed regardless of whether the trace ever observed
//     the corresponding ==> line.
//
// Terminal-state lines (==> Deployed / ==> Deploy failed / ==> Skipping /
// Error:) finalize the matching active entry from either channel.
func newActivityTraceLineHandler(app *App, selection uiSelection, kind sessionKind) func(string) {
	// RunHelmDeploy names the release on failure ("==> Deploy of <rel> failed
	// after <elapsed>") and falls back to "==> Deploy failed after
	// <elapsed>" only when no release is set; match both shapes.
	failedRe := regexp.MustCompile(`^==> Deploy (?:of \S+ )?failed`)
	errorRe := regexp.MustCompile(`(?i)^Error: `)
	// The init lifecycle (==> Initializing / ==> Initialized / init failed) is
	// honored ONLY from the Local session that actually runs `erun init`. A
	// remote-agent `erun open` re-runs the remote-worktree bootstrap and also
	// prints "==> Initialized"; before this guard the desktop treated that as a
	// fresh env creation and fired another deploy — which rolled the pod (helm
	// --force-conflicts), killed the open shell (exit 137), respawned it, and
	// re-emitted "==> Initialized", an endless deploy⇄reopen loop (and the
	// visible flicker from the rapid respawns). open/ai/command sessions never
	// create the env, so their init-trace lines are display-only here.
	initTraceHonored := kind == sessionKindLocal
	return func(line string) {
		line = strings.TrimSpace(line)
		// Buffer every line for the active entry before dispatching it, so the
		// "==> Deploy failed"/"==> Build failed" line that finalizes the entry
		// (and the tool output preceding it) is already captured when finish()
		// snapshots the buffer into entry.Detail. This must run for every caller
		// of this handler, not just the PTY reader — a subprocess-captured
		// orchestration (no PTY involved) has no other path into Detail, and
		// without it the failed card shows only the raw "==> Build failed after
		// 3m12s" marker with the actual compiler/tool error nowhere to be found.
		if app.activityQueue != nil {
			app.activityQueue.recordOutputLine(selection.Tenant, selection.Environment, line)
		}
		switch {
		case app.handleDeployTraceLine(selection, line):
		case initTraceHonored && app.handleInitTraceLine(selection, line):
		case app.handleCommandTraceLine(selection, line):
		case app.handleDoctorTraceLine(selection, line):
		case app.handleSSHDInitTraceLine(selection, line):
		case failedRe.MatchString(line):
			app.finishActivityTracking(selection, activityQueueStatusFailed, cleanActivityFailureLine(line))
		case errorRe.MatchString(line):
			app.captureActivityErrorIfRunning(selection, line)
		}
	}
}

// handleDeployTraceLine dispatches the deploy lifecycle trace lines, returning
// true on a match so the caller stops. The match order is part of the
// trace-line contract — preserve it.
func (a *App) handleDeployTraceLine(selection uiSelection, line string) bool {
	if match := activityDeployingLineRe.FindStringSubmatch(line); match != nil {
		a.startDeployFromTrace(selection, match[1], match[2], match[3], match[4])
		return true
	}
	if match := activityDeployedLineRe.FindStringSubmatch(line); match != nil {
		a.finishDeployByTenantEnv(selection, match[1], match[2], activityQueueStatusSucceeded, "")
		return true
	}
	if match := activitySkippingLineRe.FindStringSubmatch(line); match != nil {
		a.finishDeployByTenantEnv(selection, match[1], match[2], activityQueueStatusSkipped, line)
		return true
	}
	if match := activityDeployFailedLineRe.FindStringSubmatch(line); match != nil {
		reason := strings.TrimSpace(match[3])
		// Surface the failure in the toolbar so it is visible where the operator
		// is looking — not only in the drawer, and not lost entirely when the
		// failure came before any `==> Deploying` started an entry.
		a.finishDeployByTenantEnv(selection, match[1], match[2], activityQueueStatusFailed, reason)
		a.surfaceDeployFailure(match[1], match[2], reason)
		return true
	}
	return false
}

// surfaceDeployFailure makes a failed deploy visible where the operator is
// looking (Nielsen #1), not just as a red terminal line. The notification is
// env-tagged so the deploy lifecycle retires it once the state moves on — the
// next deploy starts or the runtime becomes reachable.
func (a *App) surfaceDeployFailure(tenant, environment, reason string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	a.emitEnvStatus(uiSelection{Tenant: tenant, Environment: environment}, envStatusFailed)
	message := fmt.Sprintf("Deploy of %s/%s failed.", tenant, environment)
	if reason != "" {
		message = fmt.Sprintf("Deploy of %s/%s failed: %s", tenant, environment, reason)
	}
	a.emitEnvNotification("error", tenant, environment, notificationSourceDeployFailed, message)
}

// handleDoctorTraceLine dispatches the `erun doctor` lifecycle trace lines,
// returning true on a match so the caller stops.
func (a *App) handleDoctorTraceLine(selection uiSelection, line string) bool {
	if match := activityDoctorLineRe.FindStringSubmatch(line); match != nil {
		a.startDoctorFromTrace(selection, match[1], match[2])
		return true
	}
	if match := activityDoctorDoneLineRe.FindStringSubmatch(line); match != nil {
		a.finishDoctorByTenantEnv(match[1], match[2], activityQueueStatusSucceeded, "")
		return true
	}
	if match := activityDoctorFailedLineRe.FindStringSubmatch(line); match != nil {
		a.finishDoctorByTenantEnv(match[1], match[2], activityQueueStatusFailed, strings.TrimSpace(match[3]))
		return true
	}
	return false
}

// handleSSHDInitTraceLine dispatches the `erun sshd init` lifecycle trace
// lines, returning true on a match so the caller stops.
func (a *App) handleSSHDInitTraceLine(selection uiSelection, line string) bool {
	if match := activitySSHDInitLineRe.FindStringSubmatch(line); match != nil {
		a.startSSHDInitFromTrace(selection, match[1], match[2])
		return true
	}
	if match := activitySSHDInitDoneLineRe.FindStringSubmatch(line); match != nil {
		a.finishSSHDInitByTenantEnv(match[1], match[2], activityQueueStatusSucceeded, "")
		return true
	}
	if match := activitySSHDInitFailedLineRe.FindStringSubmatch(line); match != nil {
		a.finishSSHDInitByTenantEnv(match[1], match[2], activityQueueStatusFailed, strings.TrimSpace(match[3]))
		return true
	}
	return false
}

func (a *App) handleInitTraceLine(selection uiSelection, line string) bool {
	if match := activityInitializingLineRe.FindStringSubmatch(line); match != nil {
		a.startInitFromTrace(selection, match[1], match[2])
		return true
	}
	if match := activityInitializedLineRe.FindStringSubmatch(line); match != nil {
		a.finishInitByTenantEnv(match[1], match[2], activityQueueStatusSucceeded, "")
		a.emitEnvironmentInitialized(match[1], match[2])
		return true
	}
	if match := activityInitFailedLineRe.FindStringSubmatch(line); match != nil {
		a.finishInitByTenantEnv(match[1], match[2], activityQueueStatusFailed, cleanActivityFailureLine(line))
		a.emitEnvironmentInitFailed(match[1], match[2])
		return true
	}
	return false
}

// handleCommandTraceLine dispatches the build/release/push umbrella trace
// lines. These carry no tenant/env, so the activity is keyed off the session
// selection. The match order is part of the trace-line contract — preserve it.
func (a *App) handleCommandTraceLine(selection uiSelection, line string) bool {
	if activityBuildingLineRe.MatchString(line) {
		a.startCommandFromTrace(selection, "build")
		return true
	}
	if activityBuiltLineRe.MatchString(line) {
		a.finishCommandBySelection(selection, "build", activityQueueStatusSucceeded, "")
		return true
	}
	if activityBuildFailedLineRe.MatchString(line) {
		a.finishCommandBySelection(selection, "build", activityQueueStatusFailed, cleanActivityFailureLine(line))
		return true
	}
	if activityReleasingLineRe.MatchString(line) {
		a.startCommandFromTrace(selection, "release")
		return true
	}
	if activityReleasedLineRe.MatchString(line) {
		a.finishCommandBySelection(selection, "release", activityQueueStatusSucceeded, "")
		return true
	}
	if activityReleaseFailedLineRe.MatchString(line) {
		a.finishCommandBySelection(selection, "release", activityQueueStatusFailed, cleanActivityFailureLine(line))
		return true
	}
	if activityPushingLineRe.MatchString(line) {
		a.startCommandFromTrace(selection, "push")
		return true
	}
	if activityPushedLineRe.MatchString(line) {
		a.finishCommandBySelection(selection, "push", activityQueueStatusSucceeded, "")
		return true
	}
	if activityPushFailedLineRe.MatchString(line) {
		a.finishCommandBySelection(selection, "push", activityQueueStatusFailed, cleanActivityFailureLine(line))
		return true
	}
	return false
}

// finishDeployByTenantEnv finalizes the active deploy entry for the
// (tenant, environment) parsed from a `==> Deployed`/`==> Skipping`
// trace line, falling back to the session's selection only when the
// line did not name them (an older erun build, or an unexpected
// shape). Looking up by parsed tenant/env keeps finalization
// robust when the trace is observed in a tab whose selection differs
// from the deploy's target — e.g. a generic Local shell where the user
// invoked `erun open` manually and the session selection has no
// tenant/env bound at all.
func (a *App) finishDeployByTenantEnv(selection uiSelection, tenant, environment string, status activityQueueStatus, errMsg string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		a.finishActivityTracking(selection, status, errMsg)
		return
	}
	if a.activityQueue == nil {
		return
	}
	entry, ok := a.activityQueue.findActiveByCommand("deploy", tenant, environment)
	if !ok {
		return
	}
	if final, finished := a.activityQueue.finish(entry.ID, status, errMsg); finished {
		a.unlockTerminalsForActivity(final)
		if status == activityQueueStatusSucceeded {
			// A successful deploy supersedes any stale 'failed' flag on the row:
			// clearing keeps the sidebar dot and hover card truthful and lets the
			// next session exit respawn normally.
			a.emitEnvStatus(uiSelection{Tenant: tenant, Environment: environment}, "")
		}
		if status == activityQueueStatusSucceeded || status == activityQueueStatusSkipped {
			// The runtime is now reachable (deployed, or skipped because it was
			// already current). Signal the create→deploy→open gate so a
			// freshly-created env opens its tabs only after deploy lands, never
			// against a runtime that does not exist.
			a.emitEnvironmentDeployed(tenant, environment)
		}
	}
}

// startInitFromTrace registers the umbrella init entry covering the whole
// bootstrap. The `==> Deploying` line for the helm step within init still
// registers its own deploy entry, finalized independently.
func (a *App) startInitFromTrace(selection uiSelection, tenant, environment string) {
	if a.activityQueue == nil {
		return
	}
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	if _, ok := a.activityQueue.findActiveByCommand("init", tenant, environment); ok {
		return
	}
	kubeContext := a.resolveActivityKubeContext(selection, tenant, environment)
	entry, fresh := a.activityQueue.start(activityQueueEntry{
		Command:           "init",
		Tenant:            tenant,
		Environment:       environment,
		KubernetesContext: kubeContext,
		Source:            "trace",
		Summary:           "init " + tenant + "/" + environment,
	})
	if !fresh {
		return
	}
	a.rememberKubeContextForActivity(kubeContext)
	a.lockTerminalsForActivity(entry)
}

// finishInitByTenantEnv looks the entry up by parsed tenant/env so the trace,
// wherever it is observed, converges on the same init record.
func (a *App) finishInitByTenantEnv(tenant, environment string, status activityQueueStatus, errMsg string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	if a.activityQueue == nil {
		return
	}
	entry, ok := a.activityQueue.findActiveByCommand("init", tenant, environment)
	if !ok {
		return
	}
	if final, finished := a.activityQueue.finish(entry.ID, status, errMsg); finished {
		a.unlockTerminalsForActivity(final)
	}
}

// startDoctorFromTrace registers an activity entry for `erun doctor`, giving
// it a persistent, glanceable presence in the activity drawer/sidebar spinner
// regardless of which surface started it (sidebar, Manage dialog, or a failed
// deploy card's "Run doctor" recovery button). Unlike deploy/init it never
// locks terminals: doctor's recovery actions prompt interactively in the very
// terminal it runs in, and locking that terminal would block the prompt.
func (a *App) startDoctorFromTrace(selection uiSelection, tenant, environment string) {
	if a.activityQueue == nil {
		return
	}
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	if _, ok := a.activityQueue.findActiveByCommand("doctor", tenant, environment); ok {
		return
	}
	kubeContext := a.resolveActivityKubeContext(selection, tenant, environment)
	if _, fresh := a.activityQueue.start(activityQueueEntry{
		Command:           "doctor",
		Tenant:            tenant,
		Environment:       environment,
		KubernetesContext: kubeContext,
		Source:            "trace",
		Summary:           "doctor " + tenant + "/" + environment,
	}); !fresh {
		return
	}
	a.rememberKubeContextForActivity(kubeContext)
}

// finishDoctorByTenantEnv finalizes the activity entry and records the
// persisted last-run outcome the Manage dialog's SSH tab reads
// (state.doctor.lastDoctorBySelection) — the answer "is this healthy?" that
// otherwise vanished the moment the terminal scrolled past it.
func (a *App) finishDoctorByTenantEnv(tenant, environment string, status activityQueueStatus, errMsg string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	if a.activityQueue != nil {
		if entry, ok := a.activityQueue.findActiveByCommand("doctor", tenant, environment); ok {
			a.activityQueue.finish(entry.ID, status, errMsg)
		}
	}
	a.emitDoctorCompleted(tenant, environment, status == activityQueueStatusSucceeded, errMsg)
}

// startSSHDInitFromTrace registers an activity entry for `erun sshd init`,
// giving it a persistent, glanceable presence in the activity drawer/sidebar
// spinner regardless of which surface started it. Unlike doctor, sshd init
// runs no interactive recovery prompts of its own — it is a one-shot
// provisioning action like init/deploy — so it locks terminals the same way
// those do.
func (a *App) startSSHDInitFromTrace(selection uiSelection, tenant, environment string) {
	if a.activityQueue == nil {
		return
	}
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	if _, ok := a.activityQueue.findActiveByCommand("sshd-init", tenant, environment); ok {
		return
	}
	kubeContext := a.resolveActivityKubeContext(selection, tenant, environment)
	entry, fresh := a.activityQueue.start(activityQueueEntry{
		Command:           "sshd-init",
		Tenant:            tenant,
		Environment:       environment,
		KubernetesContext: kubeContext,
		Source:            "trace",
		Summary:           "sshd init " + tenant + "/" + environment,
	})
	if !fresh {
		return
	}
	a.rememberKubeContextForActivity(kubeContext)
	a.lockTerminalsForActivity(entry)
}

// finishSSHDInitByTenantEnv finalizes the activity entry and records the
// persisted last-run outcome the Manage dialog's SSH access section reads —
// the answer "did enabling SSHD work?" that otherwise vanished the moment the
// terminal scrolled past it.
func (a *App) finishSSHDInitByTenantEnv(tenant, environment string, status activityQueueStatus, errMsg string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	if a.activityQueue != nil {
		if entry, ok := a.activityQueue.findActiveByCommand("sshd-init", tenant, environment); ok {
			if final, finished := a.activityQueue.finish(entry.ID, status, errMsg); finished {
				a.unlockTerminalsForActivity(final)
			}
		}
	}
	a.emitSSHDInitCompleted(tenant, environment, status == activityQueueStatusSucceeded, errMsg)
}

// startDeployFromTrace registers a deploy entry from a `==> Deploying` trace.
// No-op when an active entry already exists, so the helm poller and trace
// handler converge on the same record.
func (a *App) startDeployFromTrace(selection uiSelection, tenant, environment, release, version string) {
	if a.activityQueue == nil {
		return
	}
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	release = strings.TrimSpace(release)
	version = strings.TrimSpace(version)
	if tenant == "" || environment == "" {
		return
	}
	if _, ok := a.activityQueue.findActiveByCommand("deploy", tenant, environment); ok {
		return
	}
	// The runtime chart's `==> Deploying` line carries no release token, so an
	// empty parsed release means the runtime deploy; fall back to its release
	// name. A non-runtime component names itself, so the drawer labels it by
	// component ("deploy erun/local · erun-backend-postgres") instead of
	// reading like a full-env redeploy.
	summary := "deploy " + tenant + "/" + environment
	if release == "" {
		release = releaseNameForTenant(tenant)
	} else {
		summary += " · " + release
	}
	kubeContext := a.resolveActivityKubeContext(selection, tenant, environment)
	entry, fresh := a.activityQueue.start(activityQueueEntry{
		Command:           "deploy",
		Tenant:            tenant,
		Environment:       environment,
		Version:           version,
		Release:           release,
		Namespace:         namespaceForTenantEnv(tenant, environment),
		KubernetesContext: kubeContext,
		Source:            "trace",
		Summary:           summary,
	})
	if !fresh {
		return
	}
	a.rememberKubeContextForActivity(kubeContext)
	a.lockTerminalsForActivity(entry)
	if a.activityStatusPoller != nil {
		a.activityStatusPoller(entry)
	}
}

// startCommandFromTrace registers a build/release/push entry from a
// `==> Building` / `==> Releasing` / `==> Pushing` trace observed in
// the session's PTY. Unlike deploy/init these umbrella lines carry no
// tenant/env (build, release, and push have no deploy target the way
// RunHelmDeploy does), so the activity is keyed off the session
// selection — the user invoked the command from a specific terminal
// tab, and that tab's env row is the natural place to render the
// spinner. No-op when the selection has no tenant/env yet (a generic
// Local shell at the repo level): there is no row to attach the
// indicator to, and a stray activity entry with empty tenant/env
// would land on every row.
//
// Intentionally skips lockTerminalsForActivity: deploys lock the
// session so a user cannot run conflicting commands while helm is
// rolling out, but build/release/push run IN the user's terminal —
// locking it would freeze the very tab the user is reading output in.
// The sidebar spinner is the only indicator these commands need.
func (a *App) startCommandFromTrace(selection uiSelection, command string) {
	if a.activityQueue == nil {
		return
	}
	tenant := strings.TrimSpace(selection.Tenant)
	environment := strings.TrimSpace(selection.Environment)
	if tenant == "" || environment == "" {
		return
	}
	if _, ok := a.activityQueue.findActiveByCommand(command, tenant, environment); ok {
		return
	}
	kubeContext := a.resolveActivityKubeContext(selection, tenant, environment)
	if _, fresh := a.activityQueue.start(activityQueueEntry{
		Command:           command,
		Tenant:            tenant,
		Environment:       environment,
		KubernetesContext: kubeContext,
		Source:            "trace",
		Summary:           command + " " + tenant + "/" + environment,
	}); !fresh {
		return
	}
	a.rememberKubeContextForActivity(kubeContext)
}

// finishCommandBySelection is keyed off the session selection because the
// build/release/push completion lines carry no tenant/env — see
// startCommandFromTrace. The unlock is idempotent in case an earlier code path
// latched a lock this entry did not.
func (a *App) finishCommandBySelection(selection uiSelection, command string, status activityQueueStatus, errMsg string) {
	if a.activityQueue == nil {
		return
	}
	tenant := strings.TrimSpace(selection.Tenant)
	environment := strings.TrimSpace(selection.Environment)
	if tenant == "" || environment == "" {
		return
	}
	entry, ok := a.activityQueue.findActiveByCommand(command, tenant, environment)
	if !ok {
		return
	}
	if final, finished := a.activityQueue.finish(entry.ID, status, errMsg); finished {
		a.unlockTerminalsForActivity(final)
	}
}

// resolveActivityKubeContext picks the kube context to attach to a new
// trace-source activity entry. Selection wins when it carries a
// context (the session has been bound through the desktop UI). When it
// doesn't — e.g. a generic Local tab where the user invoked `erun open`
// manually — fall back to the env config's KubernetesContext so the
// container-status poller and lock-on-deploy watchlist still target
// the right cluster instead of whatever `kubectl config
// current-context` happens to be.
func (a *App) resolveActivityKubeContext(selection uiSelection, tenant, environment string) string {
	if ctx := strings.TrimSpace(selection.KubernetesContext); ctx != "" {
		return ctx
	}
	if a.deps.store == nil {
		return ""
	}
	envConfig, _, err := a.deps.store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(envConfig.KubernetesContext)
}

// activityCommandErrorPriority orders the commands captureActivityErrorIfRunning
// tries when attaching a bare "Error: ..." line to the entry it belongs to. A
// generic error line carries no command of its own, so when more than one
// entry is active for the same tenant/env (e.g. a lingering build entry
// beside a just-started deploy) the choice must be deterministic — not
// plain findActive's Go map iteration order, which let a helm failure land on
// the build card instead of the deploy card roughly half the time. Deploy is
// checked first because it is the longest-running step and the one most
// likely still active when a generic tool error line (e.g. "Error: UPGRADE
// FAILED: ...") appears.
var activityCommandErrorPriority = []string{"deploy", "push", "build", "release", "init"}

func (a *App) captureActivityErrorIfRunning(selection uiSelection, line string) {
	if a.activityQueue == nil {
		return
	}
	tenant := strings.TrimSpace(selection.Tenant)
	environment := strings.TrimSpace(selection.Environment)
	var entry activityQueueEntry
	found := false
	for _, command := range activityCommandErrorPriority {
		if e, ok := a.activityQueue.findActiveByCommand(command, tenant, environment); ok {
			entry, found = e, true
			break
		}
	}
	if !found {
		return
	}
	if strings.TrimSpace(entry.Error) != "" {
		return
	}
	entry.Error = line
	// No generic update-fields method exists (the store API is kept narrow), so
	// persist the new error by re-patching the existing container slice.
	a.activityQueue.updateContainers(entry.ID, entry.Containers)
}

// pollActivityContainerStatuses feeds the queue per-container Ready snapshots
// so the frontend can render Ready pills while a deploy runs.
//
// The loop is intentionally display-only: it does NOT finalize entries. Pod
// readiness can flip to Ready seconds before helm's `--wait` returns
// (readyReplicas trails container readiness while controllers reconcile), so
// finalizing here would mark the activity done while the terminal still shows
// the deploy running. Completion is owned by the trace handler's `==> Deployed`
// line and by the helm poller's version+freshness check (a backstop if the PTY
// dies).
func (a *App) pollActivityContainerStatuses(ctx context.Context, entry activityQueueEntry) {
	if a.activityQueue == nil {
		return
	}
	ticker := time.NewTicker(activityQueuePollInterval)
	defer ticker.Stop()
	pollOnce := func() bool {
		if _, ok := a.activityQueue.findActive(entry.Tenant, entry.Environment); !ok {
			return false
		}
		statuses, err := a.fetchActivityContainerStatuses(ctx, entry)
		if err != nil {
			return true
		}
		a.activityQueue.updateContainers(entry.ID, statuses)
		return true
	}
	if !pollOnce() {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !pollOnce() {
				return
			}
		}
	}
}

// fetchActivityContainerStatuses returns an error rather than an empty snapshot
// on a transient kubectl outage, so the frontend keeps showing the last-known
// state until the next successful poll.
func (a *App) fetchActivityContainerStatuses(ctx context.Context, entry activityQueueEntry) ([]activityQueueContainerStatus, error) {
	args := []string{"get", "pods", "-l", "app=" + entry.Release, "-o", "json"}
	if strings.TrimSpace(entry.KubernetesContext) != "" {
		args = append([]string{"--context", entry.KubernetesContext}, args...)
	}
	if strings.TrimSpace(entry.Namespace) != "" {
		args = append(args, "--namespace", entry.Namespace)
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	eruncommon.HideConsoleWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseActivityContainerStatuses(out)
}

// kubectlPodList is the subset of `kubectl get pods -o json` the container-status
// poller decodes.
type kubectlPodList struct {
	Items []kubectlPodItem `json:"items"`
}

type kubectlPodItem struct {
	Spec struct {
		Containers []struct {
			Name  string `json:"name"`
			Image string `json:"image"`
		} `json:"containers"`
	} `json:"spec"`
	Status struct {
		ContainerStatuses []kubectlContainerStatus `json:"containerStatuses"`
	} `json:"status"`
}

type kubectlContainerStatus struct {
	Name         string `json:"name"`
	Image        string `json:"image"`
	Ready        bool   `json:"ready"`
	RestartCount int    `json:"restartCount"`
	State        struct {
		Waiting *struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"waiting,omitempty"`
		Running *struct {
			StartedAt string `json:"startedAt"`
		} `json:"running,omitempty"`
		Terminated *struct {
			Reason   string `json:"reason"`
			Message  string `json:"message"`
			ExitCode int    `json:"exitCode"`
		} `json:"terminated,omitempty"`
	} `json:"state"`
}

func parseActivityContainerStatuses(raw []byte) ([]activityQueueContainerStatus, error) {
	var parsed kubectlPodList
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	out := make([]activityQueueContainerStatus, 0)
	seen := make(map[string]bool)
	for _, item := range parsed.Items {
		// Spec containers give us images even when Status hasn't reported
		// them yet (very early in pod lifecycle).
		imageByName := make(map[string]string, len(item.Spec.Containers))
		for _, c := range item.Spec.Containers {
			imageByName[c.Name] = c.Image
		}
		for _, cs := range item.Status.ContainerStatuses {
			if seen[cs.Name] {
				continue
			}
			seen[cs.Name] = true
			out = append(out, convertActivityContainerStatus(cs, imageByName))
		}
	}
	return out, nil
}

func convertActivityContainerStatus(cs kubectlContainerStatus, imageByName map[string]string) activityQueueContainerStatus {
	image := strings.TrimSpace(cs.Image)
	if image == "" {
		image = imageByName[cs.Name]
	}
	status := activityQueueContainerStatus{
		Name:     cs.Name,
		Image:    image,
		Ready:    cs.Ready,
		Restarts: cs.RestartCount,
	}
	switch {
	case cs.State.Running != nil:
		status.Phase = "Running"
	case cs.State.Waiting != nil:
		status.Phase = "Waiting"
		status.Reason = cs.State.Waiting.Reason
		status.Message = cs.State.Waiting.Message
	case cs.State.Terminated != nil:
		status.Phase = "Terminated"
		status.Reason = cs.State.Terminated.Reason
		status.Message = fmt.Sprintf("exited with code %d", cs.State.Terminated.ExitCode)
		if msg := strings.TrimSpace(cs.State.Terminated.Message); msg != "" {
			status.Message += ": " + msg
		}
	default:
		status.Phase = "Pending"
	}
	return status
}

func activityTargetForRuntime(entry activityQueueEntry) string {
	target := strings.TrimSpace(entry.Tenant) + "/" + strings.TrimSpace(entry.Environment)
	if version := strings.TrimSpace(entry.Version); version != "" {
		target += " " + version
	}
	return target
}

// feedActivityTraceFromTerminal drives the activity queue from any tab that
// hosts a running erun process (Local from the Deploy button, ERun from a
// manual `erun deploy`, AI from Claude deploying inside the pod), dispatching
// each complete line through the trace handler that owns deploy lifecycle.
func (a *App) feedActivityTraceFromTerminal(managed *managedTerminal, chunk []byte) {
	if managed == nil || a.activityQueue == nil {
		return
	}
	if managed.selection.Tenant == "" || managed.selection.Environment == "" {
		return
	}
	switch managed.kind {
	case sessionKindLocal, sessionKindOpen, sessionKindAI, sessionKindCommand,
		sessionKindContributeERun, sessionKindContributeAI:
	default:
		return
	}
	a.mu.Lock()
	managed.activityTraceBuffer += stripActivityTraceANSI(string(chunk))
	lines := drainActivityTraceLines(managed)
	a.mu.Unlock()
	if len(lines) == 0 {
		return
	}
	handler := newActivityTraceLineHandler(a, managed.selection, managed.kind)
	for _, line := range lines {
		// newActivityTraceLineHandler itself buffers the line into entry.Detail
		// before dispatching, so every caller (this PTY reader and the
		// subprocess-captured orchestration path) gets the same Detail capture.
		handler(line)
		signalSessionReadyOnLine(managed, line)
		// The CLI's taken-over notice is a public contract line (see
		// eruncommon.ShellSessionTakenOverNotice): another ERun window
		// re-attached this persistent session, so the upcoming PTY exit
		// must not trigger a reconnect that would steal it back.
		if strings.TrimSpace(line) == eruncommon.ShellSessionTakenOverNotice {
			a.markSessionTakenOver(managed)
		}
	}
}

// drainActivityTraceLines returns every complete `\n`-terminated line, leaving
// any trailing partial line buffered for the next chunk. The caller must hold
// a.mu.
func drainActivityTraceLines(managed *managedTerminal) []string {
	lines := []string{}
	for {
		idx := strings.IndexByte(managed.activityTraceBuffer, '\n')
		if idx < 0 {
			break
		}
		raw := managed.activityTraceBuffer[:idx]
		// Strip the trailing `\r` of the PTY's `\r\n`, then on a spinner-style
		// overwrite (`\rprogress\rprogress\rfinal`) keep only the content after
		// the LAST `\r` so the text matches what the user sees. Taking the first
		// `\r` instead would blank the common `text\r\n` case and break every
		// downstream matcher.
		line := strings.TrimRight(raw, "\r")
		if r := strings.LastIndexByte(line, '\r'); r >= 0 {
			line = line[r+1:]
		}
		lines = append(lines, line)
		managed.activityTraceBuffer = managed.activityTraceBuffer[idx+1:]
	}
	return lines
}

// sessionReady*Re match lines that indicate the session's setup phase
// is done. signalSessionReadyOnLine uses them to release the desktop
// action runner's gate so the next queued action can start. The
// matchers are intentionally broad — any of them firing means the user
// is past the setup phase and a parallel queued action can safely run.
var (
	sessionReadyDeployedRe    = regexp.MustCompile(`^==> Deployed `)
	sessionReadyFailedRe      = regexp.MustCompile(`^==> Deploy (?:of \S+ )?failed`)
	sessionReadySkippedRe     = regexp.MustCompile(`^==> Skipping `)
	sessionReadyAttachedRe    = regexp.MustCompile(`^Defaulted container "[^"]+" out of:`)
	sessionReadyShellPromptRe = regexp.MustCompile(`[\w][\w.-]*@[\w][\w.-]*:[~/].*[\$#]\s*$`)
)

func signalSessionReadyOnLine(managed *managedTerminal, line string) {
	if managed == nil {
		return
	}
	trimmed := strings.TrimSpace(line)
	switch {
	case sessionReadyDeployedRe.MatchString(trimmed),
		sessionReadySkippedRe.MatchString(trimmed),
		sessionReadyAttachedRe.MatchString(trimmed),
		sessionReadyShellPromptRe.MatchString(trimmed):
		managed.signalReady(nil)
	case sessionReadyFailedRe.MatchString(trimmed):
		managed.signalReady(fmt.Errorf("%s", trimmed))
	}
}

// stripActivityTraceANSI removes the most common ANSI control sequences erun
// emits via spinner/colors so trace-line matching survives terminal noise.
// The patterns are intentionally narrow (CSI-style escape sequences) to
// avoid clobbering meaningful output.
func stripActivityTraceANSI(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == 0x1b && i+1 < len(runes) && runes[i+1] == '[' {
			j := i + 2
			for j < len(runes) {
				r := runes[j]
				if r >= '@' && r <= '~' {
					j++
					break
				}
				j++
			}
			i = j - 1
			continue
		}
		b.WriteRune(runes[i])
	}
	return b.String()
}
