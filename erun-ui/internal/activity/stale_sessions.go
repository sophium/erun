package activity

import (
	"strconv"
	"strings"
	"time"
)

// staleShellDebounce skips fresh sessions, since some shells fork children before the parent exits.
const staleShellDebounce = 4 * activityPollerInterval

// runStaleShellDetector surfaces shells that died without closing their PTY.
func (s *Service) runStaleShellDetector(stop <-chan struct{}) {
	ticker := time.NewTicker(activityPollerInterval)
	defer ticker.Stop()
	s.reconcileStaleShellsOnce()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.reconcileStaleShellsOnce()
		}
	}
}

func (s *Service) reconcileStaleShellsOnce() {
	stale := s.d.Sessions.DeadSessions(staleShellDebounce)
	seenIDs := make(map[string]struct{}, len(stale))
	for _, session := range stale {
		entry, _ := s.upsertStaleShellActivity(session)
		if entry.ID != "" {
			seenIDs[entry.ID] = struct{}{}
		}
	}
	s.removeRecoveredStaleShellActivities(seenIDs)
}

func (s *Service) upsertStaleShellActivity(session Session) (Entry, bool) {
	id := staleShellActivityID(session.Serial)
	tenant := strings.TrimSpace(session.Selection.Tenant)
	environment := strings.TrimSpace(session.Selection.Environment)
	command := staleShellCommandFromKind(session.Kind)
	summary := staleShellSummary(command, tenant, environment)
	return s.queue.start(Entry{
		ID:                id,
		Command:           command,
		Tenant:            tenant,
		Environment:       environment,
		KubernetesContext: strings.TrimSpace(session.Selection.KubernetesContext),
		Summary:           summary,
		Source:            "shell",
		SessionID:         strconv.Itoa(session.Serial),
		StartedAt:         session.StartedAt.UTC(),
		Error:             "shell exited unexpectedly (use Kill to dismiss)",
	})
}

func (s *Service) removeRecoveredStaleShellActivities(stillStale map[string]struct{}) {
	for _, entry := range s.queue.list() {
		if entry.Source != "shell" {
			continue
		}
		if entry.Status != StatusRunning {
			continue
		}
		if _, ok := stillStale[entry.ID]; ok {
			continue
		}
		s.queue.forceDismiss(entry.ID)
		s.emitState(entry)
	}
}

// KillSession terminates a stale session and dismisses its entry. It marks the
// session closed first so the detector does not re-surface the reaped process.
func (s *Service) KillSession(serial int) bool {
	if serial <= 0 {
		return false
	}
	if !s.d.Sessions.Kill(serial) {
		return false
	}
	if entry, _, ok := s.queue.forceDismiss(staleShellActivityID(serial)); ok {
		s.unlockTerminalsForActivity(entry)
		s.emitState(entry)
	}
	return true
}

func staleShellActivityID(serial int) string {
	return "shell:" + strconv.Itoa(serial)
}

func staleShellCommandFromKind(kind string) string {
	switch kind {
	case KindOpen:
		return "open"
	case KindAI:
		return "ai"
	case KindLocal:
		return "local"
	case KindCommand:
		return "command"
	case KindContributeERun, KindContributeAI, KindOrchestrator:
		fallthrough
	default:
		return kind
	}
}

func staleShellSummary(command, tenant, environment string) string {
	parts := []string{command}
	if tenant != "" || environment != "" {
		target := tenant
		if environment != "" {
			if target != "" {
				target += "/"
			}
			target += environment
		}
		parts = append(parts, target)
	}
	return strings.Join(parts, " ")
}
