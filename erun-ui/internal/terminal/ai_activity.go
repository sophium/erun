package terminal

import (
	"strings"
	"sync"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/open/ailaunch"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// EndAISessions ends the env's AI sessions, desktop and pod side, so the next
// launch picks up changed Claude flags: `dtach -A` would otherwise reattach to
// the stale process. Returns false without ending anything when the AI tool
// launches verbatim, since the managed flags cannot affect it.
func (s *Service) EndAISessions(selection uimodel.Selection) (bool, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("end AI sessions", selection.Tenant, selection.Environment); err != nil {
		return false, err
	}
	if envConfig, _, err := s.d.Store.LoadEnvConfig(selection.Tenant, selection.Environment); err == nil {
		// An unreadable root config is treated as no gateway configured.
		var gateway *configpkg.OpenRouterConfig
		if config, _, err := s.d.Store.LoadERunConfig(); err == nil {
			gateway = config.OpenRouter
		}
		launch := ailaunch.AISessionLaunchCommand(envConfig.AITool, envConfig.Claude, gateway, selection.Tenant, selection.Environment)
		if launch == strings.TrimSpace(envConfig.AITool) {
			return false, nil
		}
	}
	var targets []*Session
	s.mu.Lock()
	for _, managed := range s.sessions {
		if !managedAITabFor(managed, selection) {
			continue
		}
		// Marked closed so the frontend respawn creates a fresh session instead of
		// reattaching to this one.
		markClosedByTeardownLocked(managed)
		s.releaseIdleBlockLocked(managed)
		targets = append(targets, managed)
	}
	s.mu.Unlock()
	for _, managed := range targets {
		s.withdrawExitOwnership(managed, managed.PTY.Close())
	}
	// End pod sessions only after the desktop PTYs are gone, or the attached client
	// exits with the taken-over code and freezes the tab.
	var wg sync.WaitGroup
	for _, sessionID := range []string{"ai", "contribute-ai"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			s.endRemoteAppSession(selection, id)
		}(sessionID)
	}
	wg.Wait()
	return true, nil
}

func managedAITabFor(managed *Session, selection uimodel.Selection) bool {
	if managed == nil || managed.Closed || managed.PTY == nil {
		return false
	}
	if managed.Selection.Tenant != selection.Tenant ||
		managed.Selection.Environment != selection.Environment {
		return false
	}
	return managed.Kind == KindAI || managed.Kind == KindContributeAI
}

// aiActivitySustainedThreshold suppresses single-line responses.
const aiActivitySustainedThreshold = 5 * time.Second

// aiActivityIdleThreshold swallows gaps between spinner bursts.
const aiActivityIdleThreshold = 3 * time.Second

// aiActivityKind reports whether a session kind drives the sidebar's volume-based
// working spinner. Orchestrators are excluded: their TUI repaints forever, so
// silence never arrives; they report turn boundaries instead.
// It takes the kind so each caller's own nil guard stays visible to static analysis.
func aiActivityKind(kind Kind) bool {
	return kind == KindAI
}

func (s *Service) recordAIActivity(managed *Session) {
	if managed == nil || !aiActivityKind(managed.Kind) {
		return
	}
	now := time.Now()
	s.mu.Lock()
	if managed.Closed {
		s.mu.Unlock()
		return
	}
	// Where the tool reports its own state it owns both latch and release; prompt
	// repaints would otherwise make the badge oscillate.
	if _, reported := s.aiSessionEvidenceForTabLocked(managed); reported {
		s.mu.Unlock()
		return
	}
	if managed.aiActiveSince.IsZero() || now.Sub(managed.aiLastOutput) > aiActivityIdleThreshold {
		managed.aiActiveSince = now
	}
	managed.aiLastOutput = now
	shouldFireBusy := !managed.aiBusyEmitted && now.Sub(managed.aiActiveSince) >= aiActivitySustainedThreshold
	if shouldFireBusy {
		managed.aiBusyEmitted = true
	}
	s.scheduleAIQuietCheckLocked(managed)
	selection := managed.Selection
	serial := managed.Serial
	s.mu.Unlock()
	if shouldFireBusy {
		s.EmitAIActivity(serial, selection, true)
	}
}

// scheduleAIQuietCheckLocked re-arms the quiet timer on every read; that is the
// debounce. Caller holds s.mu.
func (s *Service) scheduleAIQuietCheckLocked(managed *Session) {
	if managed.aiInactivityTimer != nil {
		managed.aiInactivityTimer.Stop()
	}
	managed.aiInactivityTimer = time.AfterFunc(aiActivityIdleThreshold, func() {
		s.clearAIActivityIfQuiet(managed)
	})
}

// clearAIActivityIfQuiet is a no-op when newer output re-armed the timer. A
// session the pod still sees running keeps the latch, since silence does not
// mean finished.
func (s *Service) clearAIActivityIfQuiet(managed *Session) {
	if managed == nil {
		return
	}
	// Only the tool's report separates finished-and-waiting from still-working.
	if busy, reported := s.aiSessionEvidenceForTab(managed); reported {
		if busy {
			return
		}
		s.releaseAIActivity(managed)
		return
	}
	if s.heartbeatSaysRunning(managed) {
		return
	}
	s.releaseAIActivityIfQuiet(managed)
}

func (s *Service) releaseAIActivityIfQuiet(managed *Session) {
	s.mu.Lock()
	if managed.Closed || !managed.aiBusyEmitted {
		s.mu.Unlock()
		return
	}
	if time.Since(managed.aiLastOutput) < aiActivityIdleThreshold {
		s.mu.Unlock()
		return
	}
	managed.aiBusyEmitted = false
	managed.aiActiveSince = time.Time{}
	managed.aiModelLatch = false
	selection := managed.Selection
	serial := managed.Serial
	s.mu.Unlock()
	s.EmitAIActivity(serial, selection, false)
}

// releaseAIActivity drops the latch when the pod reports the program gone.
func (s *Service) releaseAIActivity(managed *Session) {
	s.mu.Lock()
	if managed.Closed || !managed.aiBusyEmitted {
		s.mu.Unlock()
		return
	}
	managed.aiBusyEmitted = false
	managed.aiActiveSince = time.Time{}
	managed.aiModelLatch = false
	selection := managed.Selection
	serial := managed.Serial
	s.mu.Unlock()
	s.EmitAIActivity(serial, selection, false)
}

// latchAIActivity raises the latch on the tool's own report, which pod liveness
// alone never holds open. Caller must not hold s.mu.
func (s *Service) latchAIActivity(managed *Session) {
	if managed == nil || !aiActivityKind(managed.Kind) {
		return
	}
	s.mu.Lock()
	if managed.Closed || managed.aiBusyEmitted {
		s.mu.Unlock()
		return
	}
	managed.aiBusyEmitted = true
	managed.aiModelLatch = true
	selection := managed.Selection
	serial := managed.Serial
	s.mu.Unlock()
	s.EmitAIActivity(serial, selection, true)
}

// finalizeAIActivity releases the busy latch when an AI session exits. Caller
// must not hold s.mu.
func (s *Service) finalizeAIActivity(managed *Session) {
	if managed == nil || !aiActivityKind(managed.Kind) {
		return
	}
	s.mu.Lock()
	if managed.aiInactivityTimer != nil {
		managed.aiInactivityTimer.Stop()
		managed.aiInactivityTimer = nil
	}
	if !managed.aiBusyEmitted {
		s.mu.Unlock()
		return
	}
	managed.aiBusyEmitted = false
	managed.aiActiveSince = time.Time{}
	managed.aiModelLatch = false
	selection := managed.Selection
	serial := managed.Serial
	s.mu.Unlock()
	s.EmitAIActivity(serial, selection, false)
}

func (s *Service) EmitAIActivity(sessionID int, selection uimodel.Selection, busy bool) {
	s.emitEvent(uimodel.AIActivityEvent, AIActivityPayload{
		SessionID:   sessionID,
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		Busy:        busy,
	})
}
