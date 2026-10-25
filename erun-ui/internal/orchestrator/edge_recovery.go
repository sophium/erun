package orchestrator

import (
	"log"
	"slices"
	"strings"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// wireOrchestratorMCP logs an unreachable edge only at spawn; the activity sweep,
// which already probes every forward, logs the matching recovery.

// orchestratorEdgeOutage tracks every orchestrator that logged the outage so each
// gets its own exit line. It is dropped once the exits are logged.
type orchestratorEdgeOutage struct {
	label           string
	orchestratorIDs []string
}

// recordOrchestratorEdgeOutage skips labels that are not tenant/env: no exit could retire them.
func (s *Service) recordOrchestratorEdgeOutage(orchestratorID, label string) {
	tenant, environment, ok := cutEnvLabel(label)
	if !ok {
		return
	}
	key := uimodel.SelectionKey(uimodel.Selection{Tenant: tenant, Environment: environment})
	s.withState(func() {
		if s.edgeOutages == nil {
			s.edgeOutages = make(map[string]orchestratorEdgeOutage)
		}
		episode := s.edgeOutages[key]
		episode.label = label
		if !slices.Contains(episode.orchestratorIDs, orchestratorID) {
			episode.orchestratorIDs = append(episode.orchestratorIDs, orchestratorID)
		}
		s.edgeOutages[key] = episode
	})
}

// NoteEdgeAnswering logs the exit for an edge the sweep just saw
// answer, and forgets it.
func (s *Service) NoteEdgeAnswering(selection uimodel.Selection) {
	key := uimodel.SelectionKey(uimodel.NormalizeSelection(selection))
	var episode orchestratorEdgeOutage
	var ok bool
	s.withState(func() {
		episode, ok = s.edgeOutages[key]
		delete(s.edgeOutages, key)
	})
	if !ok {
		return
	}
	for _, orchestratorID := range episode.orchestratorIDs {
		log.Printf("erun-app: orchestrator %s: wired %s and its edge is answering again",
			orchestratorID, episode.label)
	}
}

// cutEnvLabel requires both halves of "tenant/environment" to be non-empty.
func cutEnvLabel(label string) (tenant, environment string, ok bool) {
	tenant, environment, found := strings.Cut(label, "/")
	if !found || tenant == "" || environment == "" {
		return "", "", false
	}
	return tenant, environment, true
}
