package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"time"

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
// conversation does not survive the restart and a file does.
//
// It names that note exactly. Orchestrators share one working directory, so
// "the note you wrote here" resolves to whichever note is there — and a session
// following it faithfully can pick up another orchestrator's agenda and carry it
// to a confident, wrong end.
func orchestratorRestartResumePrompt(orchestratorID string) string {
	return "The desktop just restarted to pick up a rebuild. " +
		"Read " + orchestratorReturnNoteName(orchestratorID) + " in this working directory — that one is yours, " +
		"and any other return note beside it belongs to a different orchestrator. " +
		"Confirm the rebuilt code is live in the running process, " +
		"and carry that task through to its verified end without waiting to be asked."
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

// relaunchTarget is the JSON-safe view the frontend reads on boot. OrchestratorID
// is the one this launch resumes and that OWNS THE TERMINAL PANE — the pane is
// single, so exactly one orchestrator gets it. AlsoReopen lists every other
// orchestrator that was open and comes back too, started idle alongside it with
// no conversation named and no prompt: only the pane-owning orchestrator can
// carry a resume prompt, by construction, because ConversationID/ResumePrompt
// are fields of this one target, not of each id in AlsoReopen. Notice explains,
// when non-empty, why the pane owner is not continuing a task it asked to.
type relaunchTarget struct {
	OrchestratorID string   `json:"orchestratorId"`
	ConversationID string   `json:"conversationId"`
	ResumePrompt   string   `json:"resumePrompt"`
	AlsoReopen     []string `json:"alsoReopen,omitempty"`
	Notice         string   `json:"notice"`
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
// orchestrator list); for each id that still resolves to a persisted
// orchestrator it starts a session, resuming that orchestrator's own pinned
// conversation — except the pane owner, which resumes the named conversation
// when the hand-off supplies one — so the operator lands back where they were,
// with everything else they had open still running behind it.
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
	notice := orchestratorHandoffsNotReopenedNotice(notReopened)
	openIDs := readOpenOrchestrators(a.deps.orchestratorOpenPath)

	if state.OrchestratorID == "" {
		if len(openIDs) == 0 {
			return relaunchTarget{Notice: notice}
		}
		owner := openIDs[len(openIDs)-1]
		return relaunchTarget{OrchestratorID: owner, AlsoReopen: removeOrchestratorID(openIDs, owner), Notice: notice}
	}

	target := relaunchTarget{
		OrchestratorID: state.OrchestratorID,
		AlsoReopen:     removeOrchestratorID(openIDs, state.OrchestratorID),
		Notice:         notice,
	}
	if state.ResumePrompt == "" {
		return target
	}
	if refusal := a.resumeRefusal(state); refusal != "" {
		target.Notice = joinOrchestratorNotices(refusal, notice)
		return target
	}
	target.ConversationID = state.ConversationID
	target.ResumePrompt = state.ResumePrompt
	return target
}

// removeOrchestratorID returns ids without id, preserving order.
func removeOrchestratorID(ids []string, id string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, existing := range ids {
		if existing != id {
			out = append(out, existing)
		}
	}
	return out
}

// orchestratorHandoffsNotReopenedNotice names the orchestrators that restarted
// mid-task and are not being reopened. Their work is only recoverable if the
// operator learns which sessions stopped and which note each left behind.
func orchestratorHandoffsNotReopenedNotice(states []orchestratorRestoreState) string {
	if len(states) == 0 {
		return ""
	}
	described := make([]string, 0, len(states))
	for _, state := range states {
		described = append(described, fmt.Sprintf("%s (%s)", state.OrchestratorID, orchestratorReturnNoteName(state.OrchestratorID)))
	}
	sort.Strings(described)
	return fmt.Sprintf("Also restarted mid-task but not reopened: %s. "+
		"A launch reopens one orchestrator, so start each of these and have it read its return note "+
		"in the orchestrators working directory.", strings.Join(described, ", "))
}

func joinOrchestratorNotices(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, " ")
}

// resumeRefusal reports why a restart hand-off must not be delivered, or "" when
// it may. A resume prompt is a first turn telling the woken session to CONTINUE,
// so it is only safe against the conversation that asked for it, in the scope
// that conversation knew — an id alone names neither.
func (a *App) resumeRefusal(state orchestratorRestoreState) string {
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
	return ""
}

// orchestratorResumeRefusedNotice is what the operator reads when a restart
// hand-off was withheld. It names the orchestrator, the reason, and where the
// unfinished work is described, because the session came back idle and nothing
// else will say so.
func orchestratorResumeRefusedNotice(id, reason string) string {
	return fmt.Sprintf("Reopened %s without continuing its task: %s. "+
		"Check %s in the orchestrators working directory before telling it to carry on.",
		id, reason, orchestratorReturnNoteName(id))
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
	if a.deps.quitApp != nil {
		a.deps.quitApp()
		return nil
	}
	a.quitDesktopApp()
	return nil
}

// restartHandoff describes what the next launch comes back to. The resume prompt
// is recorded only alongside the conversation that is live right now, together
// with the scope it is wired to: without those, a restart has nothing it can
// safely tell to carry on, so the launch reopens the orchestrator idle rather
// than handing a task to whichever conversation its id happens to resolve to.
func (a *App) restartHandoff(orchestratorID string) orchestratorRestoreState {
	state := orchestratorRestoreState{OrchestratorID: strings.TrimSpace(orchestratorID)}
	conversationID, scope := a.runningOrchestratorConversation(state.OrchestratorID)
	if conversationID == "" {
		return state
	}
	state.ConversationID = conversationID
	state.Environments = scope
	state.ResumePrompt = orchestratorRestartResumePrompt(state.OrchestratorID)
	return state
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

func (a *App) quitDesktopApp() {
	if a.ctx == nil {
		return
	}
	wailsruntime.Quit(a.ctx)
}
