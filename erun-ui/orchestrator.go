package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
// CLI on PATH, so it can drive the remote-agent environments it links. The real
// work happens in the pods — the orchestrator delegates edits and builds to the
// in-pod agents, reviews each env's one-way synced mirror on the host read-only,
// and runs host-native build artifacts to verify. It may build locally to help,
// but never edits the mirror (the next sync would overwrite it anyway).
//
// An orchestrator is a persisted definition (root config): a set of linked
// remote-agent environments, each mirrored to a host directory. The set
// reappears across restarts; the running session is ephemeral.

// orchestratorSession is a live orchestrator PTY. Persisted orchestrators are
// keyed by their config ID; transient ones (Investigate) carry their own display
// metadata since they have no config definition.
type orchestratorSession struct {
	id        string
	serial    int
	transient bool
	name      string
	envs      []eruncommon.OrchestratorEnvConfig
	startedAt time.Time
}

// orchestratorEnvInput is the frontend's env selection for create/update.
type orchestratorEnvInput struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Directory   string `json:"directory"`
}

// orchestratorEnvCandidate is a remote-agent env the operator can link, with the
// host directory its mirror defaults to.
type orchestratorEnvCandidate struct {
	Tenant           string `json:"tenant"`
	Environment      string `json:"environment"`
	DefaultDirectory string `json:"defaultDirectory"`
}

type orchestratorEnvInfo struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Directory   string `json:"directory"`
}

// orchestratorInfo is the JSON-safe view the frontend renders and attaches to.
type orchestratorInfo struct {
	ID           string                `json:"id"`
	Name         string                `json:"name"`
	Environments []orchestratorEnvInfo `json:"environments"`
	Tenants      []string              `json:"tenants"`
	Directories  []string              `json:"directories"`
	SessionID    int                   `json:"sessionId"`
	Status       string                `json:"status"`
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

// defaultOrchestratorDirectory is the host mirror an env defaults to:
// $HOME/orchestrators/<tenant>-<env>. Mirrors sit beside the shared CLAUDE.md,
// keyed by env so every orchestrator linking the same env reviews one synced copy.
func defaultOrchestratorDirectory(tenant, environment string) string {
	return filepath.Join(orchestratorsRoot(), strings.TrimSpace(tenant)+"-"+strings.TrimSpace(environment))
}

// orchestratorClaudeMd is the single CLAUDE.md every orchestrator shares in the
// orchestrators root. It mirrors the erun-orchestrate skill (the source of truth
// for the full workflow) and is generic on purpose: mirrors are discovered as the
// `<tenant>-<env>` subdirectories present, so there is no per-orchestrator
// environment list and no per-orchestrator folder.
const orchestratorClaudeMd = `# Orchestrator working directory

You are a **host-side erun orchestrator**. You coordinate work across the erun
remote-agent environments mirrored here, from the operator's machine. The real
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
environments are yours from recollection, or infer them from which mirror
directories happen to have files — read the config every time.

## Rules

- Each ` + "`<tenant>-<env>`" + ` subdirectory here is a **read-only** one-way mirror of a
  remote-agent environment's worktree, kept in sync from the pod. **Never edit
  files in a mirror** — the next sync overwrites them, and edits mislead you.
- To change code, **ask the in-pod agent** in the relevant environment to do it
  (drive it via ` + "`erun`" + ` / the env's MCP). Do not patch the mirror.
- **Review** changes on the host, read-only. A mirror is a one-way plain-directory
  copy of the pod's working tree — it needs no local git. Read the synced files, and
  for the authoritative diff of the agent's uncommitted work view it from the pod
  (the desktop app's Review, or ask the in-pod agent to run ` + "`git diff`" + `).
- **Verify** by running host-native build artifacts (e.g. a Windows ` + "`.exe`" + ` the pod
  cross-built) under a mirror's ` + "`.erun-outputs/`" + ` — the pod can't run a foreign-OS
  binary. You may build locally to help, but never edit the mirror.
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
  gets a clear heads-up before you run it — you inform and proceed, you do not wait.
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
	// block the orchestrator from launching.
	if err := ensureOrchestratorSkills(); err != nil {
		fmt.Fprintf(os.Stderr, "erun: could not install orchestrator skills: %v\n", err)
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

// hostSkillsSource resolves the directory holding the canonical erun skills
// (erun-skills/skills/<name>/) on the host. ERUN_SKILLS_DIR overrides it (tests
// and non-standard installs); otherwise it walks up from the erun-app executable
// to the erun repo root, since erun-app is built from source there
// (erun-cli/bin/erun-app). Returns "" when no source can be found, so the caller
// skips installation instead of failing.
func hostSkillsSource() string {
	if override := strings.TrimSpace(os.Getenv("ERUN_SKILLS_DIR")); override != "" {
		return override
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	for i := 0; i < 8; i++ {
		cand := filepath.Join(dir, "erun-skills", "skills")
		if info, statErr := os.Stat(cand); statErr == nil && info.IsDir() {
			return cand
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
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
// pod treat skill provenance identically. Returns nil when no skills source is
// resolvable so a distributed binary still launches cleanly.
func ensureOrchestratorSkills() error {
	root := hostSkillsSource()
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
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
	settings["hooks"] = hooks
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// orchestratorSessionStartHook is the SessionStart hook block: the contract-
// injecting command runs on both new starts (startup) and reopens (resume).
func orchestratorSessionStartHook(dir string) []any {
	command := map[string]any{"type": "command", "command": orchestratorSkillHookCommand(dir)}
	matcher := func(source string) map[string]any {
		return map[string]any{"matcher": source, "hooks": []any{command}}
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

func orchestratorInfoFor(id, name string, envs []eruncommon.OrchestratorEnvConfig, status string, sessionID int, transient bool) orchestratorInfo {
	return orchestratorInfo{
		ID:           id,
		Name:         name,
		Environments: envInfos(envs),
		Tenants:      tenantsFromEnvs(envs),
		Directories:  directoriesFromEnvs(envs),
		SessionID:    sessionID,
		Status:       status,
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

// orchestratorLaunchCommand resolves how to launch the host AI harness. It runs
// through the host shell so an npm claude.cmd / .ps1 shim resolves (ConPTY can't
// exec a .cmd directly). A non-empty initialPrompt seeds a fresh session (the
// Investigate flow); otherwise the launch resumes THIS orchestrator's own
// conversation (pinned by sessionID) — creating it on first open — so
// orchestrators no longer collide on `--continue`'s single most-recent
// conversation in the shared workspace. A non-empty resumePrompt is handed to
// the resumed (or freshly created) session so a rebuild+restart continues its
// task itself.
func orchestratorLaunchCommand(sessionID, initialPrompt, resumePrompt string) (string, []string, error) {
	if _, err := exec.LookPath(defaultAITool); err != nil {
		return "", nil, fmt.Errorf("the %q CLI was not found on PATH; install it to run an orchestrator", defaultAITool)
	}
	shell, args := buildOrchestratorLaunch(runtime.GOOS, sessionID, orchestratorSessionExists(sessionID), initialPrompt, resumePrompt)
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
func buildOrchestratorLaunch(goos, sessionID string, sessionExists bool, initialPrompt, resumePrompt string) (string, []string) {
	flags := orchestratorUltracodeFlag + " --model " + orchestratorModel
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
		command = fresh + " " + orchestratorPromptArg(initialPrompt)
	case strings.TrimSpace(resumePrompt) != "":
		arg := orchestratorPromptArg(resumePrompt)
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

func orchestratorPromptArg(prompt string) string {
	prompt = strings.ReplaceAll(prompt, "\n", " ")
	return `"` + strings.ReplaceAll(prompt, `"`, "'") + `"`
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

// ListOrchestratorEnvCandidates returns the remote-agent environments the
// operator can link, each with the host directory its mirror defaults to. Only
// remote-agent envs qualify — they are the ones whose worktree lives in a pod and
// syncs down to the host.
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
			if env.ResolvedType() != eruncommon.EnvironmentTypeRemoteAgent {
				continue
			}
			out = append(out, orchestratorEnvCandidate{
				Tenant:           tenant.Name,
				Environment:      env.Name,
				DefaultDirectory: defaultOrchestratorDirectory(tenant.Name, env.Name),
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

func (a *App) resolveEnvInputs(inputs []orchestratorEnvInput) ([]eruncommon.OrchestratorEnvConfig, error) {
	refs := make([]eruncommon.OrchestratorEnvConfig, 0, len(inputs))
	for _, input := range inputs {
		tenant := strings.TrimSpace(input.Tenant)
		environment := strings.TrimSpace(input.Environment)
		if tenant == "" || environment == "" {
			continue
		}
		dir := strings.TrimSpace(input.Directory)
		if dir == "" {
			dir = defaultOrchestratorDirectory(tenant, environment)
		}
		refs = append(refs, eruncommon.OrchestratorEnvConfig{Tenant: tenant, Environment: environment, Directory: dir})
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("an orchestrator must link at least one remote-agent environment")
	}
	return refs, nil
}

// wireEnvironmentSync creates the host mirror directory and turns on the env's
// one-way workspace sync into it, so the orchestrator's review window fills from
// the pod. SSHD must be deployed for the sync to flow; enabling it here is the
// configuration half, activated on the env's next open/deploy.
func (a *App) wireEnvironmentSync(ref eruncommon.OrchestratorEnvConfig) error {
	if err := os.MkdirAll(ref.Directory, 0o755); err != nil {
		return fmt.Errorf("create orchestrator directory %s: %w", ref.Directory, err)
	}
	env, _, err := a.deps.store.LoadEnvConfig(ref.Tenant, ref.Environment)
	if err != nil {
		return fmt.Errorf("load %s/%s: %w", ref.Tenant, ref.Environment, err)
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

// CreateOrchestrator persists a new orchestrator: it wires one-way sync for each
// linked remote-agent env (creating the host mirror directory), then stores the
// definition. Created stopped — StartOrchestrator spawns the session.
func (a *App) CreateOrchestrator(name string, envs []orchestratorEnvInput) (orchestratorInfo, error) {
	refs, err := a.resolveEnvInputs(envs)
	if err != nil {
		return orchestratorInfo{}, err
	}
	for _, ref := range refs {
		if err := a.wireEnvironmentSync(ref); err != nil {
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
	return orchestratorInfoFor(id, displayName, refs, "stopped", 0, false), nil
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
		if err := a.wireEnvironmentSync(ref); err != nil {
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
	if info, ok := a.runningOrchestratorInfo(id); ok {
		status = "running"
		sessionID = info.SessionID
	}
	return orchestratorInfoFor(id, displayName, refs, status, sessionID, false), nil
}

// StartOrchestrator spawns the session for a persisted orchestrator definition,
// reusing an already-running one.
func (a *App) StartOrchestrator(id string, cols, rows int) (orchestratorInfo, error) {
	return a.startPersistedOrchestrator(id, "", cols, rows)
}

// StartOrchestratorWithResume is StartOrchestrator plus a prompt handed to the
// resumed Claude session, so the boot restore path can make a rebuilt+restarted
// orchestrator continue its task itself instead of idling at the prompt.
func (a *App) StartOrchestratorWithResume(id, resumePrompt string, cols, rows int) (orchestratorInfo, error) {
	return a.startPersistedOrchestrator(id, resumePrompt, cols, rows)
}

func (a *App) startPersistedOrchestrator(id, resumePrompt string, cols, rows int) (orchestratorInfo, error) {
	id = strings.TrimSpace(id)
	if info, ok := a.runningOrchestratorInfo(id); ok {
		return info, nil
	}
	def, err := a.findOrchestratorConfig(id)
	if err != nil {
		return orchestratorInfo{}, err
	}
	return a.spawnOrchestratorSession(def.ID, def.Name, def.Environments, "", resumePrompt, false, cols, rows)
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
	return a.spawnOrchestratorSession(def.ID, def.Name, def.Environments, "", "", false, cols, rows)
}

func (a *App) runningOrchestratorInfo(id string) (orchestratorInfo, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	session := a.orchestrators[id]
	managed := a.sessions[orchestratorSessionKey(id)]
	if session == nil || managed == nil || managed.closed {
		return orchestratorInfo{}, false
	}
	return orchestratorInfoFor(session.id, session.name, session.envs, "running", session.serial, session.transient), true
}

// spawnOrchestratorSession launches the host AI harness in the first linked env's
// mirror directory and tracks the live session.
func (a *App) spawnOrchestratorSession(id, name string, envs []eruncommon.OrchestratorEnvConfig, initialPrompt, resumePrompt string, transient bool, cols, rows int) (orchestratorInfo, error) {
	cols, rows = clampTerminalSize(cols, rows)
	executable, args, err := a.deps.resolveOrchestratorLaunch(orchestratorSessionID(id), initialPrompt, resumePrompt)
	if err != nil {
		return orchestratorInfo{}, err
	}
	// Every orchestrator launches in the shared $HOME/orchestrators root, which
	// carries the one CLAUDE.md and the `<tenant>-<env>` mirror subdirectories.
	dir, err := a.ensureOrchestratorWorkspace()
	if err != nil {
		return orchestratorInfo{}, err
	}
	params := startTerminalSessionParams{
		Dir:        resolveTerminalStartDir(dir),
		Executable: executable,
		Args:       args,
		Env: []string{
			appSessionEnvVar + "=1",
			// The orchestrator's own id, so an agent driving from its shell can
			// record itself as the return target for a rebuild+restart (see the
			// erun-orchestrate skill). Empty for transient/Investigate sessions.
			"ERUN_ORCHESTRATOR_ID=" + id,
			"CLAUDE_CODE_SUBAGENT_MODEL=" + orchestratorModel,
		},
		Cols: cols,
		Rows: rows,
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
		id:        id,
		serial:    serial,
		transient: transient,
		name:      name,
		envs:      envs,
		startedAt: time.Now(),
	}
	a.mu.Unlock()

	go a.streamSession(managed)
	return orchestratorInfoFor(id, name, envs, "running", serial, transient), nil
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
		if session := a.orchestrators[config.ID]; session != nil {
			if managed := a.sessions[orchestratorSessionKey(config.ID)]; managed != nil && !managed.closed {
				status = "running"
				sessionID = session.serial
			}
		}
		out = append(out, orchestratorInfoFor(config.ID, config.Name, config.Environments, status, sessionID, false))
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
		out = append(out, orchestratorInfoFor(id, session.name, session.envs, "running", session.serial, true))
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
