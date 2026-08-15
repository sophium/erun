package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const orchestratorRestoreFileName = "orchestrator-restore.json"

// orchestratorRestoreMaxAge bounds how long the restart hand-off stays valid.
// It is deliberately short because what the hand-off carries is a task to run
// unattended: a rebuild+restart should continue its work, but a prompt fired at
// a launch hours later would run against a world the operator has moved on
// from. Which orchestrator to reopen is durable session state and is NOT bound
// this way — see orchestrator_open_state.go for why the two are separate.
const orchestratorRestoreMaxAge = 10 * time.Minute

// orchestratorRestartResumePrompt is what a rebuild+restart hands the resumed
// conversation. It carries no task of its own on purpose: the task lives in the
// return note the orchestrator wrote in its working directory before triggering
// the restart, because a conversation does not survive the restart and a file
// does. The prompt only has to put the session back on that note.
const orchestratorRestartResumePrompt = "The desktop just restarted to pick up a rebuild. " +
	"Read the return note you wrote in this working directory, confirm the rebuilt code is live in the running process, " +
	"and carry that task through to its verified end without waiting to be asked."

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

// relaunchTarget is the JSON-safe view the frontend reads on boot: which
// orchestrator to reopen, which of its conversations, the prompt to auto-run on
// resume, and — when the hand-off was refused — why nothing is being continued.
type relaunchTarget struct {
	OrchestratorID string `json:"orchestratorId"`
	ConversationID string `json:"conversationId"`
	ResumePrompt   string `json:"resumePrompt"`
	Notice         string `json:"notice"`
}

// defaultOrchestratorRestorePath is the sibling of window-state.json under
// UserConfigDir()/ERun where a restart records the orchestrator to reopen.
func defaultOrchestratorRestorePath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "ERun", orchestratorRestoreFileName)
}

func writeOrchestratorRestoreTarget(path string, state orchestratorRestoreState, now time.Time) error {
	state.OrchestratorID = strings.TrimSpace(state.OrchestratorID)
	state.ResumePrompt = strings.TrimSpace(state.ResumePrompt)
	state.SavedAtUnix = now.Unix()
	if path == "" || state.OrchestratorID == "" {
		return nil
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// consumeOrchestratorRestoreTarget reads and deletes the restore file, returning
// the hand-off only when it is fresh. Deleting on read makes the restore
// one-shot: honored on the next boot and never again.
func consumeOrchestratorRestoreTarget(path string, now time.Time) (orchestratorRestoreState, bool) {
	if path == "" {
		return orchestratorRestoreState{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return orchestratorRestoreState{}, false
	}
	_ = os.Remove(path)
	var state orchestratorRestoreState
	if err := json.Unmarshal(data, &state); err != nil {
		return orchestratorRestoreState{}, false
	}
	if state.SavedAtUnix > 0 && now.Sub(time.Unix(state.SavedAtUnix, 0)) > orchestratorRestoreMaxAge {
		return orchestratorRestoreState{}, false
	}
	state.OrchestratorID = strings.TrimSpace(state.OrchestratorID)
	state.ResumePrompt = strings.TrimSpace(state.ResumePrompt)
	if state.OrchestratorID == "" {
		return orchestratorRestoreState{}, false
	}
	return state, true
}

// ResolveOrchestratorToReopen returns the orchestrator this launch should reopen,
// which of its conversations, and any prompt to auto-run on resume. The frontend
// calls it on boot (after loading the orchestrator list) and, if the id still
// resolves to a persisted orchestrator, starts it — resuming the named
// conversation, or that orchestrator's own pinned one when none is named, so the
// operator lands back where they were.
//
// A pending restart hand-off wins: it is the more specific intent and the only
// source of a resume prompt, and it is consumed as it is read so it fires once.
// Otherwise the durable record of what was open answers, carrying no prompt — so
// a plain quit-and-relaunch, a crash or a reboot comes back to the same
// orchestrator, idle at its prompt with nothing auto-run.
//
// A hand-off that cannot be honored still reopens the orchestrator; it just
// arrives idle, carrying the notice that says why nothing was continued.
func (a *App) ResolveOrchestratorToReopen() relaunchTarget {
	state, ok := consumeOrchestratorRestoreTarget(a.deps.orchestratorRestorePath, time.Now())
	if !ok {
		return relaunchTarget{OrchestratorID: readOpenOrchestrator(a.deps.orchestratorOpenPath)}
	}
	target := relaunchTarget{OrchestratorID: state.OrchestratorID}
	if state.ResumePrompt == "" {
		return target
	}
	if notice := a.resumeRefusal(state); notice != "" {
		target.Notice = notice
		return target
	}
	target.ConversationID = state.ConversationID
	target.ResumePrompt = state.ResumePrompt
	return target
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
		"Check the return note in the orchestrator's working directory before telling it to carry on.", id, reason)
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
	if err := writeOrchestratorRestoreTarget(a.deps.orchestratorRestorePath, a.restartHandoff(returnToOrchestratorID), time.Now()); err != nil {
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
	state.ResumePrompt = orchestratorRestartResumePrompt
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
