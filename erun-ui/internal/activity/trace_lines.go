package activity

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// NotificationSourceDeployFailed tags the env notification a failed deploy posts.
const NotificationSourceDeployFailed = "deploy-failed"

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

// TraceLineHandler registers and finalizes activity entries from the trace lines
// of a session of kind; the helm poller converges on the same entries by ID.
func (s *Service) TraceLineHandler(selection uimodel.Selection, kind string) func(string) {
	// Matches both "Deploy of <rel> failed" and "Deploy failed".
	failedRe := regexp.MustCompile(`^==> Deploy (?:of \S+ )?failed`)
	errorRe := regexp.MustCompile(`(?i)^Error: `)
	// Only the Local session that runs `erun init` owns the init lifecycle; a remote
	// open re-prints it, and honoring that would loop deploy→reopen.
	initTraceHonored := kind == KindLocal
	return func(line string) {
		line = strings.TrimSpace(line)
		// Buffer before dispatch so the finalizing line and preceding output are in
		// Detail; subprocess callers have no other path into it.
		s.queue.recordOutputLine(selection.Tenant, selection.Environment, line)
		switch {
		case s.handleDeployTraceLine(selection, line):
		case initTraceHonored && s.handleInitTraceLine(selection, line):
		case s.handleCommandTraceLine(selection, line):
		case s.handleDoctorTraceLine(selection, line):
		case s.handleSSHDInitTraceLine(selection, line):
		case failedRe.MatchString(line):
			s.finishActivityTracking(selection, StatusFailed, cleanActivityFailureLine(line))
		case errorRe.MatchString(line):
			s.captureActivityErrorIfRunning(selection, line)
		}
	}
}

// handleDeployTraceLine reports whether it matched; the match order is part of the contract.
func (s *Service) handleDeployTraceLine(selection uimodel.Selection, line string) bool {
	if match := activityDeployingLineRe.FindStringSubmatch(line); match != nil {
		s.startDeployFromTrace(selection, match[1], match[2], match[3], match[4])
		return true
	}
	if match := activityDeployedLineRe.FindStringSubmatch(line); match != nil {
		s.finishDeployByTenantEnv(selection, match[1], match[2], StatusSucceeded, "")
		return true
	}
	if match := activitySkippingLineRe.FindStringSubmatch(line); match != nil {
		s.finishDeployByTenantEnv(selection, match[1], match[2], StatusSkipped, line)
		return true
	}
	if match := activityDeployFailedLineRe.FindStringSubmatch(line); match != nil {
		reason := strings.TrimSpace(match[3])
		// Surface it even when no `==> Deploying` started an entry.
		s.finishDeployByTenantEnv(selection, match[1], match[2], StatusFailed, reason)
		s.surfaceDeployFailure(match[1], match[2], reason)
		return true
	}
	return false
}

// surfaceDeployFailure's notification is env-tagged so the next deploy or reachability retires it.
func (s *Service) surfaceDeployFailure(tenant, environment, reason string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	s.emitEnvStatus(uimodel.Selection{Tenant: tenant, Environment: environment}, uimodel.EnvStatusFailed)
	message := fmt.Sprintf("Deploy of %s/%s failed.", tenant, environment)
	if reason != "" {
		message = fmt.Sprintf("Deploy of %s/%s failed: %s", tenant, environment, reason)
	}
	s.emitEnvNotification("error", tenant, environment, NotificationSourceDeployFailed, message, "")
}

func (s *Service) handleDoctorTraceLine(selection uimodel.Selection, line string) bool {
	if match := activityDoctorLineRe.FindStringSubmatch(line); match != nil {
		s.startDoctorFromTrace(selection, match[1], match[2])
		return true
	}
	if match := activityDoctorDoneLineRe.FindStringSubmatch(line); match != nil {
		s.finishDoctorByTenantEnv(match[1], match[2], StatusSucceeded, "")
		return true
	}
	if match := activityDoctorFailedLineRe.FindStringSubmatch(line); match != nil {
		s.finishDoctorByTenantEnv(match[1], match[2], StatusFailed, strings.TrimSpace(match[3]))
		return true
	}
	return false
}

func (s *Service) handleSSHDInitTraceLine(selection uimodel.Selection, line string) bool {
	if match := activitySSHDInitLineRe.FindStringSubmatch(line); match != nil {
		s.startSSHDInitFromTrace(selection, match[1], match[2])
		return true
	}
	if match := activitySSHDInitDoneLineRe.FindStringSubmatch(line); match != nil {
		s.finishSSHDInitByTenantEnv(match[1], match[2], StatusSucceeded, "")
		return true
	}
	if match := activitySSHDInitFailedLineRe.FindStringSubmatch(line); match != nil {
		s.finishSSHDInitByTenantEnv(match[1], match[2], StatusFailed, strings.TrimSpace(match[3]))
		return true
	}
	return false
}

func (s *Service) handleInitTraceLine(selection uimodel.Selection, line string) bool {
	if match := activityInitializingLineRe.FindStringSubmatch(line); match != nil {
		s.startInitFromTrace(selection, match[1], match[2])
		return true
	}
	if match := activityInitializedLineRe.FindStringSubmatch(line); match != nil {
		s.finishInitByTenantEnv(match[1], match[2], StatusSucceeded, "")
		s.d.EnvironmentInitialized(match[1], match[2])
		return true
	}
	if match := activityInitFailedLineRe.FindStringSubmatch(line); match != nil {
		s.finishInitByTenantEnv(match[1], match[2], StatusFailed, cleanActivityFailureLine(line))
		s.d.EnvironmentInitFailed(match[1], match[2])
		return true
	}
	return false
}

// handleCommandTraceLine keys on the session selection; the match order is part of the contract.
func (s *Service) handleCommandTraceLine(selection uimodel.Selection, line string) bool {
	if activityBuildingLineRe.MatchString(line) {
		s.startCommandFromTrace(selection, "build")
		return true
	}
	if activityBuiltLineRe.MatchString(line) {
		s.finishCommandBySelection(selection, "build", StatusSucceeded, "")
		return true
	}
	if activityBuildFailedLineRe.MatchString(line) {
		s.finishCommandBySelection(selection, "build", StatusFailed, cleanActivityFailureLine(line))
		return true
	}
	if activityReleasingLineRe.MatchString(line) {
		s.startCommandFromTrace(selection, "release")
		return true
	}
	if activityReleasedLineRe.MatchString(line) {
		s.finishCommandBySelection(selection, "release", StatusSucceeded, "")
		return true
	}
	if activityReleaseFailedLineRe.MatchString(line) {
		s.finishCommandBySelection(selection, "release", StatusFailed, cleanActivityFailureLine(line))
		return true
	}
	if activityPushingLineRe.MatchString(line) {
		s.startCommandFromTrace(selection, "push")
		return true
	}
	if activityPushedLineRe.MatchString(line) {
		s.finishCommandBySelection(selection, "push", StatusSucceeded, "")
		return true
	}
	if activityPushFailedLineRe.MatchString(line) {
		s.finishCommandBySelection(selection, "push", StatusFailed, cleanActivityFailureLine(line))
		return true
	}
	return false
}

// activityCommandErrorPriority makes a bare "Error:" line attach deterministically
// when several entries are active; deploy is the likeliest source.
var activityCommandErrorPriority = []string{"deploy", "push", "build", "release", "init"}

func (s *Service) captureActivityErrorIfRunning(selection uimodel.Selection, line string) {
	tenant := strings.TrimSpace(selection.Tenant)
	environment := strings.TrimSpace(selection.Environment)
	var entry Entry
	found := false
	for _, command := range activityCommandErrorPriority {
		if e, ok := s.queue.findActiveByCommand(command, tenant, environment); ok {
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
	s.queue.updateContainers(entry.ID, entry.Containers)
}
