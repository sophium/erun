package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	eruncommon "github.com/sophium/erun/erun-common"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// orchestratorRestoreDirName holds one hand-off slot per orchestrator. A single
// shared slot let a second restart overwrite the first, so one orchestrator's
// resume cancelled another's with nothing said; several orchestrators run at
// once, so the slot has to be as private as the task it carries.
const orchestratorRestoreDirName = "orchestrator-restore"

// legacyOrchestratorRestoreFileName is the single shared slot restarts wrote
// before this. It is still read once, because the restart that first installs
// the per-orchestrator slot is itself staged by the binary that had only this
// one — dropping it would lose exactly the hand-off that delivers the fix.
const legacyOrchestratorRestoreFileName = "orchestrator-restore.json"

// orchestratorRestoreMaxAge bounds how long the restart hand-off stays valid.
// It is deliberately short because what the hand-off carries is a task to run
// unattended: a rebuild+restart should continue its work, but a prompt fired at
// a launch hours later would run against a world the operator has moved on
// from. Which orchestrator to reopen is durable session state and is NOT bound
// this way — see orchestrator_open_state.go for why the two are separate.
const orchestratorRestoreMaxAge = 10 * time.Minute

// orchestratorRestartResumePrompt is what a rebuild+restart hands the resumed
// conversation. It carries no task of its own on purpose: the task lives in the
// return note the orchestrator wrote before triggering the restart, because a
// conversation does not survive the restart and a file does. The note's own
// size is not knowable from the id, so it is reported by
// orchestratorRestartResumePromptFor, which every resume path uses.
func orchestratorRestartResumePrompt(orchestratorID string) string {
	return orchestratorRestartResumeHandoff(orchestratorID) + " " + orchestratorRestartResumeObjective
}

// orchestratorRestartResumeHandoff is the prompt's opening: what happened, and
// which file carries the task. Split out because the note's size is said
// between it and the objective below.
//
// It names that note exactly. Orchestrators share one working directory, so
// "the note you wrote here" resolves to whichever note is there — and a session
// following it faithfully can pick up another orchestrator's agenda and carry it
// to a confident, wrong end.
func orchestratorRestartResumeHandoff(orchestratorID string) string {
	return "The desktop just restarted to pick up a rebuild. " +
		"Read " + orchestratorReturnNoteName(orchestratorID) + " in this working directory — that one is yours, " +
		"and any other return note beside it belongs to a different orchestrator."
}

// orchestratorRestartResumeObjective is what the prompt closes with, and it
// stays last: the task is stated after whatever the session needs to know about
// the note, never buried behind it.
const orchestratorRestartResumeObjective = "Confirm the rebuilt code is live in the running process, " +
	"and carry that task through to its verified end without waiting to be asked."

// orchestratorRestartResumePromptFor is orchestratorRestartResumePrompt with the
// one thing the id alone cannot answer: how large the note it names has grown.
//
// erun is what points every resume at that file, so the size belongs in the
// prompt that points at it. The note is contracted as a task hand-off — written
// for one restart, read once, superseded by the next — but nothing bounds it,
// so an orchestrator that appends across cycles produces a file whose sections
// claim to supersede each other with nothing on its surface saying which ones
// still hold (a measured one: 484 KB, 196 sections, already compacted twice).
// The size is the whole of what is said here: no section of the note is
// validated, required, or refused, and whether the content still holds is the
// reader's to judge.
//
// Both resume paths compose through this one function, so a live session and a
// hand-off answered from the open set announce the same note the same way. An
// absent or unreadable note is silent: the prompt already handles a hand-off
// that is not there, and a guessed size would be worse than none.
func orchestratorRestartResumePromptFor(dir, orchestratorID string) string {
	handoff := orchestratorRestartResumeHandoff(orchestratorID)
	if size, ok := orchestratorReturnNoteSize(dir, orchestratorID); ok {
		if warning := orchestratorReturnNoteBloatWarning(size); warning != "" {
			handoff += " " + warning
		}
	}
	return handoff + " " + orchestratorRestartResumeObjective
}

// orchestratorReturnNoteBloatBytes is the size past which a return note is no
// longer the one-cycle hand-off it is meant to be. Generous on purpose: a note
// carrying the task, the work delivered, in-flight job ids and first checks is
// a handful of KB — the sibling notes on the machine that reported this measure
// 2,931 / 4,477 / 9,140 bytes — so 32 KB leaves room for an unusually detailed
// cycle while still catching the failure the bound exists for, a note that grew
// by append across cycles. A note of exactly the bound is still within it.
const orchestratorReturnNoteBloatBytes = 32 * 1024

// orchestratorReturnNoteSize reports how large this orchestrator's return note
// is, and whether it could be measured at all.
func orchestratorReturnNoteSize(dir, orchestratorID string) (int64, bool) {
	info, err := os.Stat(filepath.Join(dir, orchestratorReturnNoteName(orchestratorID)))
	if err != nil || !info.Mode().IsRegular() {
		return 0, false
	}
	return info.Size(), true
}

// orchestratorReturnNoteBloatWarning is the sentence appended to the resume
// prompt when the note it names has outgrown a hand-off, and "" when it has
// not.
func orchestratorReturnNoteBloatWarning(size int64) string {
	if size <= orchestratorReturnNoteBloatBytes {
		return ""
	}
	return "That note is now " + describeNoteSize(size) +
		", far past the one-cycle hand-off it is meant to be, so it may carry state that has since been superseded: " +
		"read its newest section first, and treat the older ones as history rather than as instructions."
}

// describeNoteSize renders a note's size the way its reader needs it: the exact
// count, so nothing is lost to rounding, and the scale, so a note measured in
// hundreds of KB reads as the anomaly it is rather than as a number.
func describeNoteSize(size int64) string {
	const kb = 1024
	if size >= kb*kb {
		return fmt.Sprintf("%d bytes (%.1f MB)", size, float64(size)/(kb*kb))
	}
	return fmt.Sprintf("%d bytes (%.0f KB)", size, float64(size)/kb)
}

type orchestratorRestoreState struct {
	OrchestratorID string `json:"orchestratorId"`
	// ConversationID names the exact conversation that asked for the restart.
	// An orchestrator id is mutable and reusable, so conversations accumulate
	// against one id and continuing "whatever that id resolves to" can wake a
	// different session — with a prompt telling it to carry on.
	ConversationID string `json:"conversationId,omitempty"`
	// Environments is the scope that conversation was wired to, as sorted
	// tenant/environment pairs. Re-scoping an orchestrator leaves its id
	// untouched, so this is the only thing that can tell a resume it is about to
	// continue into a world the conversation has never seen.
	Environments []string `json:"environments,omitempty"`
	SavedAtUnix  int64    `json:"savedAtUnix"`
	// ResumePrompt, when set, is handed to the resumed Claude session so it
	// continues its task itself after a rebuild+restart instead of idling.
	ResumePrompt string `json:"resumePrompt,omitempty"`
}

// orchestratorNoticeKind classifies how loudly an operator-facing notice about
// a reopened orchestrator should read. Only "warning" is minted today — an
// unhonourable attachment, a fall-through to the anchor that diverged from the
// conversation the session last reported, a refused hand-off, a changed scope,
// a hand-off left mid-task — because every one of them means something the
// operator asked for, or something a session recorded, could not be honoured.
// "info" is kept as a distinct kind (see Sidebar.OrchestratorNotice.tsx's
// role="status" rendering) for a future routine notice; an ordinary resumption
// of the derived anchor has nothing to report at all.
type orchestratorNoticeKind string

const (
	orchestratorNoticeInfo    orchestratorNoticeKind = "info"
	orchestratorNoticeWarning orchestratorNoticeKind = "warning"
)

// orchestratorNotice is one operator-facing notice about a reopened
// orchestrator: which one it is about (empty when it names several, such as
// the hand-offs-not-reopened summary), how loudly it reads, and the text
// itself. Several orchestrators can each produce their own notice on the same
// restore, and each keeps its own kind rather than being flattened into one
// joined string — a warning sitting among several successes must stay
// visually distinct from them, not disappear into the same paragraph.
type orchestratorNotice struct {
	OrchestratorID string                 `json:"orchestratorId,omitempty"`
	Kind           orchestratorNoticeKind `json:"kind"`
	Text           string                 `json:"text"`
}

// appendOrchestratorNotice appends notice to notices when it carries text, so
// a producer that found nothing to report need not be special-cased at every
// call site.
func appendOrchestratorNotice(notices []orchestratorNotice, notice orchestratorNotice) []orchestratorNotice {
	if strings.TrimSpace(notice.Text) == "" {
		return notices
	}
	return append(notices, notice)
}

// relaunchTarget is the JSON-safe view the frontend reads on boot. OrchestratorID
// is the one this launch resumes and that OWNS THE TERMINAL PANE — the pane is
// single, so exactly one orchestrator gets it. AlsoReopen lists every other
// orchestrator that was open and comes back too, started idle alongside it.
// Only the pane-owning orchestrator can carry a resume PROMPT, by construction,
// because ResumePrompt is a field of this one target, not of each entry in
// AlsoReopen — but every entry, owner included, carries the conversation id it
// should resume: see resolveReopenSessionID for how that id is decided, which
// is never a re-derivation from the orchestrator id. Notices carries one entry
// per surprising resolution — the pane owner's, or another reopened
// orchestrator's — each with its own kind, rather than one joined string that
// would flatten several different severities into one paragraph.
type relaunchTarget struct {
	OrchestratorID string                  `json:"orchestratorId"`
	ConversationID string                  `json:"conversationId"`
	ResumePrompt   string                  `json:"resumePrompt"`
	AlsoReopen     []orchestratorReopenRef `json:"alsoReopen,omitempty"`
	Notices        []orchestratorNotice    `json:"notices,omitempty"`
}

// orchestratorReopenRef is one orchestrator in AlsoReopen: its id and the
// conversation it resumes, resolved the same way the pane owner's is.
type orchestratorReopenRef struct {
	OrchestratorID string `json:"orchestratorId"`
	ConversationID string `json:"conversationId,omitempty"`
}

// defaultOrchestratorRestoreDir is the directory beside window-state.json under
// UserConfigDir()/ERun holding the per-orchestrator restart hand-offs.
func defaultOrchestratorRestoreDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "ERun", orchestratorRestoreDirName)
}

// orchestratorRestorePath is one orchestrator's own slot. It returns "" for an
// id that is not a plain file name: an id is a slug by construction, and this is
// the check that keeps it one rather than a path into somewhere else.
func orchestratorRestorePath(dir, orchestratorID string) string {
	id := strings.TrimSpace(orchestratorID)
	if dir == "" || id == "" || id != filepath.Base(id) || strings.HasPrefix(id, ".") {
		return ""
	}
	return filepath.Join(dir, id+".json")
}

// legacyOrchestratorRestorePath is where the single shared slot sat, beside the
// directory that replaced it.
func legacyOrchestratorRestorePath(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(dir), legacyOrchestratorRestoreFileName)
}

func writeOrchestratorRestoreTarget(dir string, state orchestratorRestoreState, now time.Time) error {
	state.OrchestratorID = strings.TrimSpace(state.OrchestratorID)
	state.ResumePrompt = strings.TrimSpace(state.ResumePrompt)
	state.SavedAtUnix = now.Unix()
	path := orchestratorRestorePath(dir, state.OrchestratorID)
	if path == "" {
		return nil
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// consumeOrchestratorRestoreTargets reads and deletes every pending hand-off,
// answering with the one this launch delivers and the ones it cannot. Deleting
// on read keeps each one-shot: honored on the next boot and never again.
//
// The newest wins, because it is the restart the operator triggered last. The
// rest are returned rather than discarded — a launch reopens one orchestrator,
// so a second one that restarted around the same time is left mid-task, and
// that is something to say out loud rather than a file to quietly remove.
func consumeOrchestratorRestoreTargets(dir string, now time.Time) (orchestratorRestoreState, []orchestratorRestoreState) {
	fresh := make([]orchestratorRestoreState, 0, 4)
	for _, path := range pendingOrchestratorRestorePaths(dir) {
		state, ok := readAndClearOrchestratorRestoreTarget(path)
		if !ok || (state.SavedAtUnix > 0 && now.Sub(time.Unix(state.SavedAtUnix, 0)) > orchestratorRestoreMaxAge) {
			continue
		}
		fresh = append(fresh, state)
	}
	if len(fresh) == 0 {
		return orchestratorRestoreState{}, nil
	}
	// Sorted by id first so hand-offs staged within the same second still resolve
	// the same way on every launch, then by recency, which decides.
	sort.Slice(fresh, func(i, j int) bool {
		if fresh[i].SavedAtUnix != fresh[j].SavedAtUnix {
			return fresh[i].SavedAtUnix > fresh[j].SavedAtUnix
		}
		return fresh[i].OrchestratorID < fresh[j].OrchestratorID
	})
	return fresh[0], fresh[1:]
}

// pendingOrchestratorRestorePaths lists every hand-off staged for this launch,
// the one the previous release wrote to the shared slot included.
func pendingOrchestratorRestorePaths(dir string) []string {
	if dir == "" {
		return nil
	}
	paths := []string{legacyOrchestratorRestorePath(dir)}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return paths
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	return paths
}

func readAndClearOrchestratorRestoreTarget(path string) (orchestratorRestoreState, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return orchestratorRestoreState{}, false
	}
	_ = os.Remove(path)
	var state orchestratorRestoreState
	if err := json.Unmarshal(data, &state); err != nil {
		return orchestratorRestoreState{}, false
	}
	state.OrchestratorID = strings.TrimSpace(state.OrchestratorID)
	state.ResumePrompt = strings.TrimSpace(state.ResumePrompt)
	if state.OrchestratorID == "" {
		return orchestratorRestoreState{}, false
	}
	return state, true
}

// ResolveOrchestratorToReopen returns every orchestrator this launch should
// reopen: one that OWNS THE TERMINAL PANE (OrchestratorID), plus any others that
// were open too (AlsoReopen). The frontend calls it on boot (after loading the
// orchestrator list); for each entry it starts a session resuming the exact
// conversation this method names for it — the pane owner resumes the hand-off's
// named conversation when one is delivered, otherwise every entry (owner and
// AlsoReopen alike) resumes what resolveReopenSessionID resolves — so the
// operator lands back where they were, with everything else they had open still
// running behind it.
//
// Which orchestrator owns the pane is answered in one of two ways:
//
//   - A pending restart hand-off wins outright: it names the exact orchestrator
//     the operator just restarted from, it is the only source of a resume
//     prompt, and it is consumed as it is read so it fires once. A hand-off that
//     cannot be honored still makes that orchestrator the pane owner; it just
//     arrives idle, carrying the notice that says why nothing was continued. A
//     hand-off from an orchestrator this launch does not choose as pane owner at
//     all (a second restart racing the first) is named in the same notice
//     instead — it still comes back, but via AlsoReopen, not as the owner.
//   - Otherwise the durable record of what was open decides: the most recently
//     (re)started orchestrator owns the pane (see orchestrator_open_state.go for
//     why the record keeps that order), and it carries no prompt — so a plain
//     quit-and-relaunch, a crash or a reboot comes back to the same session,
//     idle, with nothing auto-run.
//
// Every other orchestrator the durable record names — everyone but the pane
// owner — is returned in AlsoReopen and comes back too, idle, so the tab strip
// and the live sessions agree instead of a tab surviving with no session behind
// it.
func (a *App) ResolveOrchestratorToReopen() relaunchTarget {
	state, notReopened := consumeOrchestratorRestoreTargets(a.deps.orchestratorRestoreDir, time.Now())
	var notices []orchestratorNotice
	notices = appendOrchestratorNotice(notices, orchestratorHandoffsNotReopenedNotice(notReopened))
	openEntries := readOpenOrchestrators(a.deps.orchestratorOpenPath)

	if state.OrchestratorID == "" {
		if len(openEntries) == 0 {
			return relaunchTarget{Notices: notices}
		}
		owner := openEntries[len(openEntries)-1]
		alsoRefs, alsoNotices := a.reopenRefs(removeOrchestratorEntry(openEntries, owner.OrchestratorID))
		conversationID, conversationNotice := a.resolveReopenSessionID(owner)
		notices = appendOrchestratorNotice(notices, orchestratorScopeChangedNoticeIfAny(owner, a.currentOrchestratorScope(owner.OrchestratorID)))
		notices = append(notices, alsoNotices...)
		notices = appendOrchestratorNotice(notices, conversationNotice)
		return relaunchTarget{
			OrchestratorID: owner.OrchestratorID,
			ConversationID: conversationID,
			AlsoReopen:     alsoRefs,
			Notices:        notices,
		}
	}

	alsoRefs, alsoNotices := a.reopenRefs(removeOrchestratorEntry(openEntries, state.OrchestratorID))
	notices = append(notices, alsoNotices...)
	target := relaunchTarget{
		OrchestratorID: state.OrchestratorID,
		AlsoReopen:     alsoRefs,
	}
	entry := orchestratorEntryOrEmpty(openEntries, state.OrchestratorID)
	if state.ResumePrompt != "" {
		refusal := a.resumeRefusal(state)
		if refusal.Text == "" {
			target.ConversationID = state.ConversationID
			target.ResumePrompt = state.ResumePrompt
			target.Notices = notices
			return target
		}
		// The refusal above already covers a scope mismatch (it compares the
		// hand-off's own recorded scope against the orchestrator's current one),
		// so the durable-record scope check below is skipped here to avoid saying
		// the same thing twice.
		notices = appendOrchestratorNotice(notices, refusal)
	} else {
		notices = appendOrchestratorNotice(notices, orchestratorScopeChangedNoticeIfAny(entry, a.currentOrchestratorScope(state.OrchestratorID)))
	}
	// No prompt to deliver, or the hand-off was refused: still resume the exact
	// conversation this orchestrator is on, rather than leaving it for a
	// re-derivation that can land on a different, older one.
	conversationID, conversationNotice := a.resolveReopenSessionID(entry)
	target.ConversationID = conversationID
	notices = appendOrchestratorNotice(notices, conversationNotice)
	target.Notices = notices
	return target
}

// resolveReopenSessionID decides which conversation a reopened orchestrator
// resumes, and what has to be said about it: the conversation the operator
// attached, else the anchor derived from its id — never the one this
// orchestrator's own session last reported, which stays available only in the
// Manage dialog (see orchestrator_live_conversation.go). A fall-through to the
// anchor that diverged from that reported conversation is reported, and is
// still not what gets resumed: see orchestratorAnchorDivergenceNotice. A
// transient orchestrator has no id to derive from and gets a fresh
// conversation.
func (a *App) resolveReopenSessionID(entry orchestratorOpenEntry) (string, orchestratorNotice) {
	if strings.TrimSpace(entry.OrchestratorID) == "" {
		return uuid.NewString(), orchestratorNotice{}
	}
	choice := a.resolveOrchestratorConversation(entry)
	if choice.Notice == "" {
		return choice.ConversationID, orchestratorNotice{}
	}
	return choice.ConversationID, orchestratorNotice{
		OrchestratorID: entry.OrchestratorID,
		Kind:           orchestratorNoticeWarning,
		Text:           choice.Notice,
	}
}

// orchestratorEntryOrEmpty finds id's entry in entries, or a zero-value entry
// (no recorded session) when it is not there.
func orchestratorEntryOrEmpty(entries []orchestratorOpenEntry, id string) orchestratorOpenEntry {
	for _, entry := range entries {
		if entry.OrchestratorID == id {
			return entry
		}
	}
	return orchestratorOpenEntry{OrchestratorID: id}
}

// removeOrchestratorEntry returns entries without id, preserving order.
func removeOrchestratorEntry(entries []orchestratorOpenEntry, id string) []orchestratorOpenEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]orchestratorOpenEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.OrchestratorID != id {
			out = append(out, entry)
		}
	}
	return out
}

// reopenRefs resolves each entry's conversation the same way the pane owner's
// is, so every orchestrator AlsoReopen names arrives at the exact conversation
// that was really running for it rather than one the frontend would otherwise
// have to derive. It also collects a scope-changed notice for any entry whose
// recorded environments no longer match: restoring every open orchestrator
// means an AlsoReopen entry is exactly the no-note, no-task case a re-scoped
// id can silently resume into, so it gets the same scope check as the pane
// owner rather than a silent pass.
func (a *App) reopenRefs(entries []orchestratorOpenEntry) ([]orchestratorReopenRef, []orchestratorNotice) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]orchestratorReopenRef, 0, len(entries))
	var notices []orchestratorNotice
	for _, entry := range entries {
		conversationID, conversationNotice := a.resolveReopenSessionID(entry)
		out = append(out, orchestratorReopenRef{
			OrchestratorID: entry.OrchestratorID,
			ConversationID: conversationID,
		})
		notices = appendOrchestratorNotice(notices, conversationNotice)
		notices = appendOrchestratorNotice(notices, orchestratorScopeChangedNoticeIfAny(entry, a.currentOrchestratorScope(entry.OrchestratorID)))
	}
	return out, notices
}

// currentOrchestratorScope reads an orchestrator's scope as configured right
// now. An error (deleted, unreadable config) reads as an empty scope, which is
// a mismatch against any entry that recorded a non-empty one — the same
// conservative default resolveReopenSessionID already applies to a stale or
// absent session id.
func (a *App) currentOrchestratorScope(id string) []string {
	scope, err := a.orchestratorScope(id)
	if err != nil {
		return nil
	}
	return scope
}

// orchestratorScopeChangedNoticeIfAny reports, for one reopened entry, that its
// recorded scope no longer matches the orchestrator's current one — or a zero
// value when it does, or when the entry predates scope recording (Environments
// empty, read as unknown rather than a guaranteed match). A caution to verify
// stale context rather than a fault, so it reports as a warning.
func orchestratorScopeChangedNoticeIfAny(entry orchestratorOpenEntry, currentScope []string) orchestratorNotice {
	if len(entry.Environments) == 0 || equalOrchestratorScope(entry.Environments, currentScope) {
		return orchestratorNotice{}
	}
	return orchestratorNotice{
		OrchestratorID: entry.OrchestratorID,
		Kind:           orchestratorNoticeWarning,
		Text:           orchestratorScopeChangedNotice(entry.OrchestratorID, entry.Environments, currentScope),
	}
}

// orchestratorScopeChangedNotice explains that a reopened orchestrator's
// recorded conversation carries context for a scope it is no longer wired to.
// It still resumes that conversation idle — see resolveReopenSessionID — so
// this is the only thing that will tell the operator the environments
// underneath it moved before they treat it as current.
func orchestratorScopeChangedNotice(id string, was, now []string) string {
	return fmt.Sprintf("Reopened %s: its environments changed since its last session (was %s, now %s). "+
		"Its conversation may hold context for environments it is no longer wired to; check %s before treating it as current.",
		id, describeOrchestratorScope(was), describeOrchestratorScope(now), orchestratorReturnNoteName(id))
}

// orchestratorHandoffsNotReopenedNotice names the orchestrators that restarted
// mid-task and are not being reopened. Their work is only recoverable if the
// operator learns which sessions stopped and which note each left behind. It
// names several orchestrators at once, so it carries no single OrchestratorID;
// each of those sessions being left mid-task is not the healthy case, so this
// reports as a warning.
func orchestratorHandoffsNotReopenedNotice(states []orchestratorRestoreState) orchestratorNotice {
	if len(states) == 0 {
		return orchestratorNotice{}
	}
	described := make([]string, 0, len(states))
	for _, state := range states {
		described = append(described, fmt.Sprintf("%s (%s)", state.OrchestratorID, orchestratorReturnNoteName(state.OrchestratorID)))
	}
	sort.Strings(described)
	return orchestratorNotice{
		Kind: orchestratorNoticeWarning,
		Text: fmt.Sprintf("Also restarted mid-task but not reopened: %s. "+
			"A launch reopens one orchestrator, so start each of these and have it read its return note "+
			"in the orchestrators working directory.", strings.Join(described, ", ")),
	}
}

// resumeRefusal reports why a restart hand-off must not be delivered, or a zero
// value when it may. A resume prompt is a first turn telling the woken session
// to CONTINUE, so it is only safe against the conversation that asked for it,
// in the scope that conversation knew — an id alone names neither.
func (a *App) resumeRefusal(state orchestratorRestoreState) orchestratorNotice {
	if state.ConversationID == "" {
		return orchestratorResumeRefusedNotice(state.OrchestratorID,
			"the conversation that asked for it was not identified")
	}
	if !orchestratorSessionExists(state.ConversationID) {
		return orchestratorResumeRefusedNotice(state.OrchestratorID,
			"the conversation that asked for it no longer exists")
	}
	current, err := a.orchestratorScope(state.OrchestratorID)
	if err != nil {
		return orchestratorResumeRefusedNotice(state.OrchestratorID,
			"its environments could not be read back: "+err.Error())
	}
	if !equalOrchestratorScope(current, state.Environments) {
		return orchestratorResumeRefusedNotice(state.OrchestratorID, fmt.Sprintf(
			"its environments changed (was %s, now %s)",
			describeOrchestratorScope(state.Environments), describeOrchestratorScope(current)))
	}
	return orchestratorNotice{}
}

// orchestratorResumeRefusedNotice is what the operator reads when a restart
// hand-off was withheld. It names the orchestrator, the reason, and where the
// unfinished work is described, because the session came back idle and nothing
// else will say so. A refusal is always a warning: something the operator
// asked for could not be honoured.
func orchestratorResumeRefusedNotice(id, reason string) orchestratorNotice {
	return orchestratorNotice{
		OrchestratorID: id,
		Kind:           orchestratorNoticeWarning,
		Text: fmt.Sprintf("Reopened %s without continuing its task: %s. "+
			"Check %s in the orchestrators working directory before telling it to carry on.",
			id, reason, orchestratorReturnNoteName(id)),
	}
}

// describeOrchestratorScope renders an environment set for an operator-facing
// notice, naming the empty set rather than rendering nothing.
func describeOrchestratorScope(scope []string) string {
	if len(scope) == 0 {
		return "no environments"
	}
	return strings.Join(scope, ", ")
}

func equalOrchestratorScope(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// RestartApp persists what the next launch should return to, launches a fresh
// desktop instance, then quits this one. Spawn-before-quit because a process
// cannot spawn after asking Wails to quit; the two instances briefly coexist,
// which is safe (no SingleInstanceLock). A headless/no-ctx build has no Wails
// window to quit, so that step no-ops there.
//
// The quit is asked for with the close already confirmed. Wails runs
// OnBeforeClose on its way out and abandons the quit outright when that returns
// true (beforeClose does, for as long as any activity is running — which is
// exactly when an operator restarts to pick up a rebuild). Left to the gate,
// the predecessor never exits: it keeps the control record and the control
// port, the successor waits on a process that is not going away, and the
// operator is left with two desktops while the one in front of them is still
// the old binary. A restart has already written its hand-off and launched its
// successor, so it is the operator's explicit decision rather than the
// accidental window close the gate exists to catch, and it confirms the close
// exactly as ConfirmWindowClose does.
func (a *App) RestartApp(returnToOrchestratorID string) error {
	if err := writeOrchestratorRestoreTarget(a.deps.orchestratorRestoreDir, a.restartHandoff(returnToOrchestratorID), time.Now()); err != nil {
		return fmt.Errorf("persist restart target: %w", err)
	}
	relaunch := a.deps.relaunchApp
	if relaunch == nil {
		relaunch = relaunchDesktopAppDetached
	}
	if err := relaunch(); err != nil {
		return fmt.Errorf("relaunch desktop app: %w", err)
	}
	a.markCloseConfirmed()
	if a.deps.quitApp != nil {
		a.deps.quitApp()
		return nil
	}
	if a.quitDesktopApp() {
		a.armRestartQuitStallWatch()
	}
	return nil
}

// restartQuitStallGrace bounds how long a restart that has already asked this
// process to quit waits for that quit to land before it ends the process
// itself. A restart is a hand-off this process has already completed — the
// successor is launched and the resume hand-off is written before the quit is
// asked for — so the only thing left for it to do is disappear, and a
// predecessor that stays is a real second desktop still holding the control
// record. The grace is generous because it is a backstop, not a schedule: a
// quit that lands takes milliseconds.
const restartQuitStallGrace = 15 * time.Second

// armRestartQuitStallWatch starts the restart's last resort. It is armed only
// when this process actually asked the platform to quit it, because that is
// the only case where "still running" means the quit did not land.
func (a *App) armRestartQuitStallWatch() {
	reason := fmt.Sprintf(
		"erun-app: restart asked this process to quit and it is still running %s later; exiting so the relaunched desktop is the only one left",
		restartQuitStallGrace)
	go restartQuitStallWatch(restartQuitStallGrace, time.Sleep, exitForStalledRestartQuit, reason)
}

// restartQuitStallWatch ends this process when the quit a restart asked for
// never landed. Reaching the end of the wait is itself the evidence: a process
// whose quit worked is not here to observe it. sleep and exit are supplied so
// the escalation can be witnessed without ending the process running the test.
func restartQuitStallWatch(stallGrace time.Duration, sleep func(time.Duration), exit func(string), reason string) {
	sleep(stallGrace)
	exit(reason)
}

// exitForStalledRestartQuit ends a process whose own quit could not complete.
// It records why before it goes, so the escalation is legible rather than a
// desktop that vanished.
func exitForStalledRestartQuit(reason string) {
	log.Print(reason)
	os.Exit(0)
}

// restartHandoff describes what the next launch comes back to. The resume prompt
// is recorded only alongside the conversation that is live right now, together
// with the scope it is wired to: without those, a restart has nothing it can
// safely tell to carry on, so the launch reopens the orchestrator idle rather
// than handing a task to whichever conversation its id happens to resolve to.
//
// The conversation is the one the session running right now reports being on --
// what it told its own hooks under this launch's nonce, and only failing that
// the id it was spawned with. A restart is the operator taking a live session
// away and promising it back, so naming anything other than the conversation
// that is live is how a restart strands the work it was meant to preserve.
//
// "Running right now" is this desktop's session when it has one. When it does
// not -- an orchestrator started in a terminal, or one whose session object is
// gone -- the same answer is still on disk, and the durable open-set entry plus
// the live-conversation record are what carry it (see
// restartHandoffFromOpenState). Leaving that case empty is what used to send the
// next launch to the derived anchor instead of the conversation that was really
// live, silently.
func (a *App) restartHandoff(orchestratorID string) orchestratorRestoreState {
	state := orchestratorRestoreState{OrchestratorID: strings.TrimSpace(orchestratorID)}
	conversationID, launchID, scope := a.runningOrchestratorConversation(state.OrchestratorID)
	if conversationID == "" {
		return a.restartHandoffFromOpenState(state)
	}
	state.ConversationID = orchestratorLiveConversationForLaunch(state.OrchestratorID, launchID, conversationID)
	state.Environments = scope
	state.ResumePrompt = orchestratorRestartResumePromptFor(orchestratorsRoot(), state.OrchestratorID)
	return state
}

// restartHandoffFromOpenState answers restartHandoff's question for an
// orchestrator this desktop holds no session for, from what is durable instead
// of from memory: the open-set entry records the nonce of the launch that last
// started it and the scope that launch was wired to, and the live-conversation
// record holds the conversation that session reported being on.
//
// The confirmation is the same one every other path uses and is the whole
// safety of this: orchestratorLiveConversationForLaunch only trusts a record
// whose echoed nonce matches the launch the entry names, so a record left by a
// replaced run, or by a writer that no longer exists, cannot decide what this
// restart hands a task to. An entry that names no launch, or a record that does
// not confirm, leaves the hand-off empty exactly as before -- the next launch
// then resumes the derived anchor idle, which is the honest outcome when
// nothing here can vouch for what was running.
func (a *App) restartHandoffFromOpenState(state orchestratorRestoreState) orchestratorRestoreState {
	entry := orchestratorEntryOrEmpty(readOpenOrchestrators(a.deps.orchestratorOpenPath), state.OrchestratorID)
	if strings.TrimSpace(entry.LaunchID) == "" {
		return state
	}
	// The empty fallback is deliberate: with nothing confirmed there is no
	// conversation this hand-off could name, and the next launch resumes the
	// anchor idle rather than a task being handed to a guess.
	conversationID := orchestratorLiveConversationForLaunch(state.OrchestratorID, entry.LaunchID, "")
	if conversationID == "" {
		return state
	}
	state.ConversationID = conversationID
	state.Environments = entry.Environments
	state.ResumePrompt = orchestratorRestartResumePromptFor(orchestratorsRoot(), state.OrchestratorID)
	return state
}

// orchestratorRestartPreview answers what a restart triggered right now would
// reopen, per orchestrator, WITHOUT restarting anything: no hand-off is written,
// nothing is launched, nothing quits. It reads the same state the launch that
// follows will read, and resolves each orchestrator the same way that launch
// resolves it, so the warning it produces and the notice the next launch raises
// cannot disagree about which conversation was left behind.
//
// It exists because of WHEN the next launch says anything. By the time the
// post-restart notice renders, the restart has happened: the orchestrator is
// already on its anchor and the only remedy left is to attach the stranded
// conversation and restart again. Asked before the restart, the same answer is
// actionable — attach first, then restart once.
//
// One orchestration is answered by the hand-off rather than by a resolution:
// the orchestrator the restart is resuming gets its own live conversation back
// (see restartHandoff), so it is reported coming back to that, and a hand-off
// that cannot be delivered falls back onto the same attached-or-derived
// resolution every other orchestrator gets.
func (a *App) orchestratorRestartPreview(returnToOrchestratorID string) []eruncommon.DesktopRestartReopen {
	entries := readOpenOrchestrators(a.deps.orchestratorOpenPath)
	handoff := a.restartHandoff(returnToOrchestratorID)
	// The same predicate the launch itself applies to the hand-off it finds on
	// disk, reused rather than re-derived: a hand-off that would be withheld on
	// the way back is not what this orchestrator comes back on either.
	delivered := handoff.ConversationID != "" && a.resumeRefusal(handoff).Text == ""

	ids := make([]string, 0, len(entries)+1)
	for _, entry := range entries {
		ids = append(ids, entry.OrchestratorID)
	}
	// A restart naming an orchestrator that is not in the open set still reopens
	// it — the hand-off makes it the pane owner on its own — so leaving it out of
	// the plan would be the one silence this is meant to end.
	if handoff.OrchestratorID != "" && !containsOrchestratorID(ids, handoff.OrchestratorID) {
		ids = append(ids, handoff.OrchestratorID)
	}

	out := make([]eruncommon.DesktopRestartReopen, 0, len(ids))
	for _, id := range ids {
		if delivered && id == handoff.OrchestratorID {
			out = append(out, eruncommon.DesktopRestartReopen{
				OrchestratorID: id,
				ConversationID: handoff.ConversationID,
			})
			continue
		}
		choice := a.resolveOrchestratorConversation(orchestratorEntryOrEmpty(entries, id))
		out = append(out, eruncommon.DesktopRestartReopen{
			OrchestratorID: id,
			ConversationID: choice.ConversationID,
			Notice:         choice.Notice,
		})
	}
	return out
}

// containsOrchestratorID reports whether ids already names id.
func containsOrchestratorID(ids []string, id string) bool {
	for _, existing := range ids {
		if existing == id {
			return true
		}
	}
	return false
}

// relaunchDesktopAppDetached spawns a fresh copy of this desktop binary/bundle,
// detached so it survives this process exiting. It reuses the shared
// eruncommon.DesktopAppCommand so the launch matches `erun app`.
func relaunchDesktopAppDetached() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := eruncommon.DesktopAppCommand(goruntime.GOOS, resolveDesktopSelfPath(executable), nil)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// resolveDesktopSelfPath maps this running binary to the artifact
// DesktopAppCommand should launch: on macOS the enclosing .app bundle
// (…/ERun.app/Contents/MacOS/erun-app -> …/ERun.app); elsewhere the binary itself.
func resolveDesktopSelfPath(executable string) string {
	if goruntime.GOOS == "darwin" {
		bundle := filepath.Clean(filepath.Join(filepath.Dir(executable), "..", ".."))
		if filepath.Ext(bundle) == ".app" {
			return bundle
		}
	}
	return executable
}

// quitDesktopApp asks Wails to end this process, reporting whether it had a
// window to ask at all. A headless or not-yet-started app has none, and a
// caller that escalates on a stalled quit must not arm that escalation for a
// quit nobody was ever asked for.
func (a *App) quitDesktopApp() bool {
	if a.ctx == nil {
		return false
	}
	wailsruntime.Quit(a.ctx)
	return true
}
