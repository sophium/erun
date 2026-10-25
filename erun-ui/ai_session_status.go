package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// The AI badge asks the pod's `erun activity ai-session status` (output volume cannot
// tell awaiting-input from working); reads use pod exec so they work with the edge down.

// aiSessionStatusTTL matches the heartbeat bound so an unrefreshable reading stops pinning a row.
const aiSessionStatusTTL = 3 * sessionHeartbeatInterval

const aiSessionStatusTimeout = 5 * time.Second

// aiSessionStatusMarker delimits the JSON because kubectlText merges stderr into stdout.
const aiSessionStatusMarker = "erun-ai-sessions"

// aiSessionReading includes exited sessions; attribution to a tab happens in aiSessionReadingForTab.
type aiSessionReading struct {
	observedAt time.Time
	sessions   []model.AISessionStatus
}

// aiSessionStatusScript discards erun's stderr; a pod without the verb prints
// nothing between the markers, which reads as no reading.
func aiSessionStatusScript(selection uimodel.Selection) string {
	return fmt.Sprintf(
		"printf '%s\\tbegin\\n'; erun activity ai-session status --tenant %s --environment %s --output json 2>/dev/null; printf '%s\\tend\\n'",
		aiSessionStatusMarker,
		shellSingleQuote(selection.Tenant),
		shellSingleQuote(selection.Environment),
		aiSessionStatusMarker,
	)
}

// parseAISessionStatuses reports ok=false for a missing or malformed block: a
// failed read is no evidence, not "nothing working".
func parseAISessionStatuses(probe string) ([]model.AISessionStatus, bool) {
	block, ok := markedBlock(probe, aiSessionStatusMarker)
	if !ok {
		return nil, false
	}
	// Trace output around the array is dropped.
	start := strings.Index(block, "[")
	end := strings.LastIndex(block, "]")
	if start < 0 || end < start {
		return nil, false
	}
	var statuses []model.AISessionStatus
	if err := json.Unmarshal([]byte(block[start:end+1]), &statuses); err != nil {
		return nil, false
	}
	return statuses, true
}

func markedBlock(output, marker string) (string, bool) {
	_, rest, ok := strings.Cut(output, marker+"\tbegin")
	if !ok {
		return "", false
	}
	if end := strings.Index(rest, marker+"\tend"); end >= 0 {
		return rest[:end], true
	}
	// An unterminated block is still a block.
	return rest, true
}

// reconcileAISessionStatusesOnce runs after the heartbeat tick and reads only envs
// with an open AI tab. It holds no lock across the network read.
func (a *App) reconcileAISessionStatusesOnce() {
	tabs := a.openAITabs()
	read := make(map[string]struct{}, len(tabs))
	for _, managed := range tabs {
		key := selectionKey(managed.selection)
		if _, done := read[key]; !done {
			read[key] = struct{}{}
			a.readAISessionStatusOnce(managed.selection)
		}
		a.applyAISessionReading(managed)
	}
}

// openAITabs returns AI tabs in serial order so a pass is reproducible.
func (a *App) openAITabs() []*managedTerminal {
	a.mu.Lock()
	defer a.mu.Unlock()
	tabs := make([]*managedTerminal, 0, len(a.sessions))
	for _, managed := range a.sessions {
		if managed == nil || managed.closed || !aiActivityKind(managed.kind) {
			continue
		}
		tabs = append(tabs, managed)
	}
	slices.SortFunc(tabs, func(a, b *managedTerminal) int { return a.serial - b.serial })
	return tabs
}

// readAISessionStatusOnce keeps the previous reading on failure: an unreachable
// pod is not evidence a turn ended.
func (a *App) readAISessionStatusOnce(selection uimodel.Selection) {
	ctx, cancel := context.WithTimeout(a.context(), aiSessionStatusTimeout)
	defer cancel()
	output, err := a.execInRuntimePod(ctx, selection, aiSessionStatusScript(selection))
	if err != nil {
		return
	}
	sessions, ok := parseAISessionStatuses(output)
	if !ok {
		return
	}
	a.recordAISessionReading(selection, sessions)
}

func (a *App) recordAISessionReading(selection uimodel.Selection, sessions []model.AISessionStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.aiSessionReadings == nil {
		a.aiSessionReadings = make(map[string]aiSessionReading)
	}
	a.aiSessionReadings[selectionKey(selection)] = aiSessionReading{
		observedAt: time.Now(),
		sessions:   sessions,
	}
}

// aiSessionReadingForLocked treats an expired reading as absent. Caller holds a.mu.
func (a *App) aiSessionReadingForLocked(selection uimodel.Selection) (aiSessionReading, bool) {
	reading, ok := a.aiSessionReadings[selectionKey(selection)]
	if !ok || time.Since(reading.observedAt) > aiSessionStatusTTL {
		return aiSessionReading{}, false
	}
	return reading, true
}

// aiSessionEvidenceForTab: reported=false leaves the decision to the volume latch.
func (a *App) aiSessionEvidenceForTab(managed *managedTerminal) (busy bool, reported bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.aiSessionEvidenceForTabLocked(managed)
}

// Caller holds a.mu.
func (a *App) aiSessionEvidenceForTabLocked(managed *managedTerminal) (busy bool, reported bool) {
	if managed == nil {
		return false, false
	}
	reading, ok := a.aiSessionReadingForLocked(managed.selection)
	if !ok {
		return false, false
	}
	return aiSessionReadingForTab(reading, managed.startedAt)
}

// aiSessionReadingForTab ignores records older than the tab's session, since a
// previous session's turn would otherwise pin the row; those fall back to volume.
func aiSessionReadingForTab(reading aiSessionReading, startedAt time.Time) (busy bool, reported bool) {
	for _, status := range reading.sessions {
		if status.LastActivity.IsZero() || !status.LastActivity.After(startedAt) {
			continue
		}
		reported = true
		if status.State == model.AISessionStateBusy {
			busy = true
		}
	}
	return busy, reported
}

// applyAISessionReading leaves the tab alone when the reading says nothing about it.
func (a *App) applyAISessionReading(managed *managedTerminal) {
	busy, reported := a.aiSessionEvidenceForTab(managed)
	if !reported {
		return
	}
	if busy {
		a.latchAIActivity(managed)
		return
	}
	// A reported turn boundary means no work in flight, even while the process
	// is alive and waiting for input.
	a.releaseAIActivity(managed)
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
