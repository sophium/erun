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
	// deployQueueStateEvent is emitted whenever a deploy entry changes state.
	// Frontend subscribes to this to refresh the queue drawer and per-card
	// container statuses without polling.
	deployQueueStateEvent = "deploy:state"

	// deployQueueLockEvent is emitted whenever a terminal session's locked
	// status changes. Frontend uses it to render or hide the lock overlay
	// on the affected terminal.
	deployQueueLockEvent = "deploy:lock"

	// deployQueuePollInterval governs how often the desktop polls kubectl
	// for container statuses while a deploy is running. Short enough that
	// the user sees pods transition; long enough that polling load is
	// negligible against modest cluster sizes.
	deployQueuePollInterval = 2 * time.Second
)

// deployStateEvent is the payload shape pushed to the frontend on each
// transition. Wails serializes the embedded entry directly; the type alias
// keeps the contract documented and stable.
type deployStateEvent = deployQueueEntry

// deployLockEvent describes a terminal lock transition driven by the deploy
// queue. The frontend keys overlays by SessionID.
type deployLockEvent struct {
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
func (a *App) ListDeploys() []deployQueueEntry {
	if a.deployQueue == nil {
		return nil
	}
	return a.deployQueue.list()
}

// GetDeploy returns a single deploy entry by ID, or zero value if not found.
// Wails-exported.
func (a *App) GetDeploy(id string) deployQueueEntry {
	if a.deployQueue == nil {
		return deployQueueEntry{}
	}
	for _, entry := range a.deployQueue.list() {
		if entry.ID == id {
			return entry
		}
	}
	return deployQueueEntry{}
}

// DismissDeploy removes a finished deploy from history. Returns false when
// the ID names an active deploy (which the user must wait for) or no longer
// exists. Wails-exported.
func (a *App) DismissDeploy(id string) bool {
	if a.deployQueue == nil {
		return false
	}
	return a.deployQueue.dismiss(id)
}

// FindActiveDeployForSelection returns the active entry for (tenant, env)
// when one exists; the frontend uses this for deploy-button gating without
// having to walk the full ListDeploys snapshot. Wails-exported.
func (a *App) FindActiveDeployForSelection(selection uiSelection) deployQueueEntry {
	if a.deployQueue == nil {
		return deployQueueEntry{}
	}
	selection = normalizeSelection(selection)
	if entry, ok := a.deployQueue.findActive(selection.Tenant, selection.Environment); ok {
		return entry
	}
	return deployQueueEntry{}
}

// startDeployTracking registers a new active deploy in the queue and spawns
// the per-deploy goroutines (PTY tail + kubectl status poller). Returns the
// registered entry plus a `joined` flag: when joined is true, the caller's
// new invocation collapsed into an existing in-flight deploy and no extra
// helm upgrade was queued.
func (a *App) startDeployTracking(selection uiSelection, sessionID int) (deployQueueEntry, bool) {
	if a.deployQueue == nil {
		return deployQueueEntry{}, false
	}
	tenant := strings.TrimSpace(selection.Tenant)
	environment := strings.TrimSpace(selection.Environment)
	version := strings.TrimSpace(selection.Version)
	release := releaseNameForTenant(tenant)
	namespace := namespaceForTenantEnv(tenant, environment)
	kubeContext := strings.TrimSpace(selection.KubernetesContext)
	entry, fresh := a.deployQueue.start(deployQueueEntry{
		Tenant:            tenant,
		Environment:       environment,
		Version:           version,
		Release:           release,
		Namespace:         namespace,
		KubernetesContext: kubeContext,
	})
	if !fresh {
		// Already-active duplicate: just signal terminal-lock for the
		// session that triggered this invocation so the user sees the
		// existing deploy's wait UI.
		a.lockTerminalForDeploy(sessionID, entry)
		return entry, true
	}
	a.lockTerminalsForDeploy(entry)
	if a.deployStatusPoller != nil {
		a.deployStatusPoller(entry)
	}
	return entry, false
}

// finishDeployTracking moves the active deploy for this selection to a
// terminal status. Idempotent: a no-op when no active entry exists for the
// selection.
func (a *App) finishDeployTracking(selection uiSelection, status deployQueueStatus, errMsg string) {
	if a.deployQueue == nil {
		return
	}
	entry, ok := a.deployQueue.findActive(strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment))
	if !ok {
		return
	}
	if final, ok := a.deployQueue.finish(entry.ID, status, errMsg); ok {
		a.unlockTerminalsForDeploy(final)
	}
}

// emitDeployState is the persist/notify hook the store uses to tell the
// frontend a deploy entry changed.
func (a *App) emitDeployState(entry deployQueueEntry) {
	a.emitEvent(deployQueueStateEvent, entry)
}

// lockTerminalsForDeploy marks every terminal session belonging to the
// deploy's selection as locked and emits a lock event the frontend renders
// as an overlay. Sessions joined later for the same selection will be locked
// by lockTerminalForDeploy on a per-session basis.
func (a *App) lockTerminalsForDeploy(entry deployQueueEntry) {
	if entry.ID == "" {
		return
	}
	target := deployTargetForRuntime(entry)
	a.mu.Lock()
	var events []deployLockEvent
	for _, managed := range a.sessions {
		if managed == nil || managed.closed {
			continue
		}
		if !sessionMatchesSelection(managed, entry) {
			continue
		}
		if managed.lockedByDeploy == entry.ID {
			continue
		}
		managed.lockedByDeploy = entry.ID
		events = append(events, deployLockEvent{
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
		a.emitEvent(deployQueueLockEvent, ev)
	}
}

// lockTerminalForDeploy locks a single session by its serial ID. Used when
// the user joins an in-flight deploy from a session that was created after
// the deploy started.
func (a *App) lockTerminalForDeploy(sessionID int, entry deployQueueEntry) {
	if entry.ID == "" || sessionID <= 0 {
		return
	}
	target := deployTargetForRuntime(entry)
	a.mu.Lock()
	var event *deployLockEvent
	for _, managed := range a.sessions {
		if managed == nil || managed.closed || managed.serial != sessionID {
			continue
		}
		if managed.lockedByDeploy != entry.ID {
			managed.lockedByDeploy = entry.ID
			event = &deployLockEvent{
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
		a.emitEvent(deployQueueLockEvent, *event)
	}
}

// unlockTerminalsForDeploy clears the lock on every session whose
// lockedByDeploy matches the supplied entry. Idempotent.
func (a *App) unlockTerminalsForDeploy(entry deployQueueEntry) {
	if entry.ID == "" {
		return
	}
	a.mu.Lock()
	var events []deployLockEvent
	for _, managed := range a.sessions {
		if managed == nil {
			continue
		}
		if managed.lockedByDeploy != entry.ID {
			continue
		}
		managed.lockedByDeploy = ""
		events = append(events, deployLockEvent{
			SessionID:   managed.serial,
			Tenant:      entry.Tenant,
			Environment: entry.Environment,
			Locked:      false,
			DeployID:    entry.ID,
		})
	}
	a.mu.Unlock()
	for _, ev := range events {
		a.emitEvent(deployQueueLockEvent, ev)
	}
}

// sessionMatchesSelection reports whether a managed terminal targets the same
// (tenant, environment) as the deploy entry. Local-tab sessions are not
// locked because they are the place the user kicked off the deploy from and
// need to remain interactive to read trace output / acknowledge prompts.
func sessionMatchesSelection(managed *managedTerminal, entry deployQueueEntry) bool {
	if managed == nil {
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

// deployingLineRe matches the "==> Deploying tenant/env [version]" trace
// emitted by RunHelmDeploy. Capture groups: tenant, environment, optional
// trailing version. Used by the trace handler to auto-register a deploy
// entry when the user runs `erun deploy` directly in any tab (not just via
// the desktop's Deploy button).
var deployingLineRe = regexp.MustCompile(`^==> Deploying ([^/\s]+)/([^/\s]+)(?:\s+(\S+))?\s*$`)

// newDeployTraceLineHandler scans PTY output for the trace lines emitted by
// erun deploy and feeds lifecycle transitions back into the deploy queue.
// The handler operates on the source-of-truth printed text rather than on
// any in-band signal from the desktop button, so it works regardless of
// which tab the user kicked the deploy off in (Local from the Deploy
// button, ERun from a manual `erun deploy`, AI from claude inside the pod).
//
// The selection argument is the tab's resolved tenant/env. It is used as the
// fallback (tenant, env) when the trace itself doesn't carry one — for
// example a `==> Deploy failed` line without a preceding `==> Deploying`
// (which happens when the deploy aborts before the spec resolves).
func newDeployTraceLineHandler(app *App, selection uiSelection) func(string) {
	deployedRe := regexp.MustCompile(`^==> Deployed `)
	failedRe := regexp.MustCompile(`^==> Deploy failed`)
	skippedRe := regexp.MustCompile(`^==> Skipping `)
	errorRe := regexp.MustCompile(`(?i)^Error: `)
	return func(line string) {
		line = strings.TrimSpace(line)
		if match := deployingLineRe.FindStringSubmatch(line); match != nil {
			app.startDeployTrackingFromTrace(selection, match[1], match[2], match[3])
			return
		}
		switch {
		case deployedRe.MatchString(line):
			app.finishDeployTracking(selection, deployQueueStatusSucceeded, "")
		case skippedRe.MatchString(line):
			app.finishDeployTracking(selection, deployQueueStatusSkipped, line)
		case failedRe.MatchString(line):
			app.finishDeployTracking(selection, deployQueueStatusFailed, line)
		case errorRe.MatchString(line):
			// Surface the most recent error line as the failure reason if
			// the deploy is still active. We don't transition state on
			// error alone — ==> Deploy failed is the canonical terminal
			// signal — but capturing the error message gives the frontend
			// a useful hint.
			app.captureDeployErrorIfRunning(selection, line)
		}
	}
}

// startDeployTrackingFromTrace registers a deploy in the queue based on a
// trace line we observed in any tab's PTY. If an active entry already
// exists for (tenant, environment) (e.g. because the desktop's Deploy
// button already registered it), this is a no-op. Otherwise a new entry
// is started with the parsed fields. The selection's KubernetesContext is
// used when available so the pod-status poller has somewhere to query.
func (a *App) startDeployTrackingFromTrace(selection uiSelection, tenant, environment, version string) {
	if a.deployQueue == nil {
		return
	}
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	version = strings.TrimSpace(version)
	if tenant == "" || environment == "" {
		return
	}
	if _, ok := a.deployQueue.findActive(tenant, environment); ok {
		return
	}
	entry, fresh := a.deployQueue.start(deployQueueEntry{
		Tenant:            tenant,
		Environment:       environment,
		Version:           version,
		Release:           releaseNameForTenant(tenant),
		Namespace:         namespaceForTenantEnv(tenant, environment),
		KubernetesContext: strings.TrimSpace(selection.KubernetesContext),
	})
	if !fresh {
		return
	}
	a.lockTerminalsForDeploy(entry)
	if a.deployStatusPoller != nil {
		a.deployStatusPoller(entry)
	}
}

func (a *App) captureDeployErrorIfRunning(selection uiSelection, line string) {
	if a.deployQueue == nil {
		return
	}
	entry, ok := a.deployQueue.findActive(strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment))
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
	a.deployQueue.updateContainers(entry.ID, entry.Containers)
}

// pollDeployContainerStatuses runs an ad-hoc kubectl poll loop while the
// supplied deploy entry is active. Each tick parses pod JSON for the helm
// release's pods and pushes a snapshot into the queue. The loop exits when
// the entry is no longer active in the store or ctx is cancelled.
func (a *App) pollDeployContainerStatuses(ctx context.Context, entry deployQueueEntry) {
	if a.deployQueue == nil {
		return
	}
	ticker := time.NewTicker(deployQueuePollInterval)
	defer ticker.Stop()
	pollOnce := func() bool {
		if _, ok := a.deployQueue.findActive(entry.Tenant, entry.Environment); !ok {
			return false
		}
		statuses, err := a.fetchDeployContainerStatuses(ctx, entry)
		if err != nil {
			return true
		}
		a.deployQueue.updateContainers(entry.ID, statuses)
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

// fetchDeployContainerStatuses runs `kubectl get pods -l app=<release> -o json`
// and parses the result into the queue's container shape. Errors are
// returned to the caller so transient kubectl outages don't blank the
// previous snapshot — the frontend keeps showing the last-known state until
// the next successful poll.
func (a *App) fetchDeployContainerStatuses(ctx context.Context, entry deployQueueEntry) ([]deployQueueContainerStatus, error) {
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
	return parseDeployContainerStatuses(out)
}

func parseDeployContainerStatuses(raw []byte) ([]deployQueueContainerStatus, error) {
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
	out := make([]deployQueueContainerStatus, 0)
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
			status := deployQueueContainerStatus{
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

// deployTargetForRuntime returns the human-readable "tenant/env [version]"
// string used in info lines and lock-overlay messages.
func deployTargetForRuntime(entry deployQueueEntry) string {
	target := strings.TrimSpace(entry.Tenant) + "/" + strings.TrimSpace(entry.Environment)
	if version := strings.TrimSpace(entry.Version); version != "" {
		target += " " + version
	}
	return target
}

// feedDeployTraceFromTerminal accumulates PTY output for any tab that hosts
// a running erun process (Local from the Deploy button, ERun from a manual
// `erun deploy`, AI from claude calling deploy inside the pod), splits on
// newlines, and dispatches each complete line through the deploy trace
// handler. The trace handler is the source-of-truth for deploy lifecycle:
// it auto-registers an entry on `==> Deploying` and finishes it on
// Deployed/failed/Skipping. Selections without a tenant/env are ignored.
func (a *App) feedDeployTraceFromTerminal(managed *managedTerminal, chunk []byte) {
	if managed == nil || a.deployQueue == nil {
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
	managed.deployTraceBuffer += stripDeployTraceANSI(string(chunk))
	lines := []string{}
	for {
		idx := strings.IndexByte(managed.deployTraceBuffer, '\n')
		if idx < 0 {
			break
		}
		line := managed.deployTraceBuffer[:idx]
		if r := strings.IndexByte(line, '\r'); r >= 0 {
			line = line[r+1:]
		}
		lines = append(lines, line)
		managed.deployTraceBuffer = managed.deployTraceBuffer[idx+1:]
	}
	a.mu.Unlock()
	if len(lines) == 0 {
		return
	}
	handler := newDeployTraceLineHandler(a, managed.selection)
	for _, line := range lines {
		handler(line)
	}
}

// stripDeployTraceANSI removes the most common ANSI control sequences erun
// emits via spinner/colors so trace-line matching survives terminal noise.
// The patterns are intentionally narrow (CSI-style escape sequences) to
// avoid clobbering meaningful output.
func stripDeployTraceANSI(s string) string {
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
