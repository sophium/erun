package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	eruncommon "github.com/sophium/erun/erun-common"
)

// An orchestrator is a host-side AI session that is NOT scoped to a single
// environment: it runs the AI harness on the operator's machine with the erun
// CLI on PATH, so it can drive the agent environments it links. The real work
// happens in the pods — the orchestrator delegates edits and builds to the
// in-pod agents, reviews each env's worktree on the host read-only, and runs
// host-native build artifacts to verify. It may build locally to help, but never
// writes into a review directory: the in-pod agent owns the worktree.
//
// An orchestrator is a persisted definition (root config): a set of linked agent
// environments, each with a host review directory. A remote-agent env's is the
// one-way mirror its workspace sync fills; a local-agent env's is the worktree
// itself, already on this machine because the pod hostPath-mounts it. The set
// reappears across restarts; the running session is ephemeral.

// orchestratorSession is a live orchestrator PTY. Persisted orchestrators are
// keyed by their config ID; transient ones (Investigate) carry their own display
// metadata since they have no config definition.
type orchestratorSession struct {
	id     string
	serial int
	// conversationID is the AI-harness conversation this PTY is attached to. It
	// is what a restart records, so the resume continues this conversation rather
	// than whichever one the (mutable, reusable) orchestrator id resolves to
	// later.
	conversationID string
	transient      bool
	name           string
	envs           []eruncommon.OrchestratorEnvConfig
	startedAt      time.Time
	// aiBusy is the last turn-boundary report the poller observed. It is what
	// orchestratorInfoFor's Busy field reads for every snapshot this session
	// appears in (ListOrchestrators, runningOrchestratorInfo, ...), so a fresh
	// mount or reconnect renders the true state directly rather than depending
	// on having witnessed the ai-activity event that last changed it. See
	// reconcileOrchestratorActivity in session_heartbeat.go.
	aiBusy bool
}

// orchestratorEnvInput is the frontend's env selection for create/update.
type orchestratorEnvInput struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Directory   string `json:"directory"`
}

// orchestratorEnvCandidate is an agent env the operator can link, with the host
// directory the orchestrator reviews it in. Mirrored distinguishes the two kinds
// of review directory: a workspace-sync mirror the operator may place anywhere,
// or the env's own worktree on this machine, whose path is derived and fixed.
type orchestratorEnvCandidate struct {
	Tenant           string `json:"tenant"`
	Environment      string `json:"environment"`
	DefaultDirectory string `json:"defaultDirectory"`
	Mirrored         bool   `json:"mirrored"`
}

type orchestratorEnvInfo struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Directory   string `json:"directory"`
}

// orchestratorInfo is the JSON-safe view the frontend renders and attaches to.
// Busy carries the same signal as the ai-activity event this orchestrator's
// SessionID is keyed by, so a snapshot fetched after a busy transition — a
// fresh mount, a window reopen, a reconnecting listener — renders the true
// state without having had to witness that event. The frontend seeds its
// event-keyed store from this field on every fetch (loadOrchestrators) rather
// than keeping a second source of truth; see aiActivitySlice.ts.
type orchestratorInfo struct {
	ID           string                `json:"id"`
	Name         string                `json:"name"`
	Environments []orchestratorEnvInfo `json:"environments"`
	Tenants      []string              `json:"tenants"`
	Directories  []string              `json:"directories"`
	SessionID    int                   `json:"sessionId"`
	Status       string                `json:"status"`
	Busy         bool                  `json:"busy"`
	Transient    bool                  `json:"transient"`
}

func orchestratorSessionKey(id string) string {
	return "orchestrator\x00" + id
}

// orchestratorsRoot is the single host workspace every orchestrator shares
// ($HOME/orchestrators): it holds the one shared CLAUDE.md and, as
// `<tenant>-<env>` subdirectories, the synced mirrors. Orchestrators launch here
// directly — there is no per-orchestrator subfolder.
func orchestratorsRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, "orchestrators")
}

// orchestratorReturnNoteName is the note an orchestrator leaves its unfinished
// task in before triggering a rebuild+restart, in the shared working directory.
// The id is in the name because that directory is shared: it is what lets a
// woken session read its own agenda rather than whichever note happened to be
// written last, and the session can address it without being told which one it
// is, from the id it already carries. An unnamed session (transient) stages no
// hand-off, so it only ever falls back to the bare name.
func orchestratorReturnNoteName(orchestratorID string) string {
	id := strings.TrimSpace(orchestratorID)
	if id == "" {
		return "RESUME-NOTE.md"
	}
	return "RESUME-NOTE." + id + ".md"
}

// defaultOrchestratorDirectory is the host mirror an env defaults to:
// $HOME/orchestrators/<tenant>-<env>. Mirrors sit beside the shared CLAUDE.md,
// keyed by env so every orchestrator linking the same env reviews one synced copy.
func defaultOrchestratorDirectory(tenant, environment string) string {
	return filepath.Join(orchestratorsRoot(), strings.TrimSpace(tenant)+"-"+strings.TrimSpace(environment))
}

// orchestratableEnv reports whether an env can be linked to an orchestrator. It
// needs a worktree to review and an in-pod agent to delegate to, which both agent
// types have and a runtime env does not.
func orchestratableEnv(env eruncommon.EnvConfig) bool {
	switch env.ResolvedType() {
	case eruncommon.EnvironmentTypeLocalAgent, eruncommon.EnvironmentTypeRemoteAgent:
		return true
	default:
		return false
	}
}

// orchestratorReviewDirectory resolves where an orchestrator reviews an env on
// this machine, and whether that directory is a synced mirror. It applies the
// same policy as hostWorkspacePath — a local-agent worktree is already here, so
// it is reviewed in place — but yields the mirror path a remote-agent env would
// be wired to rather than "" when its sync is not on yet.
func orchestratorReviewDirectory(tenant string, env eruncommon.EnvConfig) (string, bool) {
	if env.ResolvedType() == eruncommon.EnvironmentTypeLocalAgent {
		return strings.TrimSpace(env.LocalRepoPath), false
	}
	return defaultOrchestratorDirectory(tenant, env.Name), true
}

// orchestratorClaudeMd is the single CLAUDE.md every orchestrator shares in the
// orchestrators root. It mirrors the erun-orchestrate skill (the source of truth
// for the full workflow) and is generic on purpose: mirrors are discovered as the
// `<tenant>-<env>` subdirectories present, so there is no per-orchestrator
// environment list and no per-orchestrator folder.
const orchestratorClaudeMd = `# Orchestrator working directory

You are a **host-side erun orchestrator**. You coordinate work across the erun
agent environments linked to you, from the operator's machine. The real
work happens in the pods — you delegate, review, and verify. Follow the ` + "`erun-orchestrate`" + ` skill.

## Operating under this contract (read first)

You are **already operating under this contract** from the first turn of a
session — treat it as always in force. Do not defer or skip it for a "quick" or
"trivial-looking" question: answer every question and run every task as the
orchestrator, under these rules, before doing anything else. The ` + "`erun-orchestrate`" + ` skill
holds the detailed workflow — follow it — but never act without the rules here in
force.

**Know your scope from config, never from memory or disk.** Your identity is the
` + "`ERUN_ORCHESTRATOR_ID`" + ` environment variable; your linked environments are the
` + "`orchestrators:`" + ` entry with that id in erun's ` + "`config.yaml`" + `. Never state which
environments are yours from recollection, or infer them from which directories
here happen to have files — read the config every time.

## Rules

- Your **review directory** for an environment is the ` + "`directory`" + ` on its
  ` + "`orchestrators:`" + ` entry, and it is one of two kinds. A ` + "`<tenant>-<env>`" + `
  subdirectory here is a one-way **mirror** of a remote-agent environment's worktree,
  kept in sync from its pod. A path outside this root is a **local-agent
  environment's own worktree**, which lives on this machine and is hostPath-mounted
  into its pod. The environment's ` + "`type`" + ` tells you which kind you have.
- **Never write into a review directory**, whichever kind it is. In a mirror the edit
  is simply lost — the next sync overwrites it. In a local-agent worktree it is worse:
  the edit *does* reach the pod, so it silently competes with the in-pod agent that
  owns that tree, in what is also the operator's own checkout.
- To change code, **ask the in-pod agent** in the relevant environment to do it
  (drive it via ` + "`erun`" + ` / the env's MCP). Never patch the directory yourself.
- **Review** changes on the host, read-only. A mirror is a one-way plain-directory
  copy of the pod's working tree with no git of its own, so read the synced files and
  take the authoritative diff of uncommitted work from the pod (the desktop app's
  Review, or ask the in-pod agent to run ` + "`git diff`" + `). A local-agent worktree
  *is* a real checkout, so ` + "`git -C <dir> diff`" + ` here is already authoritative.
- **Verify** by running host-native build artifacts (e.g. a Windows ` + "`.exe`" + ` the pod
  cross-built) — the pod can't run a foreign-OS binary. A mirror carries them under
  its read-only ` + "`.erun-outputs/`" + `; a local-agent environment has no mirror, so
  pull them with that env's ` + "`outputs_list`" + `/` + "`outputs_download`" + ` (or the
  desktop's Outputs) first. You may build locally to help, but never edit a review
  directory.
- **This directory is shared with every other orchestrator**, so anything here that is
  yours alone carries your id in its name. The return note you leave before a
  rebuild+restart is the one that matters most: erun reads it back as
  ` + "`RESUME-NOTE.$ERUN_ORCHESTRATOR_ID.md`" + `, and a note addressed to the directory
  alone is one any orchestrator can read as its own or overwrite.
- File erun **platform** bugs with the ` + "`erun-file-issue`" + ` skill.

## Operating mode

- **Never stop until the assigned task is completed.** A task given to this
  orchestrator is authorization to carry it through to a verified, working end
  state, uninterrupted — investigate, decide, implement, and verify end-to-end
  without pausing between steps. Land the whole task in the **same PR**; do not
  split it across PRs, defer part of it, or hand back a half-finished task.
- **Do not ask questions — go with the recommended assumption.** Never stop to make
  the operator choose. For any ambiguity or fork in the road, pick the option you
  would recommend and proceed, resolving it from the code, tests, and sensible
  defaults rather than a question.
- **A standing instruction is pre-authorization, not a request to re-ask.** Filing
  a platform bug with the ` + "`erun-file-issue`" + ` skill is authorized every time
  it applies. When unsure whether an action needs permission, compare it against the
  most consequential thing you have already done unaided this turn — if it is
  smaller, it does not.
- **Never end a turn on an offer.** "Say the word", "let me know if", "next action
  is yours" hand the operator a decision and stall the work exactly as a question
  would. Do the thing, or state it as a decision already taken, and finish.
- **Test everything end-to-end.** Verification is part of the task, not a follow-up.
  Drive the change into the real target (the in-pod agent builds/deploys it), then
  reproduce the original flow against the running artifact and watch it succeed —
  never stop at "unit tests pass" or "it builds". State plainly anything you could
  not verify and why.
- **On completion, present the assumptions you took.** End with a concise list of
  every recommended assumption you made in place of asking, so the operator can
  course-correct. This list is required, not optional.
- The one exception to acting uninterrupted: an **irreversible or cross-env action**
  (deploy, delete, rebuild+restart, anything that mutates shared/remote state) still
  gets a clear heads-up before you run it — a notification issued as you proceed,
  never a gate you stop on.
`

// ensureOrchestratorWorkspace makes sure the shared orchestrators root exists and
// carries the one CLAUDE.md, returning the directory every orchestrator launches
// in. There is no per-orchestrator subfolder.
func (a *App) ensureOrchestratorWorkspace() (string, error) {
	dir := orchestratorsRoot()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create orchestrators workspace %s: %w", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(orchestratorClaudeMd), 0o644); err != nil {
		return "", fmt.Errorf("write orchestrator CLAUDE.md: %w", err)
	}
	// Make every erun skill available to the orchestrator by default — the
	// operator must never have to install them by hand. Best-effort: a missing
	// skills source (e.g. a distributed binary with no repo checkout) must not
	// block the orchestrator from launching, but it is reported rather than
	// passed over in silence.
	if err := ensureOrchestratorSkills(); err != nil {
		a.reportSkillsNotInstalled(err)
	}
	// Inject the operating contract on every session start and reopen via a
	// SessionStart hook, so an orchestrator always operates under its current
	// contract instead of relying on the model to invoke a skill it can skip.
	// Best-effort for the same reason as the skills install; a SessionStart hook
	// is non-blocking, so a stale or missing one never prevents a launch.
	if err := ensureOrchestratorSessionStartHook(dir); err != nil {
		fmt.Fprintf(os.Stderr, "erun: could not write orchestrator SessionStart hook: %v\n", err)
	}
	return dir, nil
}

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

// orchestratorSkillMarker records, per installed skill, the sha256 of the
// SKILL.md erun last installed, so a later launch can tell an untouched copy
// (safe to refresh from the shipped source) from one the operator edited in
// place (must be preserved). Mirrors the runtime pod's skills-install.sh marker.
const orchestratorSkillMarker = ".erun-skill-baked-sha256"

// ensureOrchestratorSkills installs every erun skill into ~/.claude/skills so the
// orchestrator's Claude session sees them by default, with no operator install
// step. It installs a skill when absent and REFRESHES an untouched one when the
// shipped source changed — so an orchestrator tracks the latest skill instead of
// freezing at first install — while preserving any in-place operator edits via a
// per-skill marker. Mirrors the pod entrypoint's install-or-refresh so host and
// pod treat skill provenance identically. An unresolvable source is returned as
// an error rather than skipped silently: the caller keeps launching, and says so.
func ensureOrchestratorSkills() error {
	root, err := hostSkillsSource()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read skills source %s: %w", root, err)
	}
	home, homeErr := os.UserHomeDir()
	if strings.TrimSpace(home) == "" {
		if homeErr == nil {
			homeErr = errors.New("it resolved empty")
		}
		return fmt.Errorf("resolve the home directory to install skills into: %w", homeErr)
	}
	destRoot := filepath.Join(home, ".claude", "skills")
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := installOrRefreshOrchestratorSkill(filepath.Join(root, entry.Name()), filepath.Join(destRoot, entry.Name())); err != nil {
			return fmt.Errorf("install skill %s: %w", entry.Name(), err)
		}
	}
	return nil
}

// installOrRefreshOrchestratorSkill reconciles one installed skill against its
// shipped source: install when absent; refresh when the installed copy is still
// the one erun wrote (marker matches, or a legacy copy with no marker); leave a
// copy the operator edited in place untouched.
func installOrRefreshOrchestratorSkill(src, dst string) error {
	bakedMD := filepath.Join(src, "SKILL.md")
	if _, err := os.Stat(bakedMD); err != nil {
		return nil // a source dir with no SKILL.md is not a skill
	}
	instMD := filepath.Join(dst, "SKILL.md")
	if _, err := os.Stat(instMD); err != nil {
		return copyOrchestratorSkill(src, dst) // absent — install
	}
	bakedSHA, err := fileSHA256(bakedMD)
	if err != nil {
		return err
	}
	instSHA, err := fileSHA256(instMD)
	if err != nil {
		return err
	}
	marker := filepath.Join(dst, orchestratorSkillMarker)
	if instSHA == bakedSHA {
		// Already the shipped version; record the marker so a future upgrade can
		// still tell this untouched copy from an edited one (also adopts a
		// pre-marker copy that happens to match).
		return os.WriteFile(marker, []byte(bakedSHA+"\n"), 0o644)
	}
	markerSHA := ""
	if b, readErr := os.ReadFile(marker); readErr == nil {
		markerSHA = strings.TrimSpace(string(b))
	}
	if markerSHA == "" || instSHA == markerSHA {
		// Unmodified since erun installed it, or a legacy copy with no marker —
		// refresh to the shipped version.
		return copyOrchestratorSkill(src, dst)
	}
	return nil // edited in place — preserve the operator's copy
}

// copyOrchestratorSkill replaces dst with a fresh copy of src and records the
// shipped SKILL.md hash, so a later launch can distinguish an untouched copy
// from an edited one.
func copyOrchestratorSkill(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := copyDirTree(src, dst); err != nil {
		return err
	}
	sha, err := fileSHA256(filepath.Join(src, "SKILL.md"))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, orchestratorSkillMarker), []byte(sha+"\n"), 0o644)
}

// fileSHA256 returns the hex sha256 of a file's contents.
func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// orchestratorContractFallback is echoed when the shared CLAUDE.md is somehow
// missing, so a session still boots knowing it is under the contract. It is
// ASCII-only and apostrophe-free so the single-quoted echo is safe in both Git
// Bash (Windows) and sh (macOS/Linux).
const orchestratorContractFallback = "You are a host-side erun orchestrator. Read and follow the CLAUDE.md in this directory and the erun-orchestrate skill before doing anything, even a trivial-looking question."

// orchestratorSkillHookCommand is the SessionStart hook command written into the
// orchestrators root's .claude/settings.json. It INJECTS the operating contract
// into the session by printing the shared CLAUDE.md to plain stdout (added to the
// session context), so an orchestrator always has its contract in context instead
// of being asked to load a skill it can skip. Plain stdout — not JSON — sidesteps
// the 10,000-char additionalContext cap; cat reads the file directly, so the
// contract body is never shell-quoted and only the forward-slashed path is
// double-quoted (safe in Git Bash and sh). The apostrophe-free echo is the
// fallback if the file is ever missing.
func orchestratorSkillHookCommand(dir string) string {
	claudeMd := filepath.ToSlash(filepath.Join(dir, "CLAUDE.md"))
	return `cat "` + claudeMd + `" 2>/dev/null || echo '` + orchestratorContractFallback + `'`
}

// ensureOrchestratorSessionStartHook writes a SessionStart hook into the shared
// orchestrators root's .claude/settings.json, merging so it never clobbers other
// keys or hook events already there. SessionStart fires on a new session
// (startup) and a reopened one (resume), so every orchestrator has its contract
// injected both on start and on reopen.
func ensureOrchestratorSessionStartHook(dir string) error {
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(claudeDir, "settings.json")
	settings := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &settings) // best-effort; the hook is rebuilt below regardless
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	hooks["SessionStart"] = orchestratorSessionStartHook(dir)
	// The agent reports its own turn boundaries. Whether it is working cannot be
	// read off its terminal: an agent TUI repaints continuously, so an
	// output-driven latch never clears.
	//
	// The busy report goes on the tool-call events as well as the turn's start, so
	// a turn longer than the staleness bound renews it instead of letting it
	// expire underneath work that is still running. Each event is merged rather
	// than assigned: this settings file is shared with the operator, and the
	// tool-call events in particular are somewhere they are likely to have hooks
	// of their own, which an assignment would silently delete.
	busyHook, idleHook := orchestratorActivityHooks()
	for _, event := range []string{"UserPromptSubmit", "PreToolUse", "PostToolUse"} {
		hooks[event] = mergeOrchestratorHookBlocks(hooks[event], busyHook, isOrchestratorActivityHookBlock)
	}
	stop := mergeOrchestratorHookBlocks(hooks["Stop"], idleHook, isOrchestratorActivityHookBlock)
	hooks["Stop"] = mergeOrchestratorHookBlocks(stop, orchestratorNoAskStopGuardBlock(), isOrchestratorNoAskStopGuardBlock)
	settings["hooks"] = hooks
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// orchestratorNoAskGuardMarker identifies the stop guard this file wrote, so a
// rewrite replaces its own previous block instead of stacking another copy.
const orchestratorNoAskGuardMarker = "noask_guard=1"

// orchestratorNoAskStopGuardReason is fed back to the session when the guard
// fires. ASCII-only and apostrophe-free so the single-quoted printf is safe in
// both Git Bash (Windows) and sh (macOS/Linux).
const orchestratorNoAskStopGuardReason = "Your closing message hands the operator a decision. " +
	"The orchestrator contract resolves ambiguity itself and carries the task to a verified end, " +
	"so a question is a defect, not caution. Do what you offered, or state it as a decision already taken, " +
	"and finish. If the action is outward-facing, announce it and proceed - a heads-up is not a gate."

// orchestratorNoAskStopGuardCommand refuses a turn that ends by handing the
// operator a decision, which the launch flag cannot reach: denying the tool
// removes the question form, not the closing sentence that stalls the work just
// as long. Reading the turn's own last words is what puts the guarantee at the
// layer the behaviour surfaces on.
//
// Every failure path exits 0. A guard that wedged a session on a transcript it
// could not parse would cost more than the stalls it prevents, and an already
// nudged turn (stop_hook_active) is let go so a session is corrected once rather
// than looped. Like the activity reports it is bare shell, so it keeps working
// when erun is not on PATH.
func orchestratorNoAskStopGuardCommand() string {
	return orchestratorNoAskGuardMarker + `; input=$(cat); ` +
		`printf '%s' "$input" | grep -q '"stop_hook_active"[[:space:]]*:[[:space:]]*true' && exit 0; ` +
		`transcript=$(printf '%s' "$input" | sed -n 's/.*"transcript_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | sed 's/\\\\/\//g'); ` +
		`[ -f "$transcript" ] || exit 0; ` +
		`tail -n 5 "$transcript" | grep -qiE 'say the word|let me know if|let me know whether|shall i |do you want me to|would you like me to|next action is yours|if you.d like me to|your call' || exit 0; ` +
		`printf '%s' '` + orchestratorNoAskStopGuardReason + `' >&2; exit 2`
}

// orchestratorNoAskStopGuardBlock is the guard bound to the Stop event.
func orchestratorNoAskStopGuardBlock() []any {
	return []any{map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": orchestratorNoAskStopGuardCommand()}},
	}}
}

// isOrchestratorNoAskStopGuardBlock reports whether a settings hook block is the
// guard. Anything it cannot read is somebody else's and is kept.
func isOrchestratorNoAskStopGuardBlock(block any) bool {
	group, ok := block.(map[string]any)
	if !ok {
		return false
	}
	hooks, ok := group["hooks"].([]any)
	if !ok {
		return false
	}
	for _, hook := range hooks {
		entry, ok := hook.(map[string]any)
		if !ok {
			continue
		}
		if command, ok := entry["command"].(string); ok && strings.Contains(command, orchestratorNoAskGuardMarker) {
			return true
		}
	}
	return false
}

// orchestratorSessionStartHook is the SessionStart hook block: the contract-
// injecting command runs on both new starts (startup) and reopens (resume).
func orchestratorSessionStartHook(dir string) []any {
	command := map[string]any{"type": "command", "command": orchestratorSkillHookCommand(dir)}
	// A session killed mid-turn never writes its end, so a new or reopened one
	// would inherit the previous run's "working" and spin on arrival with nothing
	// running. Clearing it here is what makes that guarantee real.
	idle := map[string]any{"type": "command", "command": orchestratorActivityHookCommand(false)}
	matcher := func(source string) map[string]any {
		return map[string]any{"matcher": source, "hooks": []any{command, idle}}
	}
	return []any{matcher("startup"), matcher("resume")}
}

// copyDirTree recursively copies src into dst (files + subdirectories), portable
// across host OSes (no shell cp).
func copyDirTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func envInfos(envs []eruncommon.OrchestratorEnvConfig) []orchestratorEnvInfo {
	out := make([]orchestratorEnvInfo, 0, len(envs))
	for _, env := range envs {
		out = append(out, orchestratorEnvInfo{Tenant: env.Tenant, Environment: env.Environment, Directory: env.Directory})
	}
	return out
}

func tenantsFromEnvs(envs []eruncommon.OrchestratorEnvConfig) []string {
	seen := make(map[string]struct{}, len(envs))
	out := make([]string, 0, len(envs))
	for _, env := range envs {
		if _, dup := seen[env.Tenant]; dup || strings.TrimSpace(env.Tenant) == "" {
			continue
		}
		seen[env.Tenant] = struct{}{}
		out = append(out, env.Tenant)
	}
	return out
}

func directoriesFromEnvs(envs []eruncommon.OrchestratorEnvConfig) []string {
	out := make([]string, 0, len(envs))
	for _, env := range envs {
		if strings.TrimSpace(env.Directory) != "" {
			out = append(out, env.Directory)
		}
	}
	return out
}

func orchestratorInfoFor(id, name string, envs []eruncommon.OrchestratorEnvConfig, status string, sessionID int, busy, transient bool) orchestratorInfo {
	return orchestratorInfo{
		ID:           id,
		Name:         name,
		Environments: envInfos(envs),
		Tenants:      tenantsFromEnvs(envs),
		Directories:  directoriesFromEnvs(envs),
		SessionID:    sessionID,
		Status:       status,
		Busy:         busy,
		Transient:    transient,
	}
}

// orchestratorModel is the model an orchestrator's Claude session launches on,
// and the model its subagents inherit (via CLAUDE_CODE_SUBAGENT_MODEL). Matches
// the erun-env default so an orchestrator behaves like the in-pod agent.
const orchestratorModel = "opus"

// orchestratorUltracodeFlag turns on ultracode effort — xhigh thinking plus
// standing multi-agent workflow orchestration — the erun-env default. The
// single-quoted JSON is literal in both PowerShell and POSIX shells. Kept in
// lockstep with erun-common's claudeEffortFlags(ultracode).
const orchestratorUltracodeFlag = ` --settings '{"ultracode":true}'`

// orchestratorNoAskFlag removes the harness's ability to stop and ask. An
// orchestrator's contract is to resolve ambiguity from the code, tests and
// sensible defaults and carry the task to a verified end — so a question is a
// defect, not caution, and one asked while the operator is away stalls the work
// indefinitely. Denying the tool makes that structural rather than a matter of
// the agent's judgement about its own instructions.
//
// The flag covers the tool, not the sentence: a turn that ends "say the word and
// I will do it" never calls the tool and stalls just as long, which is what
// orchestratorNoAskStopGuardCommand catches. The two are halves of one guard.
const orchestratorNoAskFlag = " --disallowedTools AskUserQuestion"

// orchestratorLaunchCommand resolves how to launch the host AI harness. It runs
// through the host shell so an npm claude.cmd / .ps1 shim resolves (ConPTY can't
// exec a .cmd directly). A non-empty initialPrompt seeds a fresh session (the
// Investigate flow); otherwise the launch resumes THIS orchestrator's own
// conversation (pinned by sessionID) — creating it on first open — so
// orchestrators no longer collide on `--continue`'s single most-recent
// conversation in the shared workspace. A non-empty resumePrompt is handed to
// the resumed (or freshly created) session so a rebuild+restart continues its
// task itself.
func orchestratorLaunchCommand(sessionID, initialPrompt, resumePrompt, mcpConfigPath string) (string, []string, error) {
	if _, err := exec.LookPath(defaultAITool); err != nil {
		return "", nil, fmt.Errorf("the %q CLI was not found on PATH; install it to run an orchestrator", defaultAITool)
	}
	shell, args := buildOrchestratorLaunch(runtime.GOOS, sessionID, orchestratorSessionExists(sessionID), initialPrompt, resumePrompt, mcpConfigPath)
	return shell, args, nil
}

// orchestratorSessionNamespace seeds the deterministic per-orchestrator Claude
// session id. Any fixed UUID works; it only has to be stable.
var orchestratorSessionNamespace = uuid.MustParse("6f7e9c2a-1b3d-4e5f-8a9b-0c1d2e3f4a5b")

// orchestratorSessionID derives a stable Claude session id for an orchestrator
// from its own id, so each orchestrator resumes ITS OWN conversation rather than
// whatever `--continue` finds as the most-recent one in the shared
// $HOME/orchestrators workspace (which collapses every orchestrator onto one
// shared session). Deterministic, so it needs no persistence, and unique per
// orchestrator id. Empty for a transient/Investigate orchestrator (no id), which
// keeps starting a fresh unpinned session.
func orchestratorSessionID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return uuid.NewSHA1(orchestratorSessionNamespace, []byte(id)).String()
}

// orchestratorSessionExists reports whether a Claude conversation for this
// session id already exists on disk, so the launch can `--resume` it instead of
// trying to create it (Claude rejects `--session-id` for an id already in use).
// It globs across all project dirs — the session id is globally unique — so it
// need not replicate Claude's cwd->project-dir encoding.
func orchestratorSessionExists(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	matches, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", sessionID+".jsonl"))
	return err == nil && len(matches) > 0
}

// buildOrchestratorLaunch composes the host-shell invocation. Every launch runs
// ultracode effort on opus. With a pinned sessionID the launch resumes THIS
// orchestrator's own conversation (`--resume <id>` when it already exists, else
// `--session-id <id>` to create it under that id) so orchestrators stay
// isolated; without one (transient/Investigate, or a legacy caller) it keeps the
// old "continue, else fresh". The resume is expressed per shell so it survives a
// first run: PowerShell tests $LASTEXITCODE, POSIX chains with ||. A resumePrompt
// is appended to both the resume and the fresh-fallback branch so an auto-resume
// runs the task even on the first launch.
func buildOrchestratorLaunch(goos, sessionID string, sessionExists bool, initialPrompt, resumePrompt, mcpConfigPath string) (string, []string) {
	quote := shellQuote
	if goos == "windows" {
		quote = powerShellQuote
	}
	flags := orchestratorUltracodeFlag + orchestratorNoAskFlag + " --model " + orchestratorModel
	if strings.TrimSpace(mcpConfigPath) != "" {
		flags += " --mcp-config " + quote(mcpConfigPath)
	}
	fresh := defaultAITool + flags
	shell, shellArgs := resolveLocalShellCommand(goos)

	resume := defaultAITool + " --continue" + flags
	if strings.TrimSpace(sessionID) != "" {
		if sessionExists {
			resume = defaultAITool + " --resume " + sessionID + flags
		} else {
			resume = defaultAITool + " --session-id " + sessionID + flags
		}
	}

	chain := func(primary, fallback string) string {
		if goos == "windows" {
			return primary + "; if ($LASTEXITCODE -ne 0) { " + fallback + " }"
		}
		return primary + " || " + fallback
	}

	var command string
	switch {
	case strings.TrimSpace(initialPrompt) != "":
		command = fresh + " " + orchestratorPromptArg(goos, initialPrompt)
	case strings.TrimSpace(resumePrompt) != "":
		arg := orchestratorPromptArg(goos, resumePrompt)
		command = chain(resume+" "+arg, fresh+" "+arg)
	default:
		command = chain(resume, fresh)
	}

	flag := "-lc"
	if goos == "windows" {
		flag = "-Command"
	}
	return shell, append(shellArgs, flag, command)
}

// orchestratorPromptArg renders a prompt as the one argument the harness reads
// it from, verbatim. Two things have to hold, and neither is the default. The
// leading `--` ends option parsing, because the prompt is positional and a
// preceding multi-value flag otherwise swallows it as another value. The text is
// then quoted for the shell that re-parses this command line, because a prompt is
// ordinary task text: it carries code spans, `$` and backslashes, which a
// double-quoted splice would execute on the operator's host and strip from what
// the harness receives.
func orchestratorPromptArg(goos, prompt string) string {
	prompt = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(prompt)
	if goos == "windows" {
		return "-- " + powerShellQuote(prompt)
	}
	return "-- " + shellQuote(prompt)
}

func (a *App) loadOrchestratorConfigs() ([]eruncommon.OrchestratorConfig, error) {
	config, _, err := a.deps.store.LoadERunConfig()
	if errors.Is(err, eruncommon.ErrNotInitialized) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return config.Orchestrators, nil
}

func (a *App) saveOrchestratorConfigs(orchestrators []eruncommon.OrchestratorConfig) error {
	existing, _, err := a.deps.store.LoadERunConfig()
	if errors.Is(err, eruncommon.ErrNotInitialized) {
		existing = eruncommon.ERunConfig{}
	} else if err != nil {
		return err
	}
	existing.Orchestrators = orchestrators
	return a.deps.store.SaveERunConfig(existing)
}

func (a *App) findOrchestratorConfig(id string) (eruncommon.OrchestratorConfig, error) {
	configs, err := a.loadOrchestratorConfigs()
	if err != nil {
		return eruncommon.OrchestratorConfig{}, err
	}
	for _, config := range configs {
		if config.ID == id {
			return config, nil
		}
	}
	return eruncommon.OrchestratorConfig{}, fmt.Errorf("orchestrator %q not found", id)
}

// ListOrchestratorEnvCandidates returns the agent environments the operator can
// link, each with the host directory the orchestrator reviews it in: a mirror the
// sync fills for a remote-agent env, or the worktree itself for a local-agent env,
// which is already on this machine because the pod hostPath-mounts it.
func (a *App) ListOrchestratorEnvCandidates() ([]orchestratorEnvCandidate, error) {
	tenants, err := a.deps.store.ListTenantConfigs()
	if err != nil {
		return nil, err
	}
	out := []orchestratorEnvCandidate{}
	for _, tenant := range tenants {
		envs, envErr := a.deps.store.ListEnvConfigs(tenant.Name)
		if envErr != nil {
			continue
		}
		for _, env := range envs {
			if !orchestratableEnv(env) {
				continue
			}
			directory, mirrored := orchestratorReviewDirectory(tenant.Name, env)
			out = append(out, orchestratorEnvCandidate{
				Tenant:           tenant.Name,
				Environment:      env.Name,
				DefaultDirectory: directory,
				Mirrored:         mirrored,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tenant != out[j].Tenant {
			return out[i].Tenant < out[j].Tenant
		}
		return out[i].Environment < out[j].Environment
	})
	return out, nil
}

// resolveEnvInputs turns the frontend's selection into links. A mirror directory
// is the operator's to place, so their input wins; a local-agent env's review
// directory is derived from its repository path, so it is resolved here and any
// supplied value ignored — that keeps the env config the single source of truth
// and a link from outliving a repository path that moved.
func (a *App) resolveEnvInputs(inputs []orchestratorEnvInput) ([]eruncommon.OrchestratorEnvConfig, error) {
	refs := make([]eruncommon.OrchestratorEnvConfig, 0, len(inputs))
	for _, input := range inputs {
		tenant := strings.TrimSpace(input.Tenant)
		environment := strings.TrimSpace(input.Environment)
		if tenant == "" || environment == "" {
			continue
		}
		env, _, err := a.deps.store.LoadEnvConfig(tenant, environment)
		if err != nil {
			return nil, fmt.Errorf("load %s/%s: %w", tenant, environment, err)
		}
		derived, mirrored := orchestratorReviewDirectory(tenant, env)
		dir := derived
		if mirrored {
			if supplied := strings.TrimSpace(input.Directory); supplied != "" {
				dir = supplied
			}
		} else if dir == "" {
			return nil, fmt.Errorf("%s/%s has no repository path on this machine; set one before linking it to an orchestrator", tenant, environment)
		}
		refs = append(refs, eruncommon.OrchestratorEnvConfig{Tenant: tenant, Environment: environment, Directory: dir})
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("an orchestrator must link at least one environment")
	}
	return refs, nil
}

// wireEnvironmentReview prepares the env's host review directory. A remote-agent
// env needs the mirror created and its one-way sync turned on so the window fills
// from the pod (SSHD must be deployed for it to flow; enabling it here is the
// configuration half, activated on the env's next open/deploy). A local-agent env
// needs neither: the pod hostPath-mounts the worktree, so the directory already
// exists and no sync is involved — creating it would hand the orchestrator an
// empty review window instead of surfacing a repository path that is not there.
func (a *App) wireEnvironmentReview(ref eruncommon.OrchestratorEnvConfig) error {
	env, _, err := a.deps.store.LoadEnvConfig(ref.Tenant, ref.Environment)
	if err != nil {
		return fmt.Errorf("load %s/%s: %w", ref.Tenant, ref.Environment, err)
	}
	if _, mirrored := orchestratorReviewDirectory(ref.Tenant, env); !mirrored {
		info, statErr := os.Stat(ref.Directory)
		if statErr != nil || !info.IsDir() {
			return fmt.Errorf("%s/%s has no worktree at %s; check its repository path before linking it to an orchestrator", ref.Tenant, ref.Environment, ref.Directory)
		}
		return nil
	}
	if err := os.MkdirAll(ref.Directory, 0o755); err != nil {
		return fmt.Errorf("create orchestrator directory %s: %w", ref.Directory, err)
	}
	env.SSHD.Enabled = true
	env.SSHD.WorkspaceSync.Enabled = true
	env.SSHD.WorkspaceSync.LocalPath = ref.Directory
	if err := a.deps.store.SaveEnvConfig(ref.Tenant, env); err != nil {
		return fmt.Errorf("enable workspace sync for %s/%s: %w", ref.Tenant, ref.Environment, err)
	}
	return nil
}

func orchestratorDisplayName(name string, envs []eruncommon.OrchestratorEnvConfig) string {
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		return trimmed
	}
	if tenants := tenantsFromEnvs(envs); len(tenants) > 0 {
		return strings.Join(tenants, ", ")
	}
	return "orchestrator"
}

// CreateOrchestrator persists a new orchestrator: it prepares each linked env's
// host review directory (creating the mirror and wiring its sync where the env
// needs one), then stores the definition. Created stopped — StartOrchestrator
// spawns the session.
func (a *App) CreateOrchestrator(name string, envs []orchestratorEnvInput) (orchestratorInfo, error) {
	refs, err := a.resolveEnvInputs(envs)
	if err != nil {
		return orchestratorInfo{}, err
	}
	for _, ref := range refs {
		if err := a.wireEnvironmentReview(ref); err != nil {
			return orchestratorInfo{}, err
		}
	}
	configs, err := a.loadOrchestratorConfigs()
	if err != nil {
		return orchestratorInfo{}, err
	}
	id := uniqueOrchestratorID(orchestratorDisplayName(name, refs), configs)
	displayName := orchestratorDisplayName(name, refs)
	def := eruncommon.OrchestratorConfig{ID: id, Name: displayName, Environments: refs}
	if err := a.saveOrchestratorConfigs(append(configs, def)); err != nil {
		return orchestratorInfo{}, err
	}
	return orchestratorInfoFor(id, displayName, refs, "stopped", 0, false, false), nil
}

// UpdateOrchestrator edits an existing orchestrator's linked environments and
// name, re-wiring sync for the current set.
func (a *App) UpdateOrchestrator(id, name string, envs []orchestratorEnvInput) (orchestratorInfo, error) {
	id = strings.TrimSpace(id)
	refs, err := a.resolveEnvInputs(envs)
	if err != nil {
		return orchestratorInfo{}, err
	}
	configs, err := a.loadOrchestratorConfigs()
	if err != nil {
		return orchestratorInfo{}, err
	}
	index := -1
	for i, config := range configs {
		if config.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return orchestratorInfo{}, fmt.Errorf("orchestrator %q not found", id)
	}
	for _, ref := range refs {
		if err := a.wireEnvironmentReview(ref); err != nil {
			return orchestratorInfo{}, err
		}
	}
	displayName := orchestratorDisplayName(name, refs)
	configs[index] = eruncommon.OrchestratorConfig{ID: id, Name: displayName, Environments: refs}
	if err := a.saveOrchestratorConfigs(configs); err != nil {
		return orchestratorInfo{}, err
	}
	status := "stopped"
	sessionID := 0
	busy := false
	if info, ok := a.runningOrchestratorInfo(id); ok {
		status = "running"
		sessionID = info.SessionID
		busy = info.Busy
	}
	return orchestratorInfoFor(id, displayName, refs, status, sessionID, busy, false), nil
}

// StartOrchestrator spawns the session for a persisted orchestrator definition,
// reusing an already-running one.
func (a *App) StartOrchestrator(id string, cols, rows int) (orchestratorInfo, error) {
	return a.startPersistedOrchestrator(id, "", "", cols, rows)
}

// StartOrchestratorWithResume is StartOrchestrator plus the conversation to
// continue and the prompt handed to it, so the boot restore path can make a
// rebuilt+restarted orchestrator carry on with its task instead of idling at the
// prompt. The conversation is named explicitly because a restart hand-off is the
// one path that must reach the session that asked for it, not merely the
// orchestrator it belongs to.
func (a *App) StartOrchestratorWithResume(id, conversationID, resumePrompt string, cols, rows int) (orchestratorInfo, error) {
	return a.startPersistedOrchestrator(id, conversationID, resumePrompt, cols, rows)
}

func (a *App) startPersistedOrchestrator(id, conversationID, resumePrompt string, cols, rows int) (orchestratorInfo, error) {
	id = strings.TrimSpace(id)
	if info, ok := a.runningOrchestratorInfo(id); ok {
		return info, nil
	}
	def, err := a.findOrchestratorConfig(id)
	if err != nil {
		return orchestratorInfo{}, err
	}
	return a.spawnOrchestratorSession(orchestratorSpawn{
		id:             def.ID,
		name:           def.Name,
		envs:           a.refreshLinkedEnvDirectories(def.Environments),
		conversationID: conversationID,
		resumePrompt:   resumePrompt,
		cols:           cols,
		rows:           rows,
	})
}

// refreshLinkedEnvDirectories re-derives each local-agent link's review directory
// from the env config as a session starts, so an orchestrator keeps reviewing a
// worktree whose repository path moved after it was linked. A mirror path is the
// operator's own choice, so it is left exactly as persisted.
func (a *App) refreshLinkedEnvDirectories(envs []eruncommon.OrchestratorEnvConfig) []eruncommon.OrchestratorEnvConfig {
	out := make([]eruncommon.OrchestratorEnvConfig, 0, len(envs))
	for _, ref := range envs {
		env, _, err := a.deps.store.LoadEnvConfig(ref.Tenant, ref.Environment)
		if err == nil {
			if derived, mirrored := orchestratorReviewDirectory(ref.Tenant, env); !mirrored && derived != "" {
				ref.Directory = derived
			}
		}
		out = append(out, ref)
	}
	return out
}

// RestartOrchestrator stops a persisted orchestrator's running session and spawns
// a fresh one, so the operator can recycle a stuck agent without losing context:
// spawnOrchestratorSession relaunches this orchestrator's own pinned session,
// which resumes its own conversation. The definition is resolved before teardown because
// stopOrchestratorSession forgets the live session, and a transient (Investigate)
// orchestrator has no persisted definition to respawn — findOrchestratorConfig
// then returns an error, so transients are intentionally not restartable.
func (a *App) RestartOrchestrator(id string, cols, rows int) (orchestratorInfo, error) {
	id = strings.TrimSpace(id)
	def, err := a.findOrchestratorConfig(id)
	if err != nil {
		return orchestratorInfo{}, err
	}
	a.stopOrchestratorSession(id)
	return a.spawnOrchestratorSession(orchestratorSpawn{
		id:   def.ID,
		name: def.Name,
		envs: a.refreshLinkedEnvDirectories(def.Environments),
		cols: cols,
		rows: rows,
	})
}

func (a *App) runningOrchestratorInfo(id string) (orchestratorInfo, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	session := a.orchestrators[id]
	managed := a.sessions[orchestratorSessionKey(id)]
	if session == nil || managed == nil || managed.closed {
		return orchestratorInfo{}, false
	}
	return orchestratorInfoFor(session.id, session.name, session.envs, "running", session.serial, session.aiBusy, session.transient), true
}

// runningOrchestratorConversation returns the conversation a live session is
// attached to and the scope it is wired to, so a restart records the exact
// session that asked for it. Empty when nothing is running for that id: there is
// then no conversation a resume could name.
func (a *App) runningOrchestratorConversation(id string) (string, []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	session := a.orchestrators[id]
	managed := a.sessions[orchestratorSessionKey(id)]
	if session == nil || managed == nil || managed.closed {
		return "", nil
	}
	return session.conversationID, orchestratorScopeOf(session.envs)
}

// orchestratorScope is the environment set an orchestrator is defined with right
// now, in the same shape a restart records, so the two can be compared on resume.
func (a *App) orchestratorScope(id string) ([]string, error) {
	def, err := a.findOrchestratorConfig(id)
	if err != nil {
		return nil, err
	}
	return orchestratorScopeOf(def.Environments), nil
}

// orchestratorScopeOf renders linked environments as sorted tenant/environment
// pairs. Review directories are deliberately left out: where an environment is
// reviewed on this host can move without changing what the orchestrator drives.
func orchestratorScopeOf(envs []eruncommon.OrchestratorEnvConfig) []string {
	out := make([]string, 0, len(envs))
	for _, env := range envs {
		tenant, environment := strings.TrimSpace(env.Tenant), strings.TrimSpace(env.Environment)
		if tenant == "" || environment == "" {
			continue
		}
		out = append(out, tenant+"/"+environment)
	}
	sort.Strings(out)
	return out
}

// orchestratorSpawn is one session launch: which orchestrator, which
// conversation it attaches to, and what that conversation is handed on arrival.
// An empty conversationID means the orchestrator's own pinned conversation.
type orchestratorSpawn struct {
	id             string
	name           string
	envs           []eruncommon.OrchestratorEnvConfig
	initialPrompt  string
	conversationID string
	resumePrompt   string
	transient      bool
	cols           int
	rows           int
}

// spawnOrchestratorSession launches the host AI harness in the shared
// orchestrators root and tracks the live session.
func (a *App) spawnOrchestratorSession(spawn orchestratorSpawn) (orchestratorInfo, error) {
	id, name, envs := spawn.id, spawn.name, spawn.envs
	transient := spawn.transient
	cols, rows := clampTerminalSize(spawn.cols, spawn.rows)
	// Wire each linked env's erun MCP into the orchestrator session so it drives
	// its envs through the MCP (raw/build/deploy/…) rather than raw kubectl.
	// Non-fatal: the orchestrator still launches without the env MCP, but an
	// agent with linked envs and none of their tools looks working and is not,
	// so the operator is told why instead of discovering it tool by tool.
	mcpConfigPath, mcpErr := a.writeOrchestratorMCPConfig(id, envs)
	if mcpErr != nil {
		log.Printf("erun-app: write orchestrator MCP config for %s: %v", id, mcpErr)
		a.emitAppNotification("warning", orchestratorMCPUnwiredNotice(name, mcpErr))
	}
	// A restart hand-off names the conversation that asked for it; every other
	// launch falls back to this orchestrator's own pinned one.
	conversationID := strings.TrimSpace(spawn.conversationID)
	if conversationID == "" {
		conversationID = orchestratorSessionID(id)
	}
	executable, args, err := a.deps.resolveOrchestratorLaunch(conversationID, spawn.initialPrompt, spawn.resumePrompt, mcpConfigPath)
	if err != nil {
		return orchestratorInfo{}, err
	}
	// Every orchestrator launches in the shared $HOME/orchestrators root, which
	// carries the one CLAUDE.md and the `<tenant>-<env>` mirror subdirectories.
	dir, err := a.ensureOrchestratorWorkspace()
	if err != nil {
		return orchestratorInfo{}, err
	}
	sessionEnv := []string{
		appSessionEnvVar + "=1",
		// The orchestrator's own id, so an agent driving from its shell can
		// record itself as the return target for a rebuild+restart (see the
		// erun-orchestrate skill). Empty for transient/Investigate sessions.
		"ERUN_ORCHESTRATOR_ID=" + id,
		"CLAUDE_CODE_SUBAGENT_MODEL=" + orchestratorModel,
	}
	// An orchestrator has no pod, so the outputs convention an in-pod agent
	// follows needs a host directory to point at. Without it a host-side agent
	// has nowhere its deliverables are expected, and the operator has no way to
	// see them. A transient session has no id and so no directory of its own.
	if outputsDir, outputsErr := ensureOrchestratorOutputsDir(id); outputsErr == nil {
		sessionEnv = append(sessionEnv, eruncommon.RuntimeOutputsDirEnvVar+"="+outputsDir)
	} else if strings.TrimSpace(id) != "" {
		log.Printf("erun-app: orchestrator outputs dir for %s: %v", id, outputsErr)
	}
	params := startTerminalSessionParams{
		Dir:        resolveTerminalStartDir(dir),
		Executable: executable,
		Args:       args,
		Env:        sessionEnv,
		Cols:       cols,
		Rows:       rows,
	}
	session, err := a.deps.startTerminal(params)
	if err != nil {
		return orchestratorInfo{}, err
	}

	a.mu.Lock()
	a.nextSerial++
	serial := a.nextSerial
	key := orchestratorSessionKey(id)
	managed := &managedTerminal{
		session:   session,
		key:       key,
		serial:    serial,
		kind:      sessionKindOrchestrator,
		startedAt: time.Now(),
	}
	a.sessions[key] = managed
	a.orchestrators[id] = &orchestratorSession{
		id:             id,
		serial:         serial,
		conversationID: conversationID,
		transient:      transient,
		name:           name,
		envs:           envs,
		startedAt:      time.Now(),
	}
	a.mu.Unlock()

	go a.streamSession(managed)
	// Record what is open now rather than on the way out: the desktop is just as
	// likely to be killed or to crash as to be quit cleanly, and only a record
	// written here survives that. A transient (Investigate) session has no
	// persisted definition to reopen, so it is deliberately not recorded.
	if !transient {
		if err := recordOpenOrchestrator(a.deps.orchestratorOpenPath, id); err != nil {
			log.Printf("erun-app: record open orchestrator %s: %v", id, err)
		}
	}
	return orchestratorInfoFor(id, name, envs, "running", serial, false, transient), nil
}

// ListOrchestrators merges the persisted definitions (each tagged running or
// stopped) with any transient running sessions (Investigate), id-ordered.
func (a *App) ListOrchestrators() []orchestratorInfo {
	configs, err := a.loadOrchestratorConfigs()
	if err != nil {
		configs = nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]orchestratorInfo, 0, len(configs)+len(a.orchestrators))
	seen := make(map[string]struct{}, len(configs))
	for _, config := range configs {
		status := "stopped"
		sessionID := 0
		busy := false
		if session := a.orchestrators[config.ID]; session != nil {
			if managed := a.sessions[orchestratorSessionKey(config.ID)]; managed != nil && !managed.closed {
				status = "running"
				sessionID = session.serial
				busy = session.aiBusy
			}
		}
		out = append(out, orchestratorInfoFor(config.ID, config.Name, config.Environments, status, sessionID, busy, false))
		seen[config.ID] = struct{}{}
	}
	for id, session := range a.orchestrators {
		if _, ok := seen[id]; ok {
			continue
		}
		managed := a.sessions[orchestratorSessionKey(id)]
		if managed == nil || managed.closed {
			continue
		}
		out = append(out, orchestratorInfoFor(id, session.name, session.envs, "running", session.serial, session.aiBusy, true))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// StopOrchestrator ends a running session while keeping a persisted definition;
// a transient session is forgotten entirely.
func (a *App) StopOrchestrator(id string) error {
	id = strings.TrimSpace(id)
	if a.stopOrchestratorSession(id) {
		return nil
	}
	if _, err := a.findOrchestratorConfig(id); err == nil {
		return nil
	}
	return fmt.Errorf("orchestrator %q not found", id)
}

func (a *App) stopOrchestratorSession(id string) bool {
	a.mu.Lock()
	session := a.orchestrators[id]
	key := orchestratorSessionKey(id)
	managed := a.sessions[key]
	if session != nil {
		delete(a.orchestrators, id)
	}
	if managed != nil {
		delete(a.sessions, key)
	}
	a.mu.Unlock()
	if managed != nil {
		_ = managed.Close()
	}
	// Stopping is the operator saying this orchestrator should not come back, so
	// it stays closed on every later launch. A restart clears and re-records in
	// the same breath, which is the same statement about what is open.
	if err := clearOpenOrchestrator(a.deps.orchestratorOpenPath, id); err != nil {
		log.Printf("erun-app: clear open orchestrator %s: %v", id, err)
	}
	return session != nil || managed != nil
}

// DeleteOrchestrator stops the session if running and removes the persisted
// definition. The linked environments' sync config is left intact.
func (a *App) DeleteOrchestrator(id string) error {
	id = strings.TrimSpace(id)
	a.stopOrchestratorSession(id)
	configs, err := a.loadOrchestratorConfigs()
	if err != nil {
		return err
	}
	filtered := make([]eruncommon.OrchestratorConfig, 0, len(configs))
	for _, config := range configs {
		if config.ID != id {
			filtered = append(filtered, config)
		}
	}
	return a.saveOrchestratorConfigs(filtered)
}

// uniqueOrchestratorID derives a stable, human-readable id from the name that
// does not collide with an existing definition.
func uniqueOrchestratorID(name string, existing []eruncommon.OrchestratorConfig) string {
	stem := orchestratorIDStem(name)
	taken := make(map[string]struct{}, len(existing))
	for _, config := range existing {
		taken[config.ID] = struct{}{}
	}
	candidate := stem
	for i := 2; ; i++ {
		if _, dup := taken[candidate]; !dup {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", stem, i)
	}
}

// orchestratorIDStem slugifies name into a stable id stem, falling back to
// "orchestrator" when the name carries no id-safe characters.
func orchestratorIDStem(name string) string {
	var slug strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			slug.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == ',':
			slug.WriteByte('-')
		}
	}
	stem := strings.Trim(slug.String(), "-")
	if stem == "" {
		return "orchestrator"
	}
	return stem
}
