package envops

import (
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Repairs a lost MCP port-forward (dropped or stale) via `erun open --reconnect`,
// which owns the kubectl lifecycle. The repair is bounded, idempotent through
// the shared ensure, and never wakes a stopped environment.

// ForwardRepairAttempts gives a mid-replacement pod time to return.
const ForwardRepairAttempts = 3

// forwardRepairEpisode is keyed by port: a new local port is a new forward.
type forwardRepairEpisode struct {
	port     int
	attempts int
	reported bool
}

// ReconcileForwardHealth reports whether the row must render a diagnosed outage.
func (s *Service) ReconcileForwardHealth(selection uimodel.Selection, port int, portIsBound bool) bool {
	const established = true
	// Only a bound port whose edge answers counts as recovery.
	answers := portIsBound && s.MCPEdgeAnswers(port)
	if answers {
		s.d.NoteEdgeAnswering(selection)
	}
	health := kube.ClassifyPortForward(established, portIsBound, answers)
	if !health.Interrupted() {
		s.ForgetForwardRepair(selection)
		return false
	}
	return s.repairInterruptedPortForward(selection, port, health)
}

// MCPEdgeAnswers counts any HTTP reply, 401 included.
func (s *Service) MCPEdgeAnswers(port int) bool {
	return s.d.CanReachMCPEndpoint(port)
}

// repairInterruptedPortForward runs only on the single sweep goroutine, so the
// read-decide-record sequence needs no spanning lock.
func (s *Service) repairInterruptedPortForward(selection uimodel.Selection, port int, health kube.PortForwardHealth) bool {
	// Ask the cluster only on this path; a stop looks like a dropped forward.
	if s.RuntimeStopped(selection) {
		s.ForgetForwardRepair(selection)
		return false
	}
	episode := s.forwardRepairEpisodeFor(selection, port)
	if episode.attempts < ForwardRepairAttempts {
		// Count only a rebind this call started; one in flight is this sweep's attempt.
		if s.ensureRuntime(selection, EnsureForBrokenForward) {
			s.recordForwardRepairAttempt(selection)
		}
		return false
	}
	if !episode.reported {
		s.recordForwardRepairReported(selection)
		s.notifyEnv("warning", selection.Tenant, selection.Environment,
			SourceForwardOutage,
			mcp.DescribeUnrepairedPortForward(selection.Tenant, selection.Environment, port, episode.attempts, health),
			"")
	}
	return true
}

func (s *Service) forwardRepairEpisodeFor(selection uimodel.Selection, port int) forwardRepairEpisode {
	key := uimodel.SelectionKey(selection)
	s.mu.Lock()
	defer s.mu.Unlock()
	episode, ok := s.forwardRepairs[key]
	if !ok || episode.port != port {
		episode = forwardRepairEpisode{port: port}
		s.forwardRepairs[key] = episode
	}
	return episode
}

func (s *Service) recordForwardRepairAttempt(selection uimodel.Selection) {
	s.updateForwardRepairEpisode(selection, func(episode *forwardRepairEpisode) {
		episode.attempts++
	})
}

// recordForwardRepairReported latches the episode's single report.
func (s *Service) recordForwardRepairReported(selection uimodel.Selection) {
	s.updateForwardRepairEpisode(selection, func(episode *forwardRepairEpisode) {
		episode.reported = true
	})
}

func (s *Service) updateForwardRepairEpisode(selection uimodel.Selection, mutate func(*forwardRepairEpisode)) {
	key := uimodel.SelectionKey(selection)
	s.mu.Lock()
	defer s.mu.Unlock()
	episode, ok := s.forwardRepairs[key]
	if !ok {
		return
	}
	mutate(&episode)
	s.forwardRepairs[key] = episode
}

// ForgetForwardRepair ends the episode and retires its notification.
func (s *Service) ForgetForwardRepair(selection uimodel.Selection) {
	selection = uimodel.NormalizeSelection(selection)
	key := uimodel.SelectionKey(selection)
	s.mu.Lock()
	episode, ok := s.forwardRepairs[key]
	delete(s.forwardRepairs, key)
	s.mu.Unlock()
	if ok && episode.reported {
		s.clearEnvNotification(selection.Tenant, selection.Environment, SourceForwardOutage)
	}
}
