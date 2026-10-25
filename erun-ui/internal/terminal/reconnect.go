package terminal

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// ClassifyMCPUnreachable reports the locally observable kind of unreachability.
func (s *Service) ClassifyMCPUnreachable(port int) mcp.LocalMCPUnreachableKind {
	if s.d.CanConnectLocalPort(port) {
		return mcp.LocalMCPStaleForward
	}
	return mcp.LocalMCPNotOpen
}

func (s *Service) EnsureMCPAvailable(ctx context.Context, result configpkg.OpenResult) error {
	mcpPort := configpkg.MCPPortForResult(result)
	// A stale forward holds the port but answers nothing, so test a round trip.
	if !s.d.CanReachMCPEndpoint(mcpPort) {
		s.emitEnvNotification("warning", result.Tenant, result.EnvConfig.Name, SourceMCPUnreachable,
			mcp.DescribeLocalMCPUnreachable(result.Tenant, result.EnvConfig.Name, mcpPort), "")
		if err := s.d.EnsureMCP(ctx, result); err != nil {
			if !s.d.CanReachMCPEndpoint(mcpPort) {
				return err
			}
		}
	}
	return nil
}

// ReconnectMCP runs `erun open --no-shell --reconnect` on an explicit click. It
// never deploys or starts a stopped environment.
func (s *Service) ReconnectMCP(selection uimodel.Selection) error {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("reconnect MCP", selection.Tenant, selection.Environment); err != nil {
		return err
	}
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return err
	}
	ctx := s.d.Context()
	emit := func(line string) {
		s.d.Emit(uimodel.MCPReconnectLineEvent, line)
	}
	return s.d.ReconnectMCP(ctx, result, emit)
}

// reconnectRefused reports whether tryReconnect must not respawn, emitting the
// marker that names the recovery.
func (s *Service) reconnectRefused(managed *Session, exitReason string) bool {
	// Orchestrators have no env selection, so the guards below would pass them.
	if managed.Kind == KindOrchestrator && s.orchestratorReconnectRefused(managed, exitReason) {
		return true
	}

	// Another window took the session over; respawning would steal it back in a
	// loop.
	if s.TakenOver(managed) {
		s.emitTakenOverMarker(managed.Serial)
		return true
	}

	// `erun open` restarts the cloud context, so respawning would fight auto-stop.
	if !s.shouldRespawnForCloudContext(managed) {
		s.emitStoppedContextMarker(managed.Serial)
		s.emitEnvStatus(managed.Selection, uimodel.EnvStatusStopped)
		return true
	}

	// Checked before the deploy-failure guards: a stopped runtime is not a failure,
	// and respawning would undo the operator's stop.
	if s.d.RuntimeStopped(managed.Selection) {
		s.emitStoppedRuntimeMarker(managed.Serial)
		s.emitEnvStatus(managed.Selection, uimodel.EnvStatusRuntimeStopped)
		return true
	}

	// Respawning after a failed deploy redeploys it from every tab at once.
	if s.reconnectBlockedByDeployFailure(managed) {
		s.emitDeployFailedMarker(managed.Serial)
		s.emitEnvStatus(managed.Selection, uimodel.EnvStatusFailed)
		return true
	}

	// Catches a deploy that failed in a separate activity, which this open sees only
	// as a timeout.
	if s.reconnectBlockedByActivityDeployFailure(managed) {
		s.emitDeployFailedMarker(managed.Serial)
		s.emitEnvStatus(managed.Selection, uimodel.EnvStatusFailed)
		return true
	}

	// Cap fast-exit respawns so a crash-looping env stops with one retry affordance.
	if s.trackExitForLoopGuard(managed) {
		s.emitReconnectLoopMarker(managed.Serial)
		s.emitEnvStatus(managed.Selection, uimodel.EnvStatusFailed)
		return true
	}

	return false
}

// orchestratorReconnectRefused refuses a clean exit, and a session Stop already
// deregistered even if it raced managed.closed.
func (s *Service) orchestratorReconnectRefused(managed *Session, exitReason string) bool {
	if strings.TrimSpace(exitReason) == "" {
		return true
	}
	id, ok := OrchestratorIDFromKey(managed.Key)
	if !ok {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.d.OrchestratorRegisteredLocked(id) || s.sessions[managed.Key] != managed
}

func (s *Service) tryReconnect(managed *Session, exitReason string) bool {
	s.mu.Lock()
	if managed == nil || managed.Closed || managed.Respawn == nil {
		s.mu.Unlock()
		return false
	}
	respawn := managed.Respawn
	s.mu.Unlock()

	if s.reconnectRefused(managed, exitReason) {
		return false
	}

	s.emitReconnectMarker(managed.Serial, exitReason)
	// Refresh the shared, TTL-deduped reconnect so forwarders rebind to a replaced
	// pod; it never redeploys.
	if managed.Kind != KindLocal && managed.Kind != KindOrchestrator {
		s.d.EnsureRuntimeOnce(managed.Selection)
	}
	next, err := respawn()
	if err != nil {
		s.emitReconnectFailureMarker(managed.Serial, err)
		return false
	}

	s.mu.Lock()
	if managed.Closed {
		s.mu.Unlock()
		_ = next.Close()
		return false
	}
	managed.PTY = next
	managed.awaitingPostRespawnInput = true
	// A fresh attach needs the repaint nudge again.
	managed.repaintNudged = false
	s.mu.Unlock()
	// Clear the row's stopped/failed flag; refusals re-flag on the next failure.
	if managed.Kind != KindOrchestrator {
		s.emitEnvStatus(managed.Selection, "")
	}
	return true
}

// shouldRespawnForCloudContext allows a cloud env only while its context runs or
// starts; a store read error allows.
func (s *Service) shouldRespawnForCloudContext(managed *Session) bool {
	if managed == nil {
		return true
	}
	// The status poller lags an explicit Stop.
	if s.IsIntentionalStop(managed.Selection) {
		return false
	}
	config, _, err := s.d.Store.LoadEnvConfig(managed.Selection.Tenant, managed.Selection.Environment)
	if err != nil {
		return true
	}
	cloudContext, ok, err := s.d.LinkedCloudContext(config)
	if err != nil || !ok {
		return true
	}
	switch strings.TrimSpace(cloudContext.Status) {
	case cloudcontext.CloudContextStatusRunning, cloudcontext.CloudContextStatusPending:
		return true
	case "":
		return true
	default:
		return false
	}
}

func (s *Service) emitReconnectMarker(sessionID int, exitReason string) {
	suffix := ""
	if reason := strings.TrimSpace(exitReason); reason != "" {
		suffix = " " + reason
	}
	marker := "\r\n\x1b[2;33m── reconnecting" + suffix + " ──\x1b[0m\r\n"
	s.emitEvent(uimodel.TerminalOutputEvent, uimodel.TerminalOutputPayload{
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

func (s *Service) trackExitForLoopGuard(managed *Session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
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

func (s *Service) emitReconnectLoopMarker(sessionID int) {
	s.emitEvent(uimodel.TerminalOutputEvent, uimodel.TerminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(reconnectLoopMarkerANSI)),
	})
}

func (s *Service) emitDeployFailedMarker(sessionID int) {
	s.emitEvent(uimodel.TerminalOutputEvent, uimodel.TerminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(deployFailedMarkerANSI)),
	})
}

func (s *Service) emitTakenOverMarker(sessionID int) {
	s.emitEvent(uimodel.TerminalOutputEvent, uimodel.TerminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(takenOverMarkerANSI)),
	})
}

func (s *Service) MarkTakenOver(managed *Session) {
	if managed == nil {
		return
	}
	s.mu.Lock()
	managed.takenOver = true
	s.mu.Unlock()
}

func (s *Service) TakenOver(managed *Session) bool {
	if managed == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return managed.takenOver
}

// reconnectBlockedByDeployFailure: a healthy ready that later dropped still
// reconnects.
func (s *Service) reconnectBlockedByDeployFailure(managed *Session) bool {
	if managed == nil {
		return false
	}
	managed.readyMu.Lock()
	defer managed.readyMu.Unlock()
	return managed.readyClosed && managed.readyErr != nil
}

// reconnectBlockedByActivityDeployFailure covers deploys that failed in a
// separate activity.
func (s *Service) reconnectBlockedByActivityDeployFailure(managed *Session) bool {
	if managed == nil {
		return false
	}
	return s.d.LatestDeployFailed(managed.Selection.Tenant, managed.Selection.Environment)
}

func (s *Service) emitStoppedContextMarker(sessionID int) {
	marker := "\r\n\x1b[2;33m── environment stopped — click the start button in the titlebar to resume ──\x1b[0m\r\n"
	s.emitEvent(uimodel.TerminalOutputEvent, uimodel.TerminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}

// emitStoppedRuntimeMarker: a scaled-to-zero runtime wakes on open, not via the
// cloud-context start button.
func (s *Service) emitStoppedRuntimeMarker(sessionID int) {
	marker := "\r\n\x1b[2;33m── environment stopped — click it in the sidebar to start it again ──\x1b[0m\r\n"
	s.emitEvent(uimodel.TerminalOutputEvent, uimodel.TerminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}

func (s *Service) emitReconnectFailureMarker(sessionID int, err error) {
	if err == nil {
		return
	}
	marker := "\r\n\x1b[31m── reconnect failed: " + err.Error() + " ──\x1b[0m\r\n"
	s.emitEvent(uimodel.TerminalOutputEvent, uimodel.TerminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}
