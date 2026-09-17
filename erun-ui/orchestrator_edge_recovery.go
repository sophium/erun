package main

import (
	"log"
	"strings"
)

// The entry this file pairs with is logged by wireOrchestratorMCP: an
// orchestrator wired to an environment whose edge does not answer gets a line
// saying so. Until #2348 that line had no counterpart, and an entry-only
// transition degrades to "something was wrong at some point inside the retained
// window" — a recovered edge reads exactly like a dead one, and the two have
// different remedies (investigate vs. ignore).
//
// The observer that can log the exit is the activity sweep, not the wiring
// path. wireOrchestratorMCP runs once, at spawn: it can say an edge was down,
// but it is never called again and so can never be the one to notice the edge
// came back. The sweep already probes every recorded forward's edge on its
// ordinary path, so pairing costs it one map read and no extra probe.

// orchestratorEdgeOutage is one recorded entry: which orchestrator was wired to
// the edge and the wiring it was wired to, so the exit can name both. It is
// dropped when the exit is logged, so a later outage is a fresh episode rather
// than a re-report of this one.
type orchestratorEdgeOutage struct {
	orchestratorID string
	label          string
}

// recordOrchestratorEdgeOutage remembers an edge that was unreachable at wire
// time, keyed by the environment the sweep will observe it through. A label
// that does not name a tenant/environment pair is not recorded: the sweep could
// never match it, so recording it would leave an entry no exit can retire.
func (a *App) recordOrchestratorEdgeOutage(orchestratorID, label string) {
	tenant, environment, ok := cutEnvLabel(label)
	if !ok {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.edgeOutages == nil {
		a.edgeOutages = make(map[string]orchestratorEdgeOutage)
	}
	a.edgeOutages[selectionKey(uiSelection{Tenant: tenant, Environment: environment})] = orchestratorEdgeOutage{
		orchestratorID: orchestratorID,
		label:          label,
	}
}

// noteOrchestratorEdgeAnswering logs the exit transition for an edge the sweep
// has just seen answer through a bound forward, and forgets it.
//
// The sweep establishes that fact in two places, and both call this: the
// ordinary one, where the edge answered a real MCP request and the sweep has a
// status to show for it, and the narrower one in reconcileForwardHealth, where
// the port is bound and the edge replied to the reachability probe but the idle
// question failed. A reply is a live tunnel in both cases, which is the
// question an outage entry leaves open.
//
// A no-op when nothing was recorded for this environment, which is the common
// case: most edges never fail their wire-time probe, and an edge that fails it
// and stays broken keeps its entry until it answers, so it gets no exit line.
// That is the property — every entry eventually pairs with an exit once the
// edge is healthy, and a still-broken edge has none.
func (a *App) noteOrchestratorEdgeAnswering(selection uiSelection) {
	key := selectionKey(normalizeSelection(selection))
	a.mu.Lock()
	episode, ok := a.edgeOutages[key]
	delete(a.edgeOutages, key)
	a.mu.Unlock()
	if !ok {
		return
	}
	log.Printf("erun-app: orchestrator %s: wired %s and its edge is answering again",
		episode.orchestratorID, episode.label)
}

// cutEnvLabel splits the "tenant/environment" label an unreachable wiring
// carries into the pair the activity sweep keys its observations by. Both
// halves must be non-empty: "frs/" names no environment to observe, and a label
// with no separator at all names neither.
func cutEnvLabel(label string) (tenant, environment string, ok bool) {
	tenant, environment, found := strings.Cut(label, "/")
	if !found || tenant == "" || environment == "" {
		return "", "", false
	}
	return tenant, environment, true
}
