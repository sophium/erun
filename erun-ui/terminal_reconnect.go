package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// classifyMCPUnreachable reports the locally observable kind of unreachability.
func (a *App) classifyMCPUnreachable(port int) mcp.LocalMCPUnreachableKind {
	if a.deps.canConnectLocalPort != nil && a.deps.canConnectLocalPort(port) {
		return mcp.LocalMCPStaleForward
	}
	return mcp.LocalMCPNotOpen
}

func (a *App) ensureMCPAvailable(ctx context.Context, result configpkg.OpenResult) error {
	mcpPort := configpkg.MCPPortForResult(result)
	// A stale forward holds the port but answers nothing, so test a round trip.
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

// ReconnectMCP runs `erun open --no-shell --reconnect` on an explicit click. It
// never deploys or starts a stopped environment.
func (a *App) ReconnectMCP(selection uimodel.Selection) error {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("reconnect MCP", selection.Tenant, selection.Environment); err != nil {
		return err
	}
	result, err := resolve.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return err
	}
	ctx := a.context()
	if a.deps.reconnectMCP == nil {
		return fmt.Errorf("reconnect is not configured")
	}
	emit := func(line string) {
		a.emit(uimodel.MCPReconnectLineEvent, line)
	}
	return a.deps.reconnectMCP(ctx, result, emit)
}

// reconnectRefused reports whether tryReconnect must not respawn, emitting the
// marker that names the recovery.
func (a *App) reconnectRefused(managed *managedTerminal, exitReason string) bool {
	// Orchestrators have no env selection, so the guards below would pass them.
	if managed.kind == sessionKindOrchestrator && a.orchestratorReconnectRefused(managed, exitReason) {
		return true
	}

	// Another window took the session over; respawning would steal it back in a
	// loop.
	if a.sessionTakenOver(managed) {
		a.emitTakenOverMarker(managed.serial)
		return true
	}

	// `erun open` restarts the cloud context, so respawning would fight auto-stop.
	if !a.shouldRespawnForCloudContext(managed) {
		a.emitStoppedContextMarker(managed.serial)
		a.emitEnvStatus(managed.selection, uimodel.EnvStatusStopped)
		return true
	}

	// Checked before the deploy-failure guards: a stopped runtime is not a failure,
	// and respawning would undo the operator's stop.
	if a.envops.RuntimeStopped(managed.selection) {
		a.emitStoppedRuntimeMarker(managed.serial)
		a.emitEnvStatus(managed.selection, uimodel.EnvStatusRuntimeStopped)
		return true
	}

	// Respawning after a failed deploy redeploys it from every tab at once.
	if a.reconnectBlockedByDeployFailure(managed) {
		a.emitDeployFailedMarker(managed.serial)
		a.emitEnvStatus(managed.selection, uimodel.EnvStatusFailed)
		return true
	}

	// Catches a deploy that failed in a separate activity, which this open sees only
	// as a timeout.
	if a.reconnectBlockedByActivityDeployFailure(managed) {
		a.emitDeployFailedMarker(managed.serial)
		a.emitEnvStatus(managed.selection, uimodel.EnvStatusFailed)
		return true
	}

	// Cap fast-exit respawns so a crash-looping env stops with one retry affordance.
	if a.trackExitForLoopGuard(managed) {
		a.emitReconnectLoopMarker(managed.serial)
		a.emitEnvStatus(managed.selection, uimodel.EnvStatusFailed)
		return true
	}

	return false
}

// orchestratorReconnectRefused refuses a clean exit, and a session Stop already
// deregistered even if it raced managed.closed.
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
	// Refresh the shared, TTL-deduped reconnect so forwarders rebind to a replaced
	// pod; it never redeploys.
	if managed.kind != sessionKindLocal && managed.kind != sessionKindOrchestrator {
		a.envops.EnsureRuntimeOnce(managed.selection)
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
	// A fresh attach needs the repaint nudge again.
	managed.repaintNudged = false
	a.mu.Unlock()
	// Clear the row's stopped/failed flag; refusals re-flag on the next failure.
	if managed.kind != sessionKindOrchestrator {
		a.emitEnvStatus(managed.selection, "")
	}
	return true
}

// shouldRespawnForCloudContext allows a cloud env only while its context runs or
// starts; a store read error allows.
func (a *App) shouldRespawnForCloudContext(managed *managedTerminal) bool {
	if managed == nil || a.deps.store == nil {
		return true
	}
	// The status poller lags an explicit Stop.
	if a.isIntentionalStop(managed.selection) {
		return false
	}
	config, _, err := a.deps.store.LoadEnvConfig(managed.selection.Tenant, managed.selection.Environment)
	if err != nil {
		return true
	}
	cloudContext, ok, err := a.settings.LinkedCloudContext(config)
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
	a.emitEvent(uimodel.TerminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}

// reconnectLoopWindow and reconnectLoopMaxExits absorb a couple of transient
// blips.
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

func (a *App) emitReconnectLoopMarker(sessionID int) {
	a.emitEvent(uimodel.TerminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(reconnectLoopMarkerANSI)),
	})
}

func (a *App) emitDeployFailedMarker(sessionID int) {
	a.emitEvent(uimodel.TerminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(deployFailedMarkerANSI)),
	})
}

func (a *App) emitTakenOverMarker(sessionID int) {
	a.emitEvent(uimodel.TerminalOutputEvent, terminalOutputPayload{
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

// reconnectBlockedByDeployFailure: a healthy ready that later dropped still
// reconnects.
func (a *App) reconnectBlockedByDeployFailure(managed *managedTerminal) bool {
	if managed == nil {
		return false
	}
	managed.readyMu.Lock()
	defer managed.readyMu.Unlock()
	return managed.readyClosed && managed.readyErr != nil
}

// reconnectBlockedByActivityDeployFailure covers deploys that failed in a
// separate activity.
func (a *App) reconnectBlockedByActivityDeployFailure(managed *managedTerminal) bool {
	if managed == nil || a.activityQueue == nil {
		return false
	}
	return a.activityQueue.latestDeployFailed(managed.selection.Tenant, managed.selection.Environment)
}

func (a *App) emitStoppedContextMarker(sessionID int) {
	marker := "\r\n\x1b[2;33m── environment stopped — click the start button in the titlebar to resume ──\x1b[0m\r\n"
	a.emitEvent(uimodel.TerminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}

// emitStoppedRuntimeMarker: a scaled-to-zero runtime wakes on open, not via the
// cloud-context start button.
func (a *App) emitStoppedRuntimeMarker(sessionID int) {
	marker := "\r\n\x1b[2;33m── environment stopped — click it in the sidebar to start it again ──\x1b[0m\r\n"
	a.emitEvent(uimodel.TerminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}

func (a *App) emitReconnectFailureMarker(sessionID int, err error) {
	if err == nil {
		return
	}
	marker := "\r\n\x1b[31m── reconnect failed: " + err.Error() + " ──\x1b[0m\r\n"
	a.emitEvent(uimodel.TerminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}
