package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/command/podruntime"
)

// buildSkillsSource is the erun-skills/skills directory of the checkout this
// binary was built from, stamped in by the desktop build scripts. It is what
// lets a desktop that runs from outside its checkout still install the skills
// its own build shipped: the bundle is routinely copied away from the source
// tree (a dev build runs from ~/.cache/erun), and nothing above it there names a
// checkout. Empty for a build that did not stamp it, and naming a path that
// exists only on the machine that produced the binary — both fall through to the
// layout the executable runs in.
var buildSkillsSource = ""

// noSkillsSourceError reports that no shipped skills resolved at all, naming
// each layout that was tried. The install stays best-effort so a binary with no
// source still launches, but the condition has to be legible: a build that
// quietly stops honouring its own install contract is indistinguishable from one
// where the skill simply had not changed, so both the author of a skill change
// and its reader believe it landed.
type noSkillsSourceError struct {
	// stamped is the build's own checkout, empty when the binary carries no stamp.
	stamped string
	// exeDir is where the running binary lives, empty when it cannot be resolved.
	exeDir string
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

// hostSkillsSource resolves the directory holding the canonical erun skills
// (erun-skills/skills/<name>/) on the host, or reports every layout it tried
// when nothing resolves.
//
// ERUN_SKILLS_DIR is an exact override: it is taken verbatim with no fallback,
// so pointing the desktop at a deliberately empty directory means exactly that.
// Otherwise the checkout the binary was built from wins over the directory it
// runs from, because that checkout holds the skills this build ships and — unlike
// a walk up from the executable — it still names them wherever the bundle was
// copied to. The walk remains for a binary built without the stamp that does sit
// inside a checkout or a packaged layout carrying one.
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

// runningExecutable resolves the binary this process runs as. A seam because
// the answer below depends on where that binary sits, and a test about a
// particular layout cannot choose where the test binary was written.
var runningExecutable = os.Executable

// skillsSourceNearExecutable walks up from the running binary looking for an
// erun-skills/skills directory, which is how one that sits inside a checkout
// finds its skills. It also returns where it started, so an unresolved source
// can say where it looked.
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

// reportSkillsNotInstalled makes a skills install that did not run observable.
// The warning goes where the operator acts, once per run — the condition belongs
// to the build rather than to any one orchestrator launch — while the log line is
// written every time so a later diagnosis still finds it.
func (a *App) reportSkillsNotInstalled(cause error) {
	log.Printf("erun-app: orchestrator skills not installed: %v", cause)
	a.mu.Lock()
	reported := a.skillsSourceReported
	a.skillsSourceReported = true
	a.mu.Unlock()
	if !reported {
		a.emitAppNotification("warning", orchestratorSkillsNotInstalledNotice(cause))
	}
}

// orchestratorSkillsNotInstalledNotice states what the operator loses — an
// installed skill that no longer tracks its source — and the two ways to put it
// back, since the launch itself succeeds and would otherwise look untroubled.
// ERUN_SKILLS_DIR leads because it is the recovery that works everywhere: a
// desktop installed from a package manager carries no checkout to rebuild from.
func orchestratorSkillsNotInstalledNotice(cause error) string {
	return "Orchestrator skills were not installed or refreshed: " + cause.Error() +
		". The orchestrator still starts, but its skills stay at whatever is already in ~/.claude/skills. " +
		"Set ERUN_SKILLS_DIR to an erun-skills/skills directory to install from, " +
		"or rebuild the desktop from its checkout with erun-ui/build.sh (build.ps1 on Windows)."
}

// ensureOrchestratorSkills installs every erun skill into ~/.claude/skills so the
// orchestrator's Claude session sees them by default, with no operator install
// step. It installs a skill when absent and refreshes an untouched one when the
// shipped source changed, preserving in-place operator edits -- the same
// install-or-refresh the runtime pod applies to its baked skills
// (podruntime.InstallOrRefreshSkills). An unresolvable source is returned as an
// error rather than skipped silently: the caller keeps launching, and says so.
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

// orchestratorInstallHome resolves the home directory skills or agents install
// under.
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

// buildAgentsSource is the erun-skills/agents directory of the checkout this
// binary was built from, stamped in by the desktop build scripts — the exact
// counterpart of buildSkillsSource above, for the reusable agent definitions
// (erun-builder/erun-reviewer) instead of skills.
var buildAgentsSource = ""

// noAgentsSourceError mirrors noSkillsSourceError for the agents source.
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

// hostAgentsSource resolves the directory holding the canonical erun reusable
// agents (erun-skills/agents/<name>.md) on the host, mirroring hostSkillsSource's
// override → build-stamp → walk-up resolution order.
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

// agentsSourceNearExecutable walks up from the running binary looking for an
// erun-skills/agents directory, mirroring skillsSourceNearExecutable.
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

// reportAgentsNotInstalled mirrors reportSkillsNotInstalled for the agents
// source.
func (a *App) reportAgentsNotInstalled(cause error) {
	log.Printf("erun-app: orchestrator agents not installed: %v", cause)
	a.mu.Lock()
	reported := a.agentsSourceReported
	a.agentsSourceReported = true
	a.mu.Unlock()
	if !reported {
		a.emitAppNotification("warning", orchestratorAgentsNotInstalledNotice(cause))
	}
}

// orchestratorAgentsNotInstalledNotice mirrors orchestratorSkillsNotInstalledNotice
// for the reusable agents (erun-builder/erun-reviewer).
func orchestratorAgentsNotInstalledNotice(cause error) string {
	return "Orchestrator agents were not installed or refreshed: " + cause.Error() +
		". The orchestrator still starts, but its agents stay at whatever is already in ~/.claude/agents. " +
		"Set ERUN_AGENTS_DIR to an erun-skills/agents directory to install from, " +
		"or rebuild the desktop from its checkout with erun-ui/build.sh (build.ps1 on Windows)."
}

// ensureOrchestratorAgents installs every erun reusable agent into
// ~/.claude/agents so the orchestrator's Claude session can delegate to
// erun-builder/erun-reviewer by default, with no operator install step, under
// the same install-or-refresh contract as ensureOrchestratorSkills.
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
