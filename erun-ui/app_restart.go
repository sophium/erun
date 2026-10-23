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

	"github.com/sophium/erun/erun-common/base/core"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// A desktop restart does not interrupt an orchestrator: its Claude background
// agent keeps running, mid-turn included, while the desktop's terminal on it
// goes away and comes back. So a restart hands nothing to the next launch but
// which orchestrator the operator restarted from, which then owns the terminal
// pane; every orchestrator that was open reattaches to its agent.

// orchestratorRestoreDirName holds one hand-off slot per orchestrator, so two
// restarts close together cannot overwrite each other's.
const orchestratorRestoreDirName = "orchestrator-restore"

// legacyOrchestratorRestoreFileName is the single shared slot restarts wrote
// before the per-orchestrator slots. It is still read once, because the restart
// that first installs a newer release is staged by the binary that had only
// this one.
const legacyOrchestratorRestoreFileName = "orchestrator-restore.json"

// orchestratorRestoreMaxAge bounds how long a restart hand-off stays valid: it
// names the orchestrator the operator was looking at when they restarted, which
// says nothing about a launch hours later. Which orchestrators to reopen is
// durable session state and is NOT bound this way — see
// orchestrator_open_state.go.
const orchestratorRestoreMaxAge = 10 * time.Minute

type orchestratorRestoreState struct {
	OrchestratorID string `json:"orchestratorId"`
	SavedAtUnix    int64  `json:"savedAtUnix"`
}

// relaunchTarget is the JSON-safe view the frontend reads on boot.
// OrchestratorID is the one that OWNS THE TERMINAL PANE — the pane is single,
// so exactly one orchestrator gets it. AlsoReopen lists every other
// orchestrator that was open and comes back too.
type relaunchTarget struct {
	OrchestratorID string                  `json:"orchestratorId"`
	AlsoReopen     []orchestratorReopenRef `json:"alsoReopen,omitempty"`
}

// orchestratorReopenRef is one orchestrator in AlsoReopen.
type orchestratorReopenRef struct {
	OrchestratorID string `json:"orchestratorId"`
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

// consumeOrchestratorRestoreTarget reads and deletes every pending hand-off and
// answers with the newest, the restart the operator triggered last. Deleting
// on read keeps each one-shot: honored on the next boot and never again.
func consumeOrchestratorRestoreTarget(dir string, now time.Time) orchestratorRestoreState {
	fresh := make([]orchestratorRestoreState, 0, 4)
	for _, path := range pendingOrchestratorRestorePaths(dir) {
		state, ok := readAndClearOrchestratorRestoreTarget(path)
		if !ok || (state.SavedAtUnix > 0 && now.Sub(time.Unix(state.SavedAtUnix, 0)) > orchestratorRestoreMaxAge) {
			continue
		}
		fresh = append(fresh, state)
	}
	if len(fresh) == 0 {
		return orchestratorRestoreState{}
	}
	// Sorted by id first so hand-offs staged within the same second still resolve
	// the same way on every launch, then by recency, which decides.
	sort.Slice(fresh, func(i, j int) bool {
		if fresh[i].SavedAtUnix != fresh[j].SavedAtUnix {
			return fresh[i].SavedAtUnix > fresh[j].SavedAtUnix
		}
		return fresh[i].OrchestratorID < fresh[j].OrchestratorID
	})
	return fresh[0]
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
	if state.OrchestratorID == "" {
		return orchestratorRestoreState{}, false
	}
	return state, true
}

// ResolveOrchestratorToReopen returns every orchestrator this launch should
// reopen: one that OWNS THE TERMINAL PANE (OrchestratorID), plus any others that
// were open too (AlsoReopen). The frontend calls it on boot and starts each,
// which reattaches it to its background agent.
//
// A pending restart hand-off decides the pane owner: it names the exact
// orchestrator the operator just restarted from, and it is consumed as it is
// read so it fires once. Otherwise the most recently (re)started orchestrator
// in the durable open set owns the pane (see orchestrator_open_state.go for why
// the record keeps that order).
func (a *App) ResolveOrchestratorToReopen() relaunchTarget {
	state := consumeOrchestratorRestoreTarget(a.deps.orchestratorRestoreDir, time.Now())
	openEntries := readOpenOrchestrators(a.deps.orchestratorOpenPath)
	owner := state.OrchestratorID
	if owner == "" {
		if len(openEntries) == 0 {
			return relaunchTarget{}
		}
		owner = openEntries[len(openEntries)-1].OrchestratorID
	}
	target := relaunchTarget{OrchestratorID: owner}
	for _, entry := range openEntries {
		if entry.OrchestratorID != owner {
			target.AlsoReopen = append(target.AlsoReopen, orchestratorReopenRef{OrchestratorID: entry.OrchestratorID})
		}
	}
	return target
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
	if err := writeOrchestratorRestoreTarget(a.deps.orchestratorRestoreDir, orchestratorRestoreState{OrchestratorID: strings.TrimSpace(returnToOrchestratorID)}, time.Now()); err != nil {
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

// orchestratorRestartPreview answers what a restart triggered right now would
// reopen, per orchestrator, WITHOUT restarting anything: no hand-off is written,
// nothing is launched, nothing quits. Every orchestrator reattaches to its own
// background agent, so nothing is left behind by the restart itself; what can
// still go wrong is a launch the next desktop would refuse, and that is the
// one thing worth saying before the restart rather than after it.
func (a *App) orchestratorRestartPreview(returnToOrchestratorID string) []core.DesktopRestartReopen {
	entries := readOpenOrchestrators(a.deps.orchestratorOpenPath)
	ids := restartPreviewOrchestratorIDs(entries, strings.TrimSpace(returnToOrchestratorID))
	out := make([]core.DesktopRestartReopen, 0, len(ids))
	for _, id := range ids {
		out = append(out, core.DesktopRestartReopen{
			OrchestratorID: id,
			Notice:         a.restartLaunchRefusal(id),
		})
	}
	return out
}

// restartPreviewOrchestratorIDs is every orchestrator a restart reopens: the
// open set in its recorded order, then the hand-off's own orchestrator. A
// restart naming one that is not in the open set still reopens it -- the
// hand-off makes it the pane owner on its own -- so leaving it out of the plan
// would be the one silence the preview is meant to end.
func restartPreviewOrchestratorIDs(entries []orchestratorOpenEntry, handoffID string) []string {
	ids := make([]string, 0, len(entries)+1)
	for _, entry := range entries {
		ids = append(ids, entry.OrchestratorID)
	}
	if handoffID != "" && !containsOrchestratorID(ids, handoffID) {
		ids = append(ids, handoffID)
	}
	return ids
}

// restartLaunchRefusal answers, before a restart, whether the launch that
// follows would refuse to reopen id -- resolved exactly as that start resolves
// it, credential included -- and what to do about it. "" when it would reopen,
// or when id names no persisted orchestrator, which no launch would resolve.
// The agent itself keeps running either way; a refused reopen only leaves the
// desktop without a terminal on it.
func (a *App) restartLaunchRefusal(id string) string {
	if id == "" {
		return ""
	}
	def, err := a.findOrchestratorConfig(id)
	if err != nil {
		return ""
	}
	if _, err := a.resolvePersistedOrchestratorLaunch(def); err != nil {
		return fmt.Sprintf("The restart will not reopen %s: %v. Its background agent keeps running; fix that and open it again.", id, err)
	}
	return ""
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
	cmd := core.DesktopAppCommand(goruntime.GOOS, resolveDesktopSelfPath(executable), nil)
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
