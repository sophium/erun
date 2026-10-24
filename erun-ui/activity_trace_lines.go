package main

import (
	"fmt"
	"regexp"
	"strings"
)

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
	a.emitEnvNotification("error", tenant, environment, notificationSourceDeployFailed, message, "")
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
