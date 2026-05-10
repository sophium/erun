package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
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

	// activitySteadyReadyTicks is the number of consecutive poll ticks
	// (≈ 2s each) over which every container must report Ready and not
	// failing before the desktop finalizes the deploy as succeeded. The
	// short hysteresis avoids declaring success during a flap (e.g. a
	// container briefly Ready then CrashLoopBackOff) while still
	// finalizing within ~6 seconds of the rollout completing.
	activitySteadyReadyTicks = 3
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
// queue. Used for stuck active entries the watcher cannot finalize on its
// own (typically a deploy whose marker is on the runtime pod's filesystem
// and unreachable from the host, or an `erun open` whose backing process
// was killed externally). The desktop also removes the on-disk marker
// when the host can reach it, and adds the ID to an ignore set so the
// watcher does not re-register it on its next tick. Wails-exported.
func (a *App) ForceDismissActivity(id string) bool {
	if a.activityQueue == nil {
		return false
	}
	entry, _, ok := a.activityQueue.forceDismiss(id)
	if !ok {
		return false
	}
	a.unlockTerminalsForActivity(entry)
	a.markActivityIgnored(entry.ID)
	if path := strings.TrimSpace(entry.MarkerPath); path != "" {
		_ = removeFileIfExists(path)
	}
	a.emitActivityState(entry)
	return true
}

// markActivityIgnored records an ID the user explicitly dismissed so the
// marker watcher won't re-register it on the next tick. The set is
// process-local; on next desktop launch the marker (if it still exists)
// is reconsidered fresh.
func (a *App) markActivityIgnored(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	a.activityIgnoredMu.Lock()
	defer a.activityIgnoredMu.Unlock()
	if a.activityIgnored == nil {
		a.activityIgnored = make(map[string]struct{})
	}
	a.activityIgnored[id] = struct{}{}
}

func (a *App) isActivityIgnored(id string) bool {
	a.activityIgnoredMu.Lock()
	defer a.activityIgnoredMu.Unlock()
	_, ok := a.activityIgnored[strings.TrimSpace(id)]
	return ok
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

// activityRegistrationIsAuthoritative documents that this desktop no
// longer maintains a desktop-side "explicit deploy registration" path:
// the on-disk RunningCommand marker every CLI deploy writes is the
// authoritative source, picked up by runActivityMarkerWatcher within one
// poll tick. The helper below stays for tests that exercise the lock
// transitions; it does not start a tracking entry on its own anymore.
func activityRegistrationIsAuthoritative() {}

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
// environment, optional version. Used by trace-based registration when the
// deploy runs inside the runtime pod (the marker watcher cannot read the
// pod's filesystem from the host).
var activityDeployingLineRe = regexp.MustCompile(`^==> Deploying ([^/\s]+)/([^/\s]+)(?:\s+(\S+))?\s*$`)

// newActivityTraceLineHandler scans PTY output for trace lines emitted by
// erun commands and updates the activity queue accordingly.
//
// Two channels feed the queue:
//
//   - Marker watcher (runActivityMarkerWatcher) — authoritative for every
//     command running on the host filesystem the desktop can read.
//   - PTY trace observation — fallback for commands running inside the
//     runtime pod, whose marker file is on the pod's filesystem and not
//     visible to the host. Trace-based registration is gated on session
//     kind: only Open/AI sessions (kubectl-exec'd into the pod) trigger
//     it, so the host marker channel stays the single source of truth
//     for host-side commands.
//
// Terminal-state lines (==> Deployed / ==> Deploy failed / ==> Skipping /
// Error:) finalize the matching active entry from either channel.
func newActivityTraceLineHandler(app *App, selection uiSelection, kind sessionKind) func(string) {
	deployedRe := regexp.MustCompile(`^==> Deployed `)
	failedRe := regexp.MustCompile(`^==> Deploy failed`)
	skippedRe := regexp.MustCompile(`^==> Skipping `)
	errorRe := regexp.MustCompile(`(?i)^Error: `)
	return func(line string) {
		line = strings.TrimSpace(line)
		if isInPodSession(kind) {
			if match := activityDeployingLineRe.FindStringSubmatch(line); match != nil {
				app.startInPodDeployFromTrace(selection, match[1], match[2], match[3])
				return
			}
		}
		switch {
		case deployedRe.MatchString(line):
			app.finishActivityTracking(selection, activityQueueStatusSucceeded, "")
		case skippedRe.MatchString(line):
			app.finishActivityTracking(selection, activityQueueStatusSkipped, line)
		case failedRe.MatchString(line):
			app.finishActivityTracking(selection, activityQueueStatusFailed, line)
		case errorRe.MatchString(line):
			app.captureActivityErrorIfRunning(selection, line)
		}
	}
}

func isInPodSession(kind sessionKind) bool {
	return kind == sessionKindOpen || kind == sessionKindAI
}

// startInPodDeployFromTrace registers a deploy entry from a `==> Deploying`
// trace observed on an in-pod session's PTY. No-op when an entry already
// exists for the selection so the host marker channel stays authoritative.
func (a *App) startInPodDeployFromTrace(selection uiSelection, tenant, environment, version string) {
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
	entry, fresh := a.activityQueue.start(activityQueueEntry{
		Command:           "deploy",
		Tenant:            tenant,
		Environment:       environment,
		Version:           version,
		Release:           releaseNameForTenant(tenant),
		Namespace:         namespaceForTenantEnv(tenant, environment),
		KubernetesContext: strings.TrimSpace(selection.KubernetesContext),
		Summary:           "deploy " + tenant + "/" + environment,
	})
	if !fresh {
		return
	}
	a.lockTerminalsForActivity(entry)
}

// allContainersReadyAndHealthy returns true when every container in the
// snapshot is in a healthy Ready state. Empty snapshots are treated as
// not-ready (we don't have evidence of a healthy rollout yet). Failing
// reasons override Ready=true even if kubelet temporarily reports both.
func allContainersReadyAndHealthy(statuses []activityQueueContainerStatus) bool {
	if len(statuses) == 0 {
		return false
	}
	for _, status := range statuses {
		if !status.Ready {
			return false
		}
		if !containerHealthy(status) {
			return false
		}
	}
	return true
}

func containerHealthy(status activityQueueContainerStatus) bool {
	if status.Phase == "Terminated" {
		return false
	}
	switch strings.TrimSpace(status.Reason) {
	case "ImagePullBackOff",
		"ErrImagePull",
		"CrashLoopBackOff",
		"CreateContainerConfigError",
		"CreateContainerError",
		"InvalidImageName",
		"OOMKilled",
		"Error",
		"RunContainerError":
		return false
	}
	return true
}

// finalizeDeployFromPodReadiness moves a still-running deploy entry into
// history with success status. Used by the pod-status poller when every
// container has been Ready and healthy across activitySteadyReadyTicks
// consecutive ticks. Idempotent — finish() returns false on a second
// call so a racing trace handler that also tries to finish the same
// entry is harmless.
func (a *App) finalizeDeployFromPodReadiness(id string) {
	if a.activityQueue == nil {
		return
	}
	if final, ok := a.activityQueue.finish(id, activityQueueStatusSucceeded, ""); ok {
		a.unlockTerminalsForActivity(final)
	}
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
// helm release's pods and pushes a snapshot into the queue.
//
// When all containers in the snapshot are Ready and none are failing for
// activitySteadyReadyWindow consecutive ticks, finalize the entry as
// succeeded — the marker file may be on a runtime-pod filesystem the
// host cannot read, so the in-pod CLI's FinalizeRunningCommand call is
// invisible from here. Pod readiness is the user-visible definition of
// "deploy is done", and using it lifts the terminal lock that depends
// on the entry's running state.
//
// The loop exits when the entry is no longer active or ctx is cancelled.
func (a *App) pollActivityContainerStatuses(ctx context.Context, entry activityQueueEntry) {
	if a.activityQueue == nil {
		return
	}
	ticker := time.NewTicker(activityQueuePollInterval)
	defer ticker.Stop()
	steadyReadyTicks := 0
	pollOnce := func() bool {
		if _, ok := a.activityQueue.findActive(entry.Tenant, entry.Environment); !ok {
			return false
		}
		statuses, err := a.fetchActivityContainerStatuses(ctx, entry)
		if err != nil {
			steadyReadyTicks = 0
			return true
		}
		a.activityQueue.updateContainers(entry.ID, statuses)
		if allContainersReadyAndHealthy(statuses) {
			steadyReadyTicks++
			if steadyReadyTicks >= activitySteadyReadyTicks {
				a.finalizeDeployFromPodReadiness(entry.ID)
				return false
			}
		} else {
			steadyReadyTicks = 0
		}
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
	case sessionKindLocal, sessionKindOpen, sessionKindAI, sessionKindCommand:
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
		line := managed.activityTraceBuffer[:idx]
		if r := strings.IndexByte(line, '\r'); r >= 0 {
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
		handler(line)
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
				if (r >= '@' && r <= '~') {
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
