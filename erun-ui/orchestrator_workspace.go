package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

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

// orchestratorReviewDirectory resolves where an orchestrator reviews an env on
// this machine, and whether that directory is a synced mirror. Three kinds, one
// per the answer it gives:
//
//   - A remote-agent env gets the mirror its workspace sync fills — the default
//     path below, which is what Mirrored=true reports.
//   - A local-agent env's worktree is already here, because the pod
//     hostPath-mounts it, so it is reviewed in place. The pod's in-pod agent
//     owns that tree (see orchestratorClaudeMd), not the orchestrator.
//   - A host env has no pod and no cluster at all, so there is nothing to
//     mount, sync, or delegate to: the review directory and the environment are
//     the very same path, and the orchestrator authors and builds in it. Same
//     derivation as a local-agent env, entirely different relationship to it.
//
// It yields the mirror path a remote-agent env would be wired to rather than ""
// when its sync is not on yet, and answers explicitly for a runtime env: it has
// no worktree and no mirror, so there is no review directory at all, rather than
// falling through to the mirror default meant for an env that does have a pod to
// sync from.
func orchestratorReviewDirectory(tenant string, env configpkg.EnvConfig) (string, bool) {
	switch env.ResolvedType() {
	case configpkg.EnvironmentTypeLocalAgent, configpkg.EnvironmentTypeHost:
		return strings.TrimSpace(env.LocalRepoPath), false
	case configpkg.EnvironmentTypeRuntime:
		return "", false
	case configpkg.EnvironmentTypeRemoteAgent:
		fallthrough
	default:
		return defaultOrchestratorDirectory(tenant, env.Name), true
	}
}

// orchestratorClaudeMd is the single CLAUDE.md every orchestrator shares in the
// orchestrators root. It mirrors the erun-orchestrate skill (the source of truth
// for the full workflow) and is generic on purpose: mirrors are discovered as the
// `<tenant>-<env>` subdirectories present, so there is no per-orchestrator
// environment list and no per-orchestrator folder.
const orchestratorClaudeMd = `# Orchestrator working directory

You are a **host-side erun orchestrator**. You coordinate work across the erun
agent environments linked to you, from the operator's machine. For a pod-backed
environment the real work happens in its pod — you delegate, review, and verify.
A host environment has no pod and no in-pod agent, so there you author and build
in its directory directly. Follow the ` + "`erun-orchestrate`" + ` skill.

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
  ` + "`orchestrators:`" + ` entry, and it is one of three kinds. The environment's
  ` + "`type`" + ` tells you which. A ` + "`<tenant>-<env>`" + ` subdirectory here is a one-way
  **mirror** of a remote-agent environment's worktree, kept in sync from its pod. A
  path outside this root is a **local-agent environment's own worktree**, which lives
  on this machine and is hostPath-mounted into its pod. A **host environment** is a
  plain directory on this machine with no pod and no cluster behind it at all:
  nothing syncs it, nothing mounts it, and nothing else owns it — its review
  directory *is* the environment.
- A **host** environment has no pod, so nothing runs the erun MCP edge for it and it
  has **no MCP tools at all**: no ` + "`exec_*`" + `, no ` + "`job_*`" + `, no ` + "`activity_lease_*`" + `.
  That is the type working as designed, not a link that failed to wire, and no restart
  will produce those tools. Reach it the way you reach any other directory on this
  machine — your own file and shell tools, in its ` + "`directory`" + `. ` + "`erun build`" + ` and
  ` + "`erun release`" + ` run there directly; ` + "`deploy`" + `, ` + "`pin`" + `, ` + "`open`" + `, ` + "`terraform`" + ` and
  ` + "`upgrade`" + ` all refuse a host environment, because there is no pod for them to act on.
- An entry whose ` + "`role`" + ` is ` + "`runtime`" + ` is a different relationship: you
  **operate** that environment — deploy, pin, observe — rather than review or
  delegate to it. It has no worktree to review and no in-pod agent to delegate to,
  so it carries no review ` + "`directory`" + ` at all. None of the review/delegate
  rules below apply to it: drive it directly through ` + "`erun`" + ` (` + "`deploy`" + `,
  ` + "`pin`" + `, ` + "`platform env`" + `, and equivalent commands) or the platform API,
  never through a directory on this host.
- **Never write into a mirror or a local-agent worktree** — the two review-directory
  kinds a pod owns. In a mirror the edit is simply lost — the next sync overwrites it.
  In a local-agent worktree it is worse: the edit *does* reach the pod, so it silently
  competes with the in-pod agent that owns that tree, in what is also the operator's
  own checkout. A **host** environment is the third kind and the one exception, for
  exactly the reason that rule gives: it has no pod, so there is no sync to lose the
  edit to and no in-pod agent to contend with. There you author, build, and review in
  the directory directly — that is what the link is for. Keep it pointed at a directory
  nothing else owns: a local-agent worktree is still that environment's, not yours.
- Your definition may also name **directories of your own** (` + "`directories:`" + ` on your
  ` + "`orchestrators:`" + ` entry): paths that belong to no environment at all — no tenant, no
  pod, no cluster, no version. They are yours to author, build, and review in directly, on
  exactly the terms the host case above describes, and they are how an operator points you
  at a directory on this machine without registering an environment for it. Nothing syncs
  or mounts them and nothing else owns them; there is no MCP edge for one either, so you
  reach it with your own file and shell tools.
- To change code in a **pod-backed** environment, **ask the in-pod agent** to do it
  (drive it via ` + "`erun`" + ` / the env's MCP). Never patch that directory yourself. A host
  environment has no in-pod agent to ask, so there you make the change yourself.
- **Review** changes on the host, read-only, for a pod-backed environment. A mirror is a
  one-way plain-directory copy of the pod's working tree with no git of its own, so read
  the synced files and take the authoritative diff of uncommitted work from the pod (the
  desktop app's Review, or ask the in-pod agent to run ` + "`git diff`" + `). A local-agent
  worktree *is* a real checkout, so ` + "`git -C <dir> diff`" + ` here is already authoritative
  — as it is in a host environment's directory, which is your own working state rather
  than a peer's.
- **Verify** by running host-native build artifacts (e.g. a Windows ` + "`.exe`" + ` the pod
  cross-built) — the pod can't run a foreign-OS binary. A mirror carries them under
  its read-only ` + "`.erun-outputs/`" + `; a local-agent environment has no mirror, so
  pull them with that env's ` + "`outputs_list`" + `/` + "`outputs_download`" + ` (or the
  desktop's Outputs) first. You may build locally to help, but never edit a pod-backed
  environment's review directory.
- **This directory is shared with every other orchestrator**, so anything here that is
  yours alone carries your id (` + "`$ERUN_ORCHESTRATOR_ID`" + `) in its name: a file
  addressed to the directory alone is one any orchestrator can read as its own or
  overwrite.
- File erun **platform** bugs with the ` + "`erun-file-issue`" + ` skill.

## Operating mode

- **Complete the authorized outcome.** Investigate, implement, and verify without
  re-asking about routine increments. Keep related work in the same PR when
  publication is requested; respect a request to stop before publishing or deploying.
- **Resolve routine ambiguity from evidence and sensible defaults.** Ask only
  when a missing decision or authority materially changes scope. Answer operator
  questions directly; a question alone does not authorize the hinted-at work.
- **Require a terminal outcome from delegated work.** Do not rely on automatic
  reinvocation to finish an agent's work: recovery is bounded, not guaranteed.
  Never accept a promise to report back after the run exits.
- **Supervise long work in a pod-backed environment through its job lifecycle.** Start
  it as a detached job, keep its activity lease for the job's lifetime, and use a
  bounded await instead of a hand-written poll loop or an open stream. When delegating
  a long gate, include this waiting contract in the task. A host environment has no
  job lifecycle to supervise — there is no MCP edge to start a job on — so long work
  there is a process you run and wait on yourself.
- **Respect exclusive worktree and gate leases.** If a claim is refused, report
  its holder and wait or use another environment; never retry-loop, clear the
  holder, or mutate the tree anyway.
- **Heavy gates claim the whole environment**, not just one worktree. Acquire
  the environment-wide exclusive claim before dispatch and hold it through the
  terminal verdict; a second clone in the same pod does not isolate resources.
- **A wait timeout is inconclusive, not a gate failure.** Read the detached job's
  own terminal record before reporting a verdict, and distinguish a clean pass, a
  zero exit that left orphaned work, and a real failure.
- **Authority comes from the request and applicable standing instructions**, not
  from the size of an earlier action. Do not expand a task into unrequested
  publishing, release, or cross-environment changes.
- **Finish with evidence, not an offer to do authorized work later.** If blocked,
  state what remains, what was checked, and the specific decision or access needed.
- **Verify end-to-end within the authorized scope.** For a requested rollout,
  drive the change into the real target (in a pod-backed environment the in-pod agent
  builds and deploys it; in a host environment you build it in its own directory),
  then reproduce the original flow against the running artifact and watch it succeed —
  never stop at "unit tests pass" or "it builds". State plainly anything you could
  not verify and why.
- **On completion, present the assumptions you took.** End with a concise list of
  every recommended assumption you made in place of asking, so the operator can
  course-correct. This list is required, not optional.
- **Land work through the target repository's review and merge queue.** A branch
  that arrives as a pull request may be gated and landed as one; a branch you
  caused to exist must reach MERGED, which the platform verifies against a
  successful recorded build of the merge commit and the remote's own tip. A
  closed pull request asserts a landing rather than proving one, so it is never
  the finish line. Do not finish delegated work by opening a pull request.
- Make irreversible, remote, and cross-environment actions explicit beforehand.
  A heads-up does not replace approval when the action is outside the authorized
  scope. General engineering and direct in-pod interaction rules remain in the
  target repository's AGENTS.md; orchestration is not required for those users.
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
	// Same contract for the reusable agents (erun-builder/erun-reviewer):
	// installed into ~/.claude/agents so a host orchestrator can delegate to
	// them, best-effort and reported the same way skills are.
	if err := ensureOrchestratorAgents(); err != nil {
		a.reportAgentsNotInstalled(err)
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

// ensureOrchestratorWorkspaceFor is ensureOrchestratorWorkspace plus the two
// things that belong to a particular orchestrator rather than to the shared
// root: its own role file, and the claim label its claim protocol addresses
// issues by. Separate from ensureOrchestratorWorkspace rather than an extra
// parameter on it: the workspace itself is id-independent, and the shared root
// is ensured from a dozen places that have no id to give.
//
// Seeding here rather than at creation means an orchestrator that already
// existed gets both on its next launch too, not only newly created ones.
func (a *App) ensureOrchestratorWorkspaceFor(id string) (string, error) {
	dir, err := a.ensureOrchestratorWorkspace()
	if err != nil {
		return "", err
	}
	// Best-effort, like the skills install and the SessionStart hook: neither
	// of these must stop the orchestrator launching, but they are reported
	// rather than passed over in silence.
	if roleErr := ensureOrchestratorRoleFile(dir, id); roleErr != nil {
		fmt.Fprintf(os.Stderr, "erun: could not seed orchestrator role file for %s: %v\n", id, roleErr)
	}
	if labelErr := a.ensureOrchestratorClaimLabel(a.ctx, dir, id); labelErr != nil {
		fmt.Fprintf(os.Stderr, "erun: could not provision the claim label for %s: %v\n", id, labelErr)
	}
	return dir, nil
}
