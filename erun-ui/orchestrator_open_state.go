package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// The desktop keeps two separate records of which orchestrator a launch should
// reopen, because they answer different questions and must not share a lifetime.
//
// This file owns the DURABLE one: which orchestrators are open, and which
// conversation each was last known to be running. It is written the moment a
// session starts and cleared the moment the operator stops it — never at
// shutdown, because a crash, a `pkill` or a reboot takes the desktop away
// without running any hook, and a record only a clean quit could write would be
// missing exactly when it is most needed. It carries no timestamp either: an
// orchestrator the operator left open is still the one they were in, however
// long ago that was.
//
// The record is a SET, not a single id: every orchestrator the operator had
// open comes back, not just the last one started. It is kept in recency order —
// each (re)start moves its entry to the end — because that order is also how
// app_restart.go picks which one owns the terminal pane when no restart
// hand-off names one: the most recently (re)started orchestrator, on the theory
// that starting one is also how the operator ends up looking at it.
//
// Each entry also carries the nonce of the launch that wrote it, and the
// conversation the operator explicitly attached if they chose one. Neither is a
// second copy of the derived conversation id: the nonce is what lets a record
// the SESSION writes be recognised as belonging to this launch rather than to a
// run that has been replaced (see orchestrator_live_conversation.go), and the
// attachment is an operator instruction that has nowhere else to live.
//
// app_restart.go owns the OTHER record: the one-shot hand-off an in-app restart
// writes, which carries the prompt the resumed session should auto-run. That one
// stays one-shot and age-bounded, so a rebuild+restart continues its task while
// a plain launch resumes the conversation idle at the prompt.

const orchestratorOpenFileName = "orchestrator-open.json"

// orchestratorOpenEntry is one orchestrator in the durable open set: which
// orchestrators to reopen, the scope each was wired to, the launch that recorded
// it, and any conversation the operator attached by hand. It deliberately
// carries no copy of the conversation a launch RESOLVED -- that is derived, or
// tracked under LaunchID, and a third copy could only disagree with both.
type orchestratorOpenEntry struct {
	OrchestratorID string `json:"orchestratorId"`
	// LaunchID is the nonce the desktop minted for the launch that wrote this
	// entry, and handed to that session in its environment. It is the desktop's
	// half of the live-conversation record: a record the session wrote is only
	// authoritative while it echoes this nonce back, which is what stops a
	// record from a replaced run -- or from a writer that no longer exists at
	// all -- from deciding what a resume attaches to.
	LaunchID string `json:"launchId,omitempty"`
	// AttachedConversationID is the conversation the operator chose for this
	// orchestrator, and it outranks both the tracked and the derived answer. An
	// explicit choice that a later launch quietly recomputed away would be no
	// choice at all, so it is durable and cleared only by asking for the
	// default back.
	AttachedConversationID string `json:"attachedConversationId,omitempty"`
	// Environments is the scope (sorted tenant/environment pairs, see
	// orchestratorScopeOf) the recorded session was actually wired to when this
	// entry was written. An orchestrator id is mutable and reusable, so restore
	// compares this against the orchestrator's CURRENT scope: a re-scoped id must
	// not silently resume a conversation carrying context for environments it no
	// longer has, with nothing said about it. Empty for an entry a pre-scope-aware
	// release wrote, which restore treats as unknown rather than a guaranteed
	// match.
	Environments []string `json:"environments,omitempty"`
	// LastReportedConversationID is the tracked conversation id this
	// orchestrator's operator was last told about (see
	// orchestratorResumedTrackedConversationNotice). Resolving to the SAME
	// tracked conversation again has nothing new to say and stays silent;
	// resolving to a different one is new information and is reported, which is
	// what stops the notice from repeating forever once a divergence has already
	// been explained. It only tracks the "info" resolution -- a warning
	// (unconfirmed record, refused attachment) reports on every occurrence by
	// design and never updates this.
	LastReportedConversationID string `json:"lastReportedConversationId,omitempty"`
}

type orchestratorOpenState struct {
	// Orchestrators is the set of orchestrators open when the desktop was last
	// running, oldest first.
	Orchestrators []orchestratorOpenEntry `json:"orchestrators,omitempty"`
	// OrchestratorIDs is the shape this file had under an earlier release,
	// before an entry carried a session id at all. Only read, never written
	// again: an operator upgrading from that release must not lose the
	// orchestrators they had open — they come back with no recorded session,
	// so restore starts each of them on a fresh conversation rather than
	// guessing which one was theirs.
	OrchestratorIDs []string `json:"orchestratorIds,omitempty"`
	// OrchestratorID is the shape from a release before that, before the file
	// could hold more than one id at all. Same treatment: read, never written
	// again.
	OrchestratorID string `json:"orchestratorId,omitempty"`
}

// defaultOrchestratorOpenPath is a sibling of window-state.json under
// UserConfigDir()/ERun: desktop session state the app restores itself with.
func defaultOrchestratorOpenPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "ERun", orchestratorOpenFileName)
}

// recordOpenOrchestrator adds an orchestrator to the open set, or moves it to
// the end (most recent) if it was already there, stamped with the nonce of the
// launch doing the recording. Called every time a session is spawned, which is
// what makes the nonce a promise the desktop always keeps: a launch that never
// wrote one could not tell a live record from an abandoned one.
//
// An attachment the operator made survives: it is their standing choice about
// this orchestrator, not a property of the launch being recorded.
func recordOpenOrchestrator(path, orchestratorID, launchID string, scope []string) error {
	orchestratorID = strings.TrimSpace(orchestratorID)
	if path == "" || orchestratorID == "" {
		return nil
	}
	entries := readOpenOrchestrators(path)
	existing := orchestratorEntryOrEmpty(entries, orchestratorID)
	attached := strings.TrimSpace(existing.AttachedConversationID)
	lastReported := strings.TrimSpace(existing.LastReportedConversationID)
	out := make([]orchestratorOpenEntry, 0, len(entries)+1)
	for _, entry := range entries {
		if entry.OrchestratorID == orchestratorID {
			continue
		}
		out = append(out, entry)
	}
	out = append(out, orchestratorOpenEntry{
		OrchestratorID:             orchestratorID,
		LaunchID:                   strings.TrimSpace(launchID),
		AttachedConversationID:     attached,
		Environments:               scope,
		LastReportedConversationID: lastReported,
	})
	return writeOpenOrchestrators(path, out)
}

// setAttachedOrchestratorConversation records the conversation the operator
// chose for an orchestrator, or clears it when conversationID is empty. It
// leaves everything else on the entry alone, and creates one for an orchestrator
// with no entry yet: attaching is also a statement that this orchestrator should
// come back, and the launch that follows the attach records the rest.
func setAttachedOrchestratorConversation(path, orchestratorID, conversationID string) error {
	orchestratorID = strings.TrimSpace(orchestratorID)
	if path == "" || orchestratorID == "" {
		return nil
	}
	entries := readOpenOrchestrators(path)
	found := false
	for i, entry := range entries {
		if entry.OrchestratorID != orchestratorID {
			continue
		}
		entries[i].AttachedConversationID = strings.TrimSpace(conversationID)
		found = true
	}
	if !found {
		entries = append(entries, orchestratorOpenEntry{
			OrchestratorID:         orchestratorID,
			AttachedConversationID: strings.TrimSpace(conversationID),
		})
	}
	return writeOpenOrchestrators(path, entries)
}

// markOrchestratorConversationReported records the tracked conversation id an
// orchestrator's operator was just told about, so a later launch that resolves
// the SAME tracked conversation again finds nothing new to say. Called only for
// the "info" resolution (see orchestratorConversationNoticeKind); a warning
// resolution reports every occurrence and never calls this. Creates the entry
// when none exists yet, mirroring setAttachedOrchestratorConversation.
func markOrchestratorConversationReported(path, orchestratorID, conversationID string) error {
	orchestratorID = strings.TrimSpace(orchestratorID)
	if path == "" || orchestratorID == "" {
		return nil
	}
	conversationID = strings.TrimSpace(conversationID)
	entries := readOpenOrchestrators(path)
	found := false
	for i, entry := range entries {
		if entry.OrchestratorID != orchestratorID {
			continue
		}
		if entry.LastReportedConversationID == conversationID {
			return nil
		}
		entries[i].LastReportedConversationID = conversationID
		found = true
	}
	if !found {
		entries = append(entries, orchestratorOpenEntry{
			OrchestratorID:             orchestratorID,
			LastReportedConversationID: conversationID,
		})
	}
	return writeOpenOrchestrators(path, entries)
}

// clearOpenOrchestrator forgets one orchestrator when the operator stops it,
// which is what keeps an explicitly stopped orchestrator closed on every later
// launch. Every other entry in the set is left exactly as recorded: stopping
// one orchestrator must not forget that the rest are still open.
func clearOpenOrchestrator(path, orchestratorID string) error {
	orchestratorID = strings.TrimSpace(orchestratorID)
	if path == "" || orchestratorID == "" {
		return nil
	}
	entries := readOpenOrchestrators(path)
	out := make([]orchestratorOpenEntry, 0, len(entries))
	removed := false
	for _, entry := range entries {
		if entry.OrchestratorID == orchestratorID {
			removed = true
			continue
		}
		out = append(out, entry)
	}
	if !removed {
		return nil
	}
	return writeOpenOrchestrators(path, out)
}

// readOpenOrchestrators returns the orchestrators that were open when the
// desktop last ran, oldest first, each with the launch that recorded it and any
// conversation the operator attached. Reading does not clear the record: it is
// durable, so every launch reopens the same set until an entry is stopped.
func readOpenOrchestrators(path string) []orchestratorOpenEntry {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var state orchestratorOpenState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}
	if entries := dedupOrchestratorEntries(state.Orchestrators); len(entries) > 0 {
		return entries
	}
	// A legacy file (the older scalar shape, or the later id-only set) recorded
	// no launch, so every id it names comes back with none — restore treats
	// whatever a session recorded under it as unconfirmable and resumes the
	// derived anchor, saying so, rather than adopting a record no launch of this
	// build ever vouched for.
	legacy := make([]orchestratorOpenEntry, 0, len(state.OrchestratorIDs)+1)
	if id := strings.TrimSpace(state.OrchestratorID); id != "" {
		legacy = append(legacy, orchestratorOpenEntry{OrchestratorID: id})
	}
	for _, id := range state.OrchestratorIDs {
		legacy = append(legacy, orchestratorOpenEntry{OrchestratorID: id})
	}
	return dedupOrchestratorEntries(legacy)
}

// dedupOrchestratorEntries trims, drops empties, and keeps the first occurrence
// of each id, preserving order.
func dedupOrchestratorEntries(entries []orchestratorOpenEntry) []orchestratorOpenEntry {
	seen := make(map[string]struct{}, len(entries))
	out := make([]orchestratorOpenEntry, 0, len(entries))
	for _, entry := range entries {
		id := strings.TrimSpace(entry.OrchestratorID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, orchestratorOpenEntry{
			OrchestratorID:             id,
			LaunchID:                   strings.TrimSpace(entry.LaunchID),
			AttachedConversationID:     strings.TrimSpace(entry.AttachedConversationID),
			Environments:               entry.Environments,
			LastReportedConversationID: strings.TrimSpace(entry.LastReportedConversationID),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// writeOpenOrchestrators persists the open set, migrating a legacy file to the
// current shape the first time anything changes. An empty set removes the file
// rather than leaving a durable record with nothing durable to say.
func writeOpenOrchestrators(path string, entries []orchestratorOpenEntry) error {
	if path == "" {
		return nil
	}
	if len(entries) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	data, err := json.Marshal(orchestratorOpenState{Orchestrators: entries})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
