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

// A restart does not interrupt an orchestrator's background agent; it only hands
// the next launch the orchestrator that owns the terminal pane.

// One hand-off slot per orchestrator, so close restarts cannot overwrite each other.
const orchestratorRestoreDirName = "orchestrator-restore"

// legacyOrchestratorRestoreFileName is still read: the first restart into a newer
// release is staged by a binary that only wrote this slot.
const legacyOrchestratorRestoreFileName = "orchestrator-restore.json"

// orchestratorRestoreMaxAge bounds a restart hand-off; the durable open set is not bound this way.
const orchestratorRestoreMaxAge = 10 * time.Minute

type orchestratorRestoreState struct {
	OrchestratorID string `json:"orchestratorId"`
	SavedAtUnix    int64  `json:"savedAtUnix"`
}

// relaunchTarget is what the frontend reads on boot. OrchestratorID owns the
// single terminal pane; AlsoReopen lists the others.
type relaunchTarget struct {
	OrchestratorID string                  `json:"orchestratorId"`
	AlsoReopen     []orchestratorReopenRef `json:"alsoReopen,omitempty"`
}

type orchestratorReopenRef struct {
	OrchestratorID string `json:"orchestratorId"`
}

func defaultOrchestratorRestoreDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "ERun", orchestratorRestoreDirName)
}

// orchestratorRestorePath returns "" for an id that is not a plain file name.
func orchestratorRestorePath(dir, orchestratorID string) string {
	id := strings.TrimSpace(orchestratorID)
	if dir == "" || id == "" || id != filepath.Base(id) || strings.HasPrefix(id, ".") {
		return ""
	}
	return filepath.Join(dir, id+".json")
}

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

// consumeOrchestratorRestoreTarget deletes every pending hand-off and returns the newest.
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
	// Sort by id first so same-second hand-offs resolve deterministically.
	sort.Slice(fresh, func(i, j int) bool {
		if fresh[i].SavedAtUnix != fresh[j].SavedAtUnix {
			return fresh[i].SavedAtUnix > fresh[j].SavedAtUnix
		}
		return fresh[i].OrchestratorID < fresh[j].OrchestratorID
	})
	return fresh[0]
}

// pendingOrchestratorRestorePaths includes the legacy shared slot.
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

// ResolveOrchestratorToReopen returns the pane owner plus every other open orchestrator.
// A pending hand-off decides the pane owner; otherwise the most recently started one does.
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

// RestartApp persists the hand-off, spawns a fresh instance, then quits this one
// (spawn-before-quit: a process cannot spawn after asking Wails to quit).
// The close is pre-confirmed: beforeClose would otherwise veto the quit while any
// activity runs, leaving the old binary holding the control record and port.
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

// restartQuitStallGrace is the backstop before a restart ends a process whose quit did not land.
const restartQuitStallGrace = 15 * time.Second

// armRestartQuitStallWatch is armed only when a quit was actually requested.
func (a *App) armRestartQuitStallWatch() {
	reason := fmt.Sprintf(
		"erun-app: restart asked this process to quit and it is still running %s later; exiting so the relaunched desktop is the only one left",
		restartQuitStallGrace)
	go restartQuitStallWatch(restartQuitStallGrace, time.Sleep, exitForStalledRestartQuit, reason)
}

// restartQuitStallWatch exits when the wait ends; a process whose quit worked never gets here.
func restartQuitStallWatch(stallGrace time.Duration, sleep func(time.Duration), exit func(string), reason string) {
	sleep(stallGrace)
	exit(reason)
}

// exitForStalledRestartQuit logs the reason before exiting.
func exitForStalledRestartQuit(reason string) {
	log.Print(reason)
	os.Exit(0)
}

// orchestratorRestartPreview reports, without restarting, which orchestrators the
// next launch would refuse to reopen.
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

// restartPreviewOrchestratorIDs is the open set plus the hand-off's orchestrator,
// which is reopened even when not in the open set.
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

// restartLaunchRefusal reports why the next launch would refuse to reopen id,
// resolved exactly as that start resolves it; "" when it would reopen or id is unknown.
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

func containsOrchestratorID(ids []string, id string) bool {
	for _, existing := range ids {
		if existing == id {
			return true
		}
	}
	return false
}

// relaunchDesktopAppDetached spawns a detached copy via core.DesktopAppCommand, like `erun app`.
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

// resolveDesktopSelfPath maps the binary to its enclosing .app bundle on macOS.
func resolveDesktopSelfPath(executable string) string {
	if goruntime.GOOS == "darwin" {
		bundle := filepath.Clean(filepath.Join(filepath.Dir(executable), "..", ".."))
		if filepath.Ext(bundle) == ".app" {
			return bundle
		}
	}
	return executable
}

// quitDesktopApp asks Wails to quit and reports whether there was a window to ask.
func (a *App) quitDesktopApp() bool {
	if a.ctx == nil {
		return false
	}
	wailsruntime.Quit(a.ctx)
	return true
}
