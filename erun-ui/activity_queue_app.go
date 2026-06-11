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
	// activityQueueStateEvent is emitted whenever a deploy entry changes state.
	// Frontend subscribes to this to refresh the queue drawer and per-card
	// container statuses without polling.
	activityQueueStateEvent = "activity:state"

	// activityQueueLockEvent is emitted whenever a terminal session's locked
	// status changes. Frontend uses it to render or hide the lock overlay
	// on the affected terminal.
	activityQueueLockEvent = "activity:lock"

	// activityQueuePollInterval governs how often the desktop polls kubectl
	// for container statuses while a deploy is running. Short enough that
	// the user sees pods transition; long enough that polling load is
	// negligible against modest cluster sizes.
	activityQueuePollInterval = 2 * time.Second
)

// activityStateEvent is the payload shape pushed to the frontend on each
// transition. Wails serializes the embedded entry directly; the type alias
// keeps the contract documented and stable.
type activityStateEvent = activityQueueEntry

// activityLockEvent describes a terminal lock transition driven by the deploy
// queue. The frontend keys overlays by SessionID.
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

// finishActivityTracking moves the active deploy for this selection to a
// terminal status. Idempotent: a no-op when no active entry exists for the
// selection.
func (a *App) finishActivityTracking(selection uiSelection, status activityQueueStatus, errMsg string) {
	if a.activityQueue == nil {
		return
	}
	entry, ok := a.activityQueue.findActive(strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment))
	if !ok {
		return
	}
	if final, ok := a.activityQueue.finish(entry.ID, status, errMsg); ok {
		a.unlockTerminalsForActivity(final)
	}
}

// emitActivityState is the persist/notify hook the store uses to tell the
// frontend a deploy entry changed.
func (a *App) emitActivityState(entry activityQueueEntry) {
	a.emitEvent(activityQueueStateEvent, entry)
}

// lockTerminalsForActivity marks every terminal session belonging to the
// deploy's selection as locked and emits a lock event the frontend renders
// as an overlay. Sessions joined later for the same selection will be locked
// by lockTerminalForActivity on a per-session basis.
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
}

// lockTerminalForActivity locks a single session by its serial ID. Used when
// the user joins an in-flight deploy from a session that was created after
// the deploy started.
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
	}
}

// unlockTerminalsForActivity clears the lock on every session whose
// lockedByActivity matches the supplied entry. Idempotent.
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

// sessionMatchesActivity reports whether a managed terminal targets the
// same (tenant, environment) as the activity entry AND the activity is one
// that warrants locking sibling terminals. Only deploys lock terminals —
// they roll out the runtime that hosts the env/AI sessions, so the shell
// becomes meaningless until the deploy finishes. Builds, releases, and
// other activities don't disturb the running runtime, so their cards
// appear in the queue without locking any terminals.
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

// releaseNameForTenant matches the helm release naming used by the runtime
// chart: `<tenant>-devops`. Kept local so the desktop can compute it without
// rebuilding a full DeploySpec.
func releaseNameForTenant(tenant string) string {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return ""
	}
	return tenant + "-devops"
}

// namespaceForTenantEnv mirrors the deploy resolver's per-tenant namespace
// pattern: `<tenant>-<environment>`. If either part is empty, returns
// whichever is non-empty.
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

// activityDeployingLineRe matches the `==> Deploying tenant/env [version]`
// trace emitted by RunHelmDeploy at deploy start. Captures: tenant,
// environment, optional version.
var activityDeployingLineRe = regexp.MustCompile(`^==> Deploying ([^/\s]+)/([^/\s]+)(?:\s+(\S+))?\s*$`)

// activityDeployedLineRe matches the `==> Deployed tenant/env [version]
// in <elapsed>` trace emitted at successful completion. Captures:
// tenant, environment.
var activityDeployedLineRe = regexp.MustCompile(`^==> Deployed ([^/\s]+)/([^/\s]+)\b`)

// activitySkippingLineRe matches the `==> Skipping tenant/env [version]
// (identical deploy already in progress)` trace emitted when the
// dedup decides this caller is a duplicate. Captures: tenant,
// environment.
var activitySkippingLineRe = regexp.MustCompile(`^==> Skipping ([^/\s]+)/([^/\s]+)\b`)

// activityInitializingLineRe matches the umbrella `==> Initializing
// tenant/env` trace emitted by RunBootstrapInit once tenant + env are
// resolved and config writes are about to start. Captures: tenant,
// environment. The matched entry parallels deploy: the user sees an
// init activity in the drawer while bootstrap runs (which itself
// fires a separate deploy entry from `==> Deploying`).
var activityInitializingLineRe = regexp.MustCompile(`^==> Initializing ([^/\s]+)/([^/\s]+)\b`)

// activityInitializedLineRe matches the `==> Initialized tenant/env`
// trace emitted by RunBootstrapInit at successful completion.
// Captures: tenant, environment. See erun-ui/AGENTS.md § "Command
// Completion And State-Refresh Wiring" for why this trace line is the
// completion signal instead of PTY exit: `erun init` runs piped into
// the shared Local shell PTY (via runErunCommandInLocal), so the PTY
// does not exit when init finishes.
var activityInitializedLineRe = regexp.MustCompile(`^==> Initialized ([^/\s]+)/([^/\s]+)\b`)

// activityInitFailedLineRe matches the umbrella `==> Initialization
// failed tenant/env` trace emitted by RunBootstrapInit when a step
// after Initializing returns an error. Captures: tenant, environment.
var activityInitFailedLineRe = regexp.MustCompile(`^==> Initialization failed ([^/\s]+)/([^/\s]+)\b`)

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
// RunReleaseSpec emits for a standalone `erun release` (mirrors
// `==> Building`). Like build they carry no tenant/env, so the handler
// keys the activity off the session selection. `erun build --release`
// does not emit these — runBuildExecution calls the unexported
// runReleaseSpec, keeping that flow under the single `==> Building`
// umbrella.
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
	failedRe := regexp.MustCompile(`^==> Deploy failed`)
	errorRe := regexp.MustCompile(`(?i)^Error: `)
	_ = kind
	return func(line string) {
		line = strings.TrimSpace(line)
		if match := activityDeployingLineRe.FindStringSubmatch(line); match != nil {
			app.startDeployFromTrace(selection, match[1], match[2], match[3])
			return
		}
		if match := activityDeployedLineRe.FindStringSubmatch(line); match != nil {
			app.finishDeployByTenantEnv(selection, match[1], match[2], activityQueueStatusSucceeded, "")
			return
		}
		if match := activitySkippingLineRe.FindStringSubmatch(line); match != nil {
			app.finishDeployByTenantEnv(selection, match[1], match[2], activityQueueStatusSkipped, line)
			return
		}
		if match := activityInitializingLineRe.FindStringSubmatch(line); match != nil {
			app.startInitFromTrace(selection, match[1], match[2])
			return
		}
		if match := activityInitializedLineRe.FindStringSubmatch(line); match != nil {
			app.finishInitByTenantEnv(match[1], match[2], activityQueueStatusSucceeded, "")
			app.emitEnvironmentInitialized(match[1], match[2])
			return
		}
		if match := activityInitFailedLineRe.FindStringSubmatch(line); match != nil {
			app.finishInitByTenantEnv(match[1], match[2], activityQueueStatusFailed, line)
			app.emitEnvironmentInitFailed(match[1], match[2])
			return
		}
		if activityBuildingLineRe.MatchString(line) {
			app.startCommandFromTrace(selection, "build")
			return
		}
		if activityBuiltLineRe.MatchString(line) {
			app.finishCommandBySelection(selection, "build", activityQueueStatusSucceeded, "")
			return
		}
		if activityBuildFailedLineRe.MatchString(line) {
			app.finishCommandBySelection(selection, "build", activityQueueStatusFailed, line)
			return
		}
		if activityReleasingLineRe.MatchString(line) {
			app.startCommandFromTrace(selection, "release")
			return
		}
		if activityReleasedLineRe.MatchString(line) {
			app.finishCommandBySelection(selection, "release", activityQueueStatusSucceeded, "")
			return
		}
		if activityReleaseFailedLineRe.MatchString(line) {
			app.finishCommandBySelection(selection, "release", activityQueueStatusFailed, line)
			return
		}
		if activityPushingLineRe.MatchString(line) {
			app.startCommandFromTrace(selection, "push")
			return
		}
		if activityPushedLineRe.MatchString(line) {
			app.finishCommandBySelection(selection, "push", activityQueueStatusSucceeded, "")
			return
		}
		if activityPushFailedLineRe.MatchString(line) {
			app.finishCommandBySelection(selection, "push", activityQueueStatusFailed, line)
			return
		}
		switch {
		case failedRe.MatchString(line):
			app.finishActivityTracking(selection, activityQueueStatusFailed, line)
		case errorRe.MatchString(line):
			app.captureActivityErrorIfRunning(selection, line)
		}
	}
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
			// A successful deploy supersedes any stale 'failed' flag on the
			// row (issue #498): the env-status clear keeps the sidebar dot
			// and hover card truthful, and the next session exit respawns
			// normally because latestDeployFailed is now false.
			a.emitEnvStatus(uiSelection{Tenant: tenant, Environment: environment}, "")
		}
	}
}

// startInitFromTrace registers an umbrella init entry from a
// `==> Initializing tenant/env` trace observed in any session's PTY.
// Parallels startDeployFromTrace: the init entry covers the whole
// bootstrap (config writes + devops assets + embedded deploy); the
// `==> Deploying` line still registers a separate deploy entry for
// the helm step within init, which is finalized independently by
// `==> Deployed`.
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

// finishInitByTenantEnv finalizes the umbrella init entry on
// `==> Initialized` (succeeded) or `==> Initialization failed`
// (failed). Looks up the entry by parsed tenant/env so the trace
// observed in any session's PTY converges on the same record.
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

// startDeployFromTrace registers a deploy entry from a `==> Deploying`
// trace observed in any session's PTY. No-op when an active entry
// already exists for the selection so the helm poller and trace
// handler converge on the same record.
func (a *App) startDeployFromTrace(selection uiSelection, tenant, environment, version string) {
	if a.activityQueue == nil {
		return
	}
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	version = strings.TrimSpace(version)
	if tenant == "" || environment == "" {
		return
	}
	if _, ok := a.activityQueue.findActiveByCommand("deploy", tenant, environment); ok {
		return
	}
	kubeContext := a.resolveActivityKubeContext(selection, tenant, environment)
	entry, fresh := a.activityQueue.start(activityQueueEntry{
		Command:           "deploy",
		Tenant:            tenant,
		Environment:       environment,
		Version:           version,
		Release:           releaseNameForTenant(tenant),
		Namespace:         namespaceForTenantEnv(tenant, environment),
		KubernetesContext: kubeContext,
		Source:            "trace",
		Summary:           "deploy " + tenant + "/" + environment,
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

// finishCommandBySelection finalizes the build/release/push entry
// registered by startCommandFromTrace. Keyed off the session selection
// because the `==> Built` / `==> Released` / `==> Pushed` (and failed)
// lines carry no tenant/env — see startCommandFromTrace for the
// identity rationale. Idempotent unlock in case a previous code path
// latched a lock that this entry did not.
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

func (a *App) captureActivityErrorIfRunning(selection uiSelection, line string) {
	if a.activityQueue == nil {
		return
	}
	entry, ok := a.activityQueue.findActive(strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment))
	if !ok {
		return
	}
	if strings.TrimSpace(entry.Error) != "" {
		return
	}
	entry.Error = line
	// updateContainers preserves Containers; we don't have a generic
	// update-fields method to avoid widening the API. Use a no-op
	// container slice patch with the existing slice so the persisted entry
	// captures the new error.
	a.activityQueue.updateContainers(entry.ID, entry.Containers)
}

// pollActivityContainerStatuses runs an ad-hoc kubectl poll loop while
// the supplied deploy entry is active. Each tick parses pod JSON for the
// helm release's pods and pushes a snapshot into the queue so the
// frontend can render per-container Ready pills.
//
// The loop is intentionally display-only: it does NOT finalize entries.
// Pod readiness can flip to Ready a few seconds before helm's `--wait`
// returns (Deployment.status.readyReplicas trails container readiness
// while controllers reconcile), so finalizing here would mark the
// activity done while the user's terminal still shows the deploy
// running. Completion is owned by the trace handler's `==> Deployed`
// line (authoritative for trace-source entries — it matches the
// terminal exactly) and by the helm poller's version+freshness check
// (for helm-source entries and as a backstop if the PTY dies).
//
// The loop exits when the entry is no longer active or ctx is cancelled.
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

// fetchActivityContainerStatuses runs `kubectl get pods -l app=<release> -o json`
// and parses the result into the queue's container shape. Errors are
// returned to the caller so transient kubectl outages don't blank the
// previous snapshot — the frontend keeps showing the last-known state until
// the next successful poll.
func (a *App) fetchActivityContainerStatuses(ctx context.Context, entry activityQueueEntry) ([]activityQueueContainerStatus, error) {
	args := []string{"get", "pods", "-l", "app=" + entry.Release, "-o", "json"}
	if strings.TrimSpace(entry.KubernetesContext) != "" {
		args = append([]string{"--context", entry.KubernetesContext}, args...)
	}
	if strings.TrimSpace(entry.Namespace) != "" {
		args = append(args, "--namespace", entry.Namespace)
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseActivityContainerStatuses(out)
}

func parseActivityContainerStatuses(raw []byte) ([]activityQueueContainerStatus, error) {
	var parsed struct {
		Items []struct {
			Spec struct {
				Containers []struct {
					Name  string `json:"name"`
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				ContainerStatuses []struct {
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
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
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
			key := cs.Name
			if seen[key] {
				continue
			}
			seen[key] = true
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
			out = append(out, status)
		}
	}
	return out, nil
}

// activityTargetForRuntime returns the human-readable "tenant/env [version]"
// string used in info lines and lock-overlay messages.
func activityTargetForRuntime(entry activityQueueEntry) string {
	target := strings.TrimSpace(entry.Tenant) + "/" + strings.TrimSpace(entry.Environment)
	if version := strings.TrimSpace(entry.Version); version != "" {
		target += " " + version
	}
	return target
}

// feedActivityTraceFromTerminal accumulates PTY output for any tab that hosts
// a running erun process (Local from the Deploy button, ERun from a manual
// `erun deploy`, AI from claude calling deploy inside the pod), splits on
// newlines, and dispatches each complete line through the deploy trace
// handler. The trace handler is the source-of-truth for deploy lifecycle:
// it auto-registers an entry on `==> Deploying` and finishes it on
// Deployed/failed/Skipping. Selections without a tenant/env are ignored.
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
	lines := []string{}
	for {
		idx := strings.IndexByte(managed.activityTraceBuffer, '\n')
		if idx < 0 {
			break
		}
		raw := managed.activityTraceBuffer[:idx]
		// PTY output ends lines with `\r\n`, so strip a trailing `\r`
		// before considering carriage returns. Then, if the line still
		// contains a `\r` in the middle (spinner-style overwrite —
		// e.g. `\rprogress\rprogress\rfinal\n`), keep only the
		// content after the LAST `\r` so the rendered text matches
		// what the user actually sees on the terminal. The previous
		// implementation took content after the FIRST `\r`, which on
		// the common `text\r\n` case left an empty string and broke
		// every downstream matcher (deploy trace, session-ready
		// detection).
		line := strings.TrimRight(raw, "\r")
		if r := strings.LastIndexByte(line, '\r'); r >= 0 {
			line = line[r+1:]
		}
		lines = append(lines, line)
		managed.activityTraceBuffer = managed.activityTraceBuffer[idx+1:]
	}
	a.mu.Unlock()
	if len(lines) == 0 {
		return
	}
	handler := newActivityTraceLineHandler(a, managed.selection, managed.kind)
	for _, line := range lines {
		// Buffer the line for the active entry before dispatching it, so the
		// "==> Deploy failed" line that finalizes the entry (and the error
		// output preceding it) is already captured when finish() snapshots
		// the buffer into entry.Detail.
		a.activityQueue.recordOutputLine(managed.selection.Tenant, managed.selection.Environment, line)
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

// sessionReady*Re match lines that indicate the session's setup phase
// is done. signalSessionReadyOnLine uses them to release the desktop
// action runner's gate so the next queued action can start. The
// matchers are intentionally broad — any of them firing means the user
// is past the setup phase and a parallel queued action can safely run.
var (
	sessionReadyDeployedRe    = regexp.MustCompile(`^==> Deployed `)
	sessionReadyFailedRe      = regexp.MustCompile(`^==> Deploy failed`)
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
