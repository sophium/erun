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

// orchestratorRestoreMaxAge bounds how long a persisted restore target stays
// valid, so a deliberate restart restores the orchestrator while a plain launch
// long afterwards never resurrects a stale one.
const orchestratorRestoreMaxAge = 10 * time.Minute

type orchestratorRestoreState struct {
	OrchestratorID string `json:"orchestratorId"`
	SavedAtUnix    int64  `json:"savedAtUnix"`
	// ResumePrompt, when set, is handed to the resumed Claude session so it
	// continues its task itself after a rebuild+restart instead of idling.
	ResumePrompt string `json:"resumePrompt,omitempty"`
}

// relaunchTarget is the JSON-safe view the frontend reads on boot: which
// orchestrator to reopen and, when set, the prompt to auto-run on resume.
type relaunchTarget struct {
	OrchestratorID string `json:"orchestratorId"`
	ResumePrompt   string `json:"resumePrompt"`
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

func writeOrchestratorRestoreTarget(path, orchestratorID, resumePrompt string, now time.Time) error {
	orchestratorID = strings.TrimSpace(orchestratorID)
	if path == "" || orchestratorID == "" {
		return nil
	}
	data, err := json.Marshal(orchestratorRestoreState{
		OrchestratorID: orchestratorID,
		SavedAtUnix:    now.Unix(),
		ResumePrompt:   strings.TrimSpace(resumePrompt),
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// consumeOrchestratorRestoreTarget reads and deletes the restore file, returning
// the target orchestrator id only when it is fresh. Deleting on read makes the
// restore one-shot: honored on the next boot and never again.
func consumeOrchestratorRestoreTarget(path string, now time.Time) relaunchTarget {
	if path == "" {
		return relaunchTarget{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return relaunchTarget{}
	}
	_ = os.Remove(path)
	var state orchestratorRestoreState
	if err := json.Unmarshal(data, &state); err != nil {
		return relaunchTarget{}
	}
	if state.SavedAtUnix > 0 && now.Sub(time.Unix(state.SavedAtUnix, 0)) > orchestratorRestoreMaxAge {
		return relaunchTarget{}
	}
	id := strings.TrimSpace(state.OrchestratorID)
	if id == "" {
		return relaunchTarget{}
	}
	return relaunchTarget{OrchestratorID: id, ResumePrompt: strings.TrimSpace(state.ResumePrompt)}
}

// ConsumeRelaunchTarget returns the orchestrator a restart asked to reopen (and
// any prompt to auto-run on resume), clearing it so it fires once. The frontend
// calls this on boot (after loading the orchestrator list) and, if the id still
// resolves to a persisted orchestrator, re-starts it — spawnOrchestratorSession's
// `claude --continue` resumes the same conversation, so the operator lands back
// where they were. When ResumePrompt is set, the resumed session runs it
// immediately so a rebuild+restart continues its task itself.
func (a *App) ConsumeRelaunchTarget() relaunchTarget {
	return consumeOrchestratorRestoreTarget(a.deps.orchestratorRestorePath, time.Now())
}

// RestartApp persists the orchestrator to return to, launches a fresh desktop
// instance, then quits this one. Spawn-before-quit because a process cannot
// spawn after asking Wails to quit; the two instances briefly coexist, which is
// safe (no SingleInstanceLock). A headless/no-ctx build has no Wails window to
// quit, so that step no-ops there.
func (a *App) RestartApp(returnToOrchestratorID string) error {
	if err := writeOrchestratorRestoreTarget(a.deps.orchestratorRestorePath, returnToOrchestratorID, "", time.Now()); err != nil {
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
