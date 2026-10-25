package terminal

import (
	"strings"
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Stream silence is not evidence an AI session finished, so the busy latch is
// released only when a pod heartbeat says the program stopped.

const HeartbeatInterval = 15 * time.Second

// Past sessionHeartbeatTTL the stream-silence rule decides again, so an unreachable
// pod can never latch a session as running forever.
const sessionHeartbeatTTL = 3 * HeartbeatInterval

// PodSession is one app session the pod's heartbeat reports; Running is
// observed, never inferred from recent output.
type PodSession struct {
	ID      string
	Running bool
}

type sessionHeartbeat struct {
	observedAt time.Time
	running    map[string]struct{}
	// sessions includes stopped ones, so "unknown to the pod" differs from "stopped".
	sessions map[string]struct{}
}

func (s *Service) ReconcileSessionHeartbeatsOnce() {
	for _, selection := range s.selectionsWithPodSessions() {
		sessions, err := s.d.ProbePodSessions(selection)
		if err != nil {
			// An unreadable pod is not a finished session; let the observation age out.
			continue
		}
		s.applySessionHeartbeat(selection, sessions)
	}
	s.releaseUnobservedAIActivity()
}

// releaseUnobservedAIActivity releases latches whose observation expired; the
// silence timer that would have cleared them already fired and was declined.
func (s *Service) releaseUnobservedAIActivity() {
	s.mu.Lock()
	var candidates []*Session
	for _, managed := range s.sessions {
		if managed == nil || managed.Closed || !aiActivityKind(managed.Kind) || !managed.aiBusyEmitted {
			continue
		}
		if s.aiLatchStillSupportedLocked(managed) {
			continue
		}
		candidates = append(candidates, managed)
	}
	s.mu.Unlock()
	for _, managed := range candidates {
		s.releaseAIActivityIfQuiet(managed)
	}
}

// Caller holds s.mu.
func (s *Service) aiLatchStillSupportedLocked(managed *Session) bool {
	if _, reported := s.aiSessionEvidenceForTabLocked(managed); reported {
		// A fresh tool report means ReconcileAISessionStatusesOnce owns this latch.
		return true
	}
	if managed.aiModelLatch {
		// Pod liveness cannot hold a tool-raised latch: a process blocked on the human is alive too.
		return false
	}
	heartbeat, ok := s.sessionHeartbeats[uimodel.SelectionKey(managed.Selection)]
	return ok && time.Since(heartbeat.observedAt) <= sessionHeartbeatTTL
}

func (s *Service) selectionsWithPodSessions() []uimodel.Selection {
	seen := make(map[string]struct{})
	var out []uimodel.Selection
	s.mu.Lock()
	for _, managed := range s.sessions {
		if managed == nil || managed.Closed || strings.TrimSpace(managed.AppSession) == "" {
			continue
		}
		key := uimodel.SelectionKey(managed.Selection)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, managed.Selection)
	}
	s.mu.Unlock()
	return out
}

func (s *Service) applySessionHeartbeat(selection uimodel.Selection, sessions []PodSession) {
	heartbeat := sessionHeartbeat{
		observedAt: time.Now(),
		running:    make(map[string]struct{}, len(sessions)),
		sessions:   make(map[string]struct{}, len(sessions)),
	}
	for _, session := range sessions {
		heartbeat.sessions[session.ID] = struct{}{}
		if session.Running {
			heartbeat.running[session.ID] = struct{}{}
		}
	}

	key := uimodel.SelectionKey(selection)
	s.mu.Lock()
	if s.sessionHeartbeats == nil {
		s.sessionHeartbeats = make(map[string]sessionHeartbeat)
	}
	s.sessionHeartbeats[key] = heartbeat
	var finished []*Session
	for _, managed := range s.sessions {
		if heartbeat.reportsFinished(managed, key) {
			finished = append(finished, managed)
		}
	}
	s.mu.Unlock()

	for _, managed := range finished {
		s.releaseAIActivity(managed)
	}
}

// A session the observation does not mention is not finished; its socket may not exist yet. Caller holds s.mu.
func (h sessionHeartbeat) reportsFinished(managed *Session, key string) bool {
	if managed == nil || managed.Closed || !aiActivityKind(managed.Kind) {
		return false
	}
	if uimodel.SelectionKey(managed.Selection) != key || !managed.aiBusyEmitted {
		return false
	}
	id := strings.TrimSpace(managed.AppSession)
	if _, known := h.sessions[id]; !known {
		return false
	}
	_, running := h.running[id]
	return !running
}

// The heartbeat can only keep a session running, never invent one.
func (s *Service) heartbeatSaysRunning(managed *Session) bool {
	if managed == nil {
		return false
	}
	id := strings.TrimSpace(managed.AppSession)
	if id == "" {
		return false
	}
	s.mu.Lock()
	heartbeat, ok := s.sessionHeartbeats[uimodel.SelectionKey(managed.Selection)]
	s.mu.Unlock()
	if !ok || time.Since(heartbeat.observedAt) > sessionHeartbeatTTL {
		return false
	}
	_, running := heartbeat.running[id]
	return running
}

func (s *Service) forgetSessionHeartbeats(selection uimodel.Selection) {
	key := uimodel.SelectionKey(selection)
	s.mu.Lock()
	delete(s.sessionHeartbeats, key)
	s.mu.Unlock()
}
