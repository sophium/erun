package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// classifyMCPUnreachable reports which locally observable shape of
// unreachability the review panel is looking at, through the same injectable
// port-bound check the rest of erun-ui uses (so it stays testable without a
// real dial).
func (a *App) classifyMCPUnreachable(port int) mcp.LocalMCPUnreachableKind {
	if a.deps.canConnectLocalPort != nil && a.deps.canConnectLocalPort(port) {
		return mcp.LocalMCPStaleForward
	}
	return mcp.LocalMCPNotOpen
}

func (a *App) ensureMCPAvailable(ctx context.Context, result configpkg.OpenResult) error {
	mcpPort := configpkg.MCPPortForResult(result)
	// Reachability is a round trip, not a dial. A stale forward holds the port
	// and answers nothing, so gating recovery on a dial left the env
	// permanently unreachable behind a listener that looked fine.
	if a.deps.ensureMCP != nil && !a.deps.canReachMCPEndpoint(mcpPort) {
		a.emitEnvNotification("warning", result.Tenant, result.EnvConfig.Name, notificationSourceMCPUnreachable,
			mcp.DescribeLocalMCPUnreachable(result.Tenant, result.EnvConfig.Name, mcpPort), "")
		if err := a.deps.ensureMCP(ctx, result); err != nil {
			if !a.deps.canReachMCPEndpoint(mcpPort) {
				return err
			}
		}
	}
	return nil
}

// ReconnectMCP runs `erun open --no-shell --reconnect` to re-establish the
// env's MCP port-forward against the runtime that is already there. This is the
// only desktop path that opens implicitly on the user's behalf, and it is gated
// on an explicit click in the review panel's unreachable state. Output is
// streamed to the frontend so a slow open does not look frozen.
//
// It does not deploy. `--reconnect` marks the call machine-initiated, so a
// stopped environment is refused rather than started (a stop is what dropped
// the session being reattached) and `open` without `--deploy` verifies the
// runtime is already deployed instead of bringing one up. The reconnect
// dialog's copy is what has to keep saying so.
func (a *App) ReconnectMCP(selection uiSelection) error {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("reconnect MCP", selection.Tenant, selection.Environment); err != nil {
		return err
	}
	result, err := open.ResolveOpen(a.deps.store, shared.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if a.deps.reconnectMCP == nil {
		return fmt.Errorf("reconnect is not configured")
	}
	emit := func(line string) {
		a.emit(mcpReconnectLineEvent, line)
	}
	return a.deps.reconnectMCP(ctx, result, emit)
}

// reconnectRefused decides whether tryReconnect should decline to respawn,
// emitting the matching terminal marker and env status as a side effect. Each
// guard is a terminal condition — handover, stopped cloud context, deploy
// failure, fast-exit loop — where an automatic respawn would fight another
// actor or storm a broken env; the recovery affordance is named in the marker.
func (a *App) reconnectRefused(managed *managedTerminal, exitReason string) bool {
	// An orchestrator has no env selection, so every guard below that keys on
	// managed.selection would silently pass it through. It gets its own check
	// instead: refuse a clean exit (the operator quit the TUI, not a crash)
	// and refuse whenever the operator's Stop already removed this
	// orchestrator's registration, so Stop refuses its own respawn.
	if managed.kind == sessionKindOrchestrator && a.orchestratorReconnectRefused(managed, exitReason) {
		return true
	}

	// Another ERun window re-attached this persistent session — a
	// deliberate handover, not a transient drop. Respawning would run
	// `erun open` again, whose attach takes the session straight back,
	// and the two windows would steal it from each other in a loop.
	// Clicking the env in the sidebar is the deliberate take-back.
	if a.sessionTakenOver(managed) {
		a.emitTakenOverMarker(managed.serial)
		return true
	}

	// Refuse to respawn while the env's linked cloud context is not
	// running. Each respawn re-runs `erun open`, whose preflight calls
	// StartCloudContext and immediately undoes any auto-stop that has
	// just fired. Without this gate the desktop and the runtime-pod
	// monitor stop and start the EC2 instance in a tight loop, which
	// surfaces in the terminal as repeated IncorrectInstanceState
	// errors. End the loop cleanly here; the titlebar Play button is
	// the recovery affordance (same shape as the autoStart=never
	// empty state).
	if !a.shouldRespawnForCloudContext(managed) {
		a.emitStoppedContextMarker(managed.serial)
		a.emitEnvStatus(managed.selection, envStatusStopped)
		return true
	}

	// A runtime scaled to zero is a stopped environment, not a broken one.
	// It must be checked BEFORE the deploy-failure guards below, or the
	// tabs that die when the pod goes away flag the row as failed and the
	// operator's own Stop looks like an outage. Respawning is refused for
	// the same reason the cloud-context guard refuses: `erun open` wakes a
	// stopped env, so an automatic respawn would undo the stop the operator
	// just asked for. Re-clicking the row is the deliberate wake.
	if a.runtimeStoppedForSelection(managed.selection) {
		a.emitStoppedRuntimeMarker(managed.serial)
		a.emitEnvStatus(managed.selection, envStatusRuntimeStopped)
		return true
	}

	// A deploy failure is terminal, not a transient drop. Respawning
	// re-runs `erun open`, which re-deploys and fails the same way — and
	// because every env tab (ERun + AI) reconnects independently, that
	// turns one failing deploy into a parallel re-deploy storm across
	// tabs. Refuse here and leave recovery to the user (the failed-deploy
	// card's Run doctor / Rebuild & redeploy, or the titlebar Play
	// button). A session that reached a healthy shell and then dropped has
	// readyErr == nil and still reconnects below; its `erun open` finds the
	// deploy already current and skips it.
	if a.reconnectBlockedByDeployFailure(managed) {
		a.emitDeployFailedMarker(managed.serial)
		a.emitEnvStatus(managed.selection, envStatusFailed)
		return true
	}

	// The readyErr guard above only catches an open that emitted the
	// `==> Deploy failed` trace. When the deploy failed in a *separate*
	// activity and this open is just trying to reach the resulting (never
	// ready) pod, it exits with an MCP port-forward timeout / "pod not ready
	// while syncing SSH" instead — no deploy-failed readyErr — and would loop
	// here forever (each respawn re-runs `erun open`, re-times-out, and stacks
	// another queued open). Consult the activity queue: if the env's latest
	// deploy failed, refuse for the same reason and leave recovery to the user.
	if a.reconnectBlockedByActivityDeployFailure(managed) {
		a.emitDeployFailedMarker(managed.serial)
		a.emitEnvStatus(managed.selection, envStatusFailed)
		return true
	}

	// Cap consecutive fast-exit respawns. Without this, an env whose
	// underlying cluster keeps tearing down the freshly-spawned pod
	// (helm rollout timeouts, MCP port-forward races against a
	// terminating instance, etc.) loops indefinitely — each respawn
	// re-runs `erun open`, which deploys, fails again in seconds, and
	// exits. The user sees N stacked "Deploy failed after Ns" entries
	// in the activity drawer and a wall of "── reconnecting ──"
	// markers in the terminal. Stop after the cap and surface a
	// single explicit retry affordance.
	if a.trackExitForLoopGuard(managed) {
		a.emitReconnectLoopMarker(managed.serial)
		a.emitEnvStatus(managed.selection, envStatusFailed)
		return true
	}

	return false
}

// orchestratorReconnectRefused is reconnectRefused's orchestrator-specific
// guard. terminalSessionExitReason returns "" for a clean exit (Wait()
// reported no error) — the operator quitting the TUI from inside, not a
// failure — and a clean exit must never trigger the auto-resume this bound
// exists for. stopOrchestratorSession deletes both a.orchestrators[id] and
// a.sessions[key] under the same lock a Stop takes, so checking they are
// still exactly this registration is what makes Stop refuse its own respawn
// even if it raced tryReconnect's own managed.closed check.
func (a *App) orchestratorReconnectRefused(managed *managedTerminal, exitReason string) bool {
	if strings.TrimSpace(exitReason) == "" {
		return true
	}
	id, ok := orchestratorIDFromSessionKey(managed.key)
	if !ok {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.orchestrators[id] == nil || a.sessions[managed.key] != managed
}

func (a *App) tryReconnect(managed *managedTerminal, exitReason string) bool {
	a.mu.Lock()
	if managed == nil || managed.closed || managed.respawn == nil {
		a.mu.Unlock()
		return false
	}
	respawn := managed.respawn
	a.mu.Unlock()

	if a.reconnectRefused(managed, exitReason) {
		return false
	}

	a.emitReconnectMarker(managed.serial, exitReason)
	// The respawned `erun open` is a pure primitive; refresh the
	// shared thin reconnect (TTL-deduped) so a replaced pod's forwarders are
	// rebound without every tab repeating it. If the pod is gone
	// for good the reconnect surfaces a recoverable failure rather than
	// silently redeploying — deploy stays the caller's explicit action. An
	// orchestrator has no env runtime to rebind at all.
	if managed.kind != sessionKindLocal && managed.kind != sessionKindOrchestrator {
		a.ensureEnvRuntimeOnce(managed.selection)
	}
	next, err := respawn()
	if err != nil {
		a.emitReconnectFailureMarker(managed.serial, err)
		return false
	}

	a.mu.Lock()
	if managed.closed {
		a.mu.Unlock()
		_ = next.Close()
		return false
	}
	managed.session = next
	managed.awaitingPostRespawnInput = true
	// A reconnect is a fresh attach to the (possibly same) pod session, so
	// re-arm the AI repaint nudge: the new client gets a cleared screen and
	// Claude needs the geometry-change WINCH again to repaint.
	managed.repaintNudged = false
	a.mu.Unlock()
	// The respawn went through — whatever stopped/failed condition the row
	// was flagged with is being retried, so clear it (the refusal paths
	// above re-flag on the next failure). An orchestrator has no env row to
	// clear a status on.
	if managed.kind != sessionKindOrchestrator {
		a.emitEnvStatus(managed.selection, "")
	}
	return true
}

// shouldRespawnForCloudContext reports whether the managed PTY can be safely
// relaunched. Local envs (no linked cloud context) always may; a cloud env may
// only while its context is running or starting, because respawning against a
// stopped or stopping context would fight the desktop's auto-stop. A store read
// error is treated as "allow" so a transient failure does not permanently break
// reconnect.
func (a *App) shouldRespawnForCloudContext(managed *managedTerminal) bool {
	if managed == nil || a.deps.store == nil {
		return true
	}
	// Honor an explicit Stop click before consulting on-disk state. The
	// status poller writes the cloud-context status on its own cadence,
	// so a fresh Stop leaves a wide race window where the on-disk status
	// still reads running while the kubectl session is already gone.
	// isIntentionalStop closes that window; the marker is cleared on
	// successful Start so the next reconnect cycle behaves normally.
	if a.isIntentionalStop(managed.selection) {
		return false
	}
	config, _, err := a.deps.store.LoadEnvConfig(managed.selection.Tenant, managed.selection.Environment)
	if err != nil {
		return true
	}
	cloudContext, ok, err := a.linkedCloudContext(config)
	if err != nil || !ok {
		return true
	}
	switch strings.TrimSpace(cloudContext.Status) {
	case cloud.CloudContextStatusRunning, cloud.CloudContextStatusPending:
		return true
	case "":
		return true
	default:
		return false
	}
}

func (a *App) emitReconnectMarker(sessionID int, exitReason string) {
	suffix := ""
	if reason := strings.TrimSpace(exitReason); reason != "" {
		suffix = " " + reason
	}
	marker := "\r\n\x1b[2;33m── reconnecting" + suffix + " ──\x1b[0m\r\n"
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}

// reconnectLoopWindow and reconnectLoopMaxExits tune the fast-exit loop guard:
// large enough to absorb a couple of legitimate transient blips (an `erun open`
// retrying past a momentary AWS describe-instances error) without leaving the
// user staring at stacked reconnect noise.
const (
	reconnectLoopWindow     = 30 * time.Second
	reconnectLoopMaxExits   = 2
	reconnectLoopMarkerANSI = "\r\n\x1b[2;33m── stopped reconnecting after repeated failures — click the environment in the sidebar to retry ──\x1b[0m\r\n"
	deployFailedMarkerANSI  = "\r\n\x1b[2;33m── deploy failed — not retrying automatically; use Run doctor or Rebuild & redeploy on the failed deploy, or click the environment in the sidebar to retry ──\x1b[0m\r\n"
	takenOverMarkerANSI     = "\r\n\x1b[2;33m── session re-attached in another ERun window — click the environment in the sidebar to attach it here ──\x1b[0m\r\n"
)

func (a *App) trackExitForLoopGuard(managed *managedTerminal) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if managed == nil {
		return false
	}
	now := time.Now()
	cutoff := now.Add(-reconnectLoopWindow)
	kept := managed.recentExits[:0]
	for _, t := range managed.recentExits {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	managed.recentExits = kept
	return len(kept) > reconnectLoopMaxExits
}

// emitReconnectLoopMarker's dim-yellow style matches the reconnecting and
// stopped-context markers so the user reads them all as one status channel.
func (a *App) emitReconnectLoopMarker(sessionID int) {
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(reconnectLoopMarkerANSI)),
	})
}

func (a *App) emitDeployFailedMarker(sessionID int) {
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(deployFailedMarkerANSI)),
	})
}

func (a *App) emitTakenOverMarker(sessionID int) {
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(takenOverMarkerANSI)),
	})
}

func (a *App) markSessionTakenOver(managed *managedTerminal) {
	if managed == nil {
		return
	}
	a.mu.Lock()
	managed.takenOver = true
	a.mu.Unlock()
}

func (a *App) sessionTakenOver(managed *managedTerminal) bool {
	if managed == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return managed.takenOver
}

// reconnectBlockedByDeployFailure reports whether this open ended in a deploy
// failure (signalReady got the `==> Deploy failed` readyErr) rather than a
// healthy ready that later dropped — the latter still reconnects.
func (a *App) reconnectBlockedByDeployFailure(managed *managedTerminal) bool {
	if managed == nil {
		return false
	}
	managed.readyMu.Lock()
	defer managed.readyMu.Unlock()
	return managed.readyClosed && managed.readyErr != nil
}

// reconnectBlockedByActivityDeployFailure reports whether the env's latest
// deploy failed in a separate activity — the case reconnectBlockedByDeployFailure
// misses because this open has no deploy-failed readyErr of its own.
func (a *App) reconnectBlockedByActivityDeployFailure(managed *managedTerminal) bool {
	if managed == nil || a.activityQueue == nil {
		return false
	}
	return a.activityQueue.latestDeployFailed(managed.selection.Tenant, managed.selection.Environment)
}

// emitStoppedContextMarker's dim-yellow style matches the reconnecting marker
// so the user reads them as one status channel; naming the recovery (titlebar
// Play button) in the line itself means no separate UI element is needed.
func (a *App) emitStoppedContextMarker(sessionID int) {
	marker := "\r\n\x1b[2;33m── environment stopped — click the start button in the titlebar to resume ──\x1b[0m\r\n"
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}

// emitStoppedRuntimeMarker names the other recovery: a runtime scaled to zero
// is woken by opening the environment, not by the titlebar's cloud-context
// start button.
func (a *App) emitStoppedRuntimeMarker(sessionID int) {
	marker := "\r\n\x1b[2;33m── environment stopped — click it in the sidebar to start it again ──\x1b[0m\r\n"
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}

func (a *App) emitReconnectFailureMarker(sessionID int, err error) {
	if err == nil {
		return
	}
	marker := "\r\n\x1b[31m── reconnect failed: " + err.Error() + " ──\x1b[0m\r\n"
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}
