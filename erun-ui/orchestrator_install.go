package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/command/podruntime"
	"github.com/sophium/erun/erun-ui/internal/terminal"
)

// buildSkillsSource is the build checkout's erun-skills/skills, stamped by the
// build scripts, so a bundle copied out of its checkout still finds its skills.
var buildSkillsSource = ""

// noSkillsSourceError names every layout tried, so a silently skipped install is visible.
type noSkillsSourceError struct {
	stamped string
	exeDir  string
}

func (e *noSkillsSourceError) Error() string {
	built := "this build records no source checkout"
	if e.stamped != "" {
		built = "its build checkout " + e.stamped + " is not on this machine"
	}
	near := "the executable's own directory"
	if e.exeDir != "" {
		near = e.exeDir
	}
	return "no erun skills source resolved: " + built + ", and no erun-skills/skills sits above " + near
}

// hostSkillsSource resolves the erun skills directory. ERUN_SKILLS_DIR is taken
// verbatim; otherwise the build stamp wins over walking up from the executable.
func hostSkillsSource() (string, error) {
	if override := strings.TrimSpace(os.Getenv("ERUN_SKILLS_DIR")); override != "" {
		return override, nil
	}
	stamped := strings.TrimSpace(buildSkillsSource)
	if isExistingDir(stamped) {
		return stamped, nil
	}
	found, exeDir := skillsSourceNearExecutable()
	if found != "" {
		return found, nil
	}
	return "", &noSkillsSourceError{stamped: stamped, exeDir: exeDir}
}

// runningExecutable is a seam: tests cannot choose where the test binary lives.
var runningExecutable = os.Executable

// skillsSourceNearExecutable also returns where it started, for error reporting.
func skillsSourceNearExecutable() (string, string) {
	exe, err := runningExecutable()
	if err != nil {
		return "", ""
	}
	exeDir := filepath.Dir(exe)
	dir := exeDir
	for i := 0; i < 8; i++ {
		if cand := filepath.Join(dir, "erun-skills", "skills"); isExistingDir(cand) {
			return cand, exeDir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", exeDir
}

func isExistingDir(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// reportSkillsNotInstalled warns once per run but logs every time.
func (a *App) reportSkillsNotInstalled(cause error) {
	log.Printf("erun-app: orchestrator skills not installed: %v", cause)
	var reported bool
	a.terminal.WithLock(func(*terminal.Locked) {
		reported = a.skillsSourceReported
		a.skillsSourceReported = true
	})
	if !reported {
		a.emitAppNotification("warning", orchestratorSkillsNotInstalledNotice(cause))
	}
}

// ERUN_SKILLS_DIR leads: a package-manager install has no checkout to rebuild from.
func orchestratorSkillsNotInstalledNotice(cause error) string {
	return "Orchestrator skills were not installed or refreshed: " + cause.Error() +
		". The orchestrator still starts, but its skills stay at whatever is already in ~/.claude/skills. " +
		"Set ERUN_SKILLS_DIR to an erun-skills/skills directory to install from, " +
		"or rebuild the desktop from its checkout with erun-ui/build.sh (build.ps1 on Windows)."
}

// ensureOrchestratorSkills installs or refreshes erun skills in ~/.claude/skills,
// preserving operator edits.
func ensureOrchestratorSkills() error {
	root, err := hostSkillsSource()
	if err != nil {
		return err
	}
	home, err := orchestratorInstallHome("skills")
	if err != nil {
		return err
	}
	_, err = podruntime.InstallOrRefreshSkills(root, filepath.Join(home, ".claude", "skills"))
	return err
}

func orchestratorInstallHome(what string) (string, error) {
	home, homeErr := os.UserHomeDir()
	if strings.TrimSpace(home) == "" {
		if homeErr == nil {
			homeErr = errors.New("it resolved empty")
		}
		return "", fmt.Errorf("resolve the home directory to install %s into: %w", what, homeErr)
	}
	return home, nil
}

// buildAgentsSource is buildSkillsSource's counterpart for erun-skills/agents.
var buildAgentsSource = ""

type noAgentsSourceError struct {
	stamped string
	exeDir  string
}

func (e *noAgentsSourceError) Error() string {
	built := "this build records no source checkout"
	if e.stamped != "" {
		built = "its build checkout " + e.stamped + " is not on this machine"
	}
	near := "the executable's own directory"
	if e.exeDir != "" {
		near = e.exeDir
	}
	return "no erun reusable agents source resolved: " + built + ", and no erun-skills/agents sits above " + near
}

// hostAgentsSource resolves like hostSkillsSource, for erun-skills/agents.
func hostAgentsSource() (string, error) {
	if override := strings.TrimSpace(os.Getenv("ERUN_AGENTS_DIR")); override != "" {
		return override, nil
	}
	stamped := strings.TrimSpace(buildAgentsSource)
	if isExistingDir(stamped) {
		return stamped, nil
	}
	found, exeDir := agentsSourceNearExecutable()
	if found != "" {
		return found, nil
	}
	return "", &noAgentsSourceError{stamped: stamped, exeDir: exeDir}
}

func agentsSourceNearExecutable() (string, string) {
	exe, err := runningExecutable()
	if err != nil {
		return "", ""
	}
	exeDir := filepath.Dir(exe)
	dir := exeDir
	for i := 0; i < 8; i++ {
		if cand := filepath.Join(dir, "erun-skills", "agents"); isExistingDir(cand) {
			return cand, exeDir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", exeDir
}

func (a *App) reportAgentsNotInstalled(cause error) {
	log.Printf("erun-app: orchestrator agents not installed: %v", cause)
	var reported bool
	a.terminal.WithLock(func(*terminal.Locked) {
		reported = a.agentsSourceReported
		a.agentsSourceReported = true
	})
	if !reported {
		a.emitAppNotification("warning", orchestratorAgentsNotInstalledNotice(cause))
	}
}

func orchestratorAgentsNotInstalledNotice(cause error) string {
	return "Orchestrator agents were not installed or refreshed: " + cause.Error() +
		". The orchestrator still starts, but its agents stay at whatever is already in ~/.claude/agents. " +
		"Set ERUN_AGENTS_DIR to an erun-skills/agents directory to install from, " +
		"or rebuild the desktop from its checkout with erun-ui/build.sh (build.ps1 on Windows)."
}

// ensureOrchestratorAgents installs or refreshes erun agents in ~/.claude/agents.
func ensureOrchestratorAgents() error {
	root, err := hostAgentsSource()
	if err != nil {
		return err
	}
	home, err := orchestratorInstallHome("agents")
	if err != nil {
		return err
	}
	_, err = podruntime.InstallOrRefreshAgents(root, filepath.Join(home, ".claude", "agents"))
	return err
}
