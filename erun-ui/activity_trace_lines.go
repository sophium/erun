package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// activityDeployingLineRe: a non-runtime component follows ` · `; an empty release means the runtime chart.
var activityDeployingLineRe = regexp.MustCompile(`^==> Deploying ([^/\s]+)/([^/\s]+)(?: · (\S+))?(?:\s+(\S+))?\s*$`)

var activityDeployedLineRe = regexp.MustCompile(`^==> Deployed ([^/\s]+)/([^/\s]+)\b`)

// activitySkippingLineRe matches when dedup finds an in-progress deploy.
var activitySkippingLineRe = regexp.MustCompile(`^==> Skipping ([^/\s]+)/([^/\s]+)\b`)

// activityDeployFailedLineRe also fires for pre-rollout failures that precede `==> Deploying`.
var activityDeployFailedLineRe = regexp.MustCompile(`^==> Deploy failed ([^/\s]+)/([^/\s]+)(?::\s*(.*))?$`)

var activityInitializingLineRe = regexp.MustCompile(`^==> Initializing ([^/\s]+)/([^/\s]+)\b`)

// activityInitializedLineRe is init's completion signal; the Local shell does not exit after init.
var activityInitializedLineRe = regexp.MustCompile(`^==> Initialized ([^/\s]+)/([^/\s]+)\b`)

var activityInitFailedLineRe = regexp.MustCompile(`^==> Initialization failed ([^/\s]+)/([^/\s]+)\b`)

var activityDoctorLineRe = regexp.MustCompile(`^==> Doctor ([^/\s]+)/([^/\s]+)\s*$`)

// activityDoctorDoneLineRe is doctor's completion signal, as with init.
var activityDoctorDoneLineRe = regexp.MustCompile(`^==> Doctor done ([^/\s]+)/([^/\s]+)\b`)

var activityDoctorFailedLineRe = regexp.MustCompile(`^==> Doctor failed ([^/\s]+)/([^/\s]+)(?::\s*(.*))?$`)

var activitySSHDInitLineRe = regexp.MustCompile(`^==> SSHD init ([^/\s]+)/([^/\s]+)\s*$`)

// activitySSHDInitDoneLineRe is sshd init's completion signal, as with init.
var activitySSHDInitDoneLineRe = regexp.MustCompile(`^==> SSHD init done ([^/\s]+)/([^/\s]+)\b`)

var activitySSHDInitFailedLineRe = regexp.MustCompile(`^==> SSHD init failed ([^/\s]+)/([^/\s]+)(?::\s*(.*))?$`)

// activityBuildingLineRe carries no tenant/env, so build activity keys on the session selection.
var activityBuildingLineRe = regexp.MustCompile(`^==> Building\b`)

var activityBuiltLineRe = regexp.MustCompile(`^==> Built\b`)

var activityBuildFailedLineRe = regexp.MustCompile(`^==> Build failed\b`)

// Standalone `erun release` only; `erun build --release` stays under `==> Building`.
var (
	activityReleasingLineRe     = regexp.MustCompile(`^==> Releasing\b`)
	activityReleasedLineRe      = regexp.MustCompile(`^==> Released\b`)
	activityReleaseFailedLineRe = regexp.MustCompile(`^==> Release failed\b`)
)

// Standalone `erun push` only; build-internal pushes stay under `==> Building`.
var (
	activityPushingLineRe    = regexp.MustCompile(`^==> Pushing\b`)
	activityPushedLineRe     = regexp.MustCompile(`^==> Pushed\b`)
	activityPushFailedLineRe = regexp.MustCompile(`^==> Push failed\b`)
)

var activityFailureElapsedClauseRe = regexp.MustCompile(`\s+after\s+\S+\s*$`)

// cleanActivityFailureLine turns "==> Build failed after 3m12s" into "Build failed".
func cleanActivityFailureLine(line string) string {
	cleaned := strings.TrimPrefix(strings.TrimSpace(line), "==> ")
	cleaned = activityFailureElapsedClauseRe.ReplaceAllString(cleaned, "")
	return strings.TrimSpace(cleaned)
}

// newActivityTraceLineHandler registers and finalizes activity entries from PTY
// trace lines; the helm poller converges on the same entries by ID.
func newActivityTraceLineHandler(app *App, selection uimodel.Selection, kind sessionKind) func(string) {
	// Matches both "Deploy of <rel> failed" and "Deploy failed".
	failedRe := regexp.MustCompile(`^==> Deploy (?:of \S+ )?failed`)
	errorRe := regexp.MustCompile(`(?i)^Error: `)
	// Only the Local session that runs `erun init` owns the init lifecycle; a remote
	// open re-prints it, and honoring that would loop deploy→reopen.
	initTraceHonored := kind == sessionKindLocal
	return func(line string) {
		line = strings.TrimSpace(line)
		// Buffer before dispatch so the finalizing line and preceding output are in
		// Detail; subprocess callers have no other path into it.
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

// handleDeployTraceLine reports whether it matched; the match order is part of the contract.
func (a *App) handleDeployTraceLine(selection uimodel.Selection, line string) bool {
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
		// Surface it even when no `==> Deploying` started an entry.
		a.finishDeployByTenantEnv(selection, match[1], match[2], activityQueueStatusFailed, reason)
		a.surfaceDeployFailure(match[1], match[2], reason)
		return true
	}
	return false
}

// surfaceDeployFailure's notification is env-tagged so the next deploy or reachability retires it.
func (a *App) surfaceDeployFailure(tenant, environment, reason string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	a.emitEnvStatus(uimodel.Selection{Tenant: tenant, Environment: environment}, uimodel.EnvStatusFailed)
	message := fmt.Sprintf("Deploy of %s/%s failed.", tenant, environment)
	if reason != "" {
		message = fmt.Sprintf("Deploy of %s/%s failed: %s", tenant, environment, reason)
	}
	a.emitEnvNotification("error", tenant, environment, notificationSourceDeployFailed, message, "")
}

func (a *App) handleDoctorTraceLine(selection uimodel.Selection, line string) bool {
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

func (a *App) handleSSHDInitTraceLine(selection uimodel.Selection, line string) bool {
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

func (a *App) handleInitTraceLine(selection uimodel.Selection, line string) bool {
	if match := activityInitializingLineRe.FindStringSubmatch(line); match != nil {
		a.startInitFromTrace(selection, match[1], match[2])
		return true
	}
	if match := activityInitializedLineRe.FindStringSubmatch(line); match != nil {
		a.finishInitByTenantEnv(match[1], match[2], activityQueueStatusSucceeded, "")
		a.envops.EnvironmentInitialized(match[1], match[2])
		return true
	}
	if match := activityInitFailedLineRe.FindStringSubmatch(line); match != nil {
		a.finishInitByTenantEnv(match[1], match[2], activityQueueStatusFailed, cleanActivityFailureLine(line))
		a.envops.EnvironmentInitFailed(match[1], match[2])
		return true
	}
	return false
}

// handleCommandTraceLine keys on the session selection; the match order is part of the contract.
func (a *App) handleCommandTraceLine(selection uimodel.Selection, line string) bool {
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

// activityCommandErrorPriority makes a bare "Error:" line attach deterministically
// when several entries are active; deploy is the likeliest source.
var activityCommandErrorPriority = []string{"deploy", "push", "build", "release", "init"}

func (a *App) captureActivityErrorIfRunning(selection uimodel.Selection, line string) {
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
	// The store has no generic field update, so re-patch the containers to persist the error.
	a.activityQueue.updateContainers(entry.ID, entry.Containers)
}
