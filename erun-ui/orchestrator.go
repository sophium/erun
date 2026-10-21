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
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// An orchestrator is a host-side AI session that is NOT scoped to a single
// environment: it runs the AI harness on the operator's machine with the erun
// CLI on PATH, so it can drive the environments it links. For a POD-BACKED
// environment the real work happens in the pod — the orchestrator delegates edits
// and builds to the in-pod agent, reviews that env's worktree on the host
// read-only, and runs host-native build artifacts to verify. It may build locally
// to help, but never writes into a pod-backed environment's review directory: the
// in-pod agent owns that worktree. A host environment has no pod, so there is no
// in-pod agent to delegate to and no other owner of its directory — the
// orchestrator authors and builds in it directly (see orchestratorClaudeMd).
//
// An orchestrator is a persisted definition (root config): a set of linked
// environments, each with a host review directory of one of three kinds (see
// orchestratorReviewDirectory) — a remote-agent env's is the one-way mirror its
// workspace sync fills; a local-agent env's is the worktree itself, already on
// this machine because the pod hostPath-mounts it; a host env's is the
// environment, the same plain directory with nothing between the two — plus the
// directories it names for itself, which belong to no environment at all and are
// picked by path rather than derived from one. The set reappears across restarts;
// the running session is ephemeral.

// orchestratorSession is a live orchestrator PTY. Persisted orchestrators are
// keyed by their config ID; transient ones (Investigate) carry their own display
// metadata since they have no config definition.
type orchestratorSession struct {
	id     string
	serial int
	// agentID is the Claude background agent this PTY is attached to. Empty for
	// a transient (Investigate) session, which runs in the PTY itself.
	agentID   string
	transient bool
	name      string
	// alias is the erun platform alias this orchestrator acts as, held on the
	// session for the same reason name and dirs are: the Edit dialog reads a
	// running orchestrator's own snapshot, and a session that did not carry the
	// alias would report none and erase it on the next save.
	alias string
	// launched is what this session actually runs on, resolved at spawn. It is
	// only ever compared against the definition resolved now to flag a restart;
	// rows report the configured values, never these.
	launched orchestratorResolvedLaunch
	envs     []eruncommon.OrchestratorEnvConfig
	// wiredScope is the environment scope the agent's MCP config held when the
	// agent last started. An agent that was already running when this PTY
	// attached keeps the tools it started with, whatever envs says now.
	wiredScope []string
	// dirs are the orchestrator's own directories: paths it operates in that
	// belong to no environment. Held on the session so a running orchestrator
	// still reports them (the Edit dialog reads its scope from this snapshot),
	// and read from the persisted definition rather than re-derived, since
	// nothing about a directory changes behind the operator's back.
	dirs      []eruncommon.OrchestratorDirectoryConfig
	startedAt time.Time
	// aiBusy is the last turn-boundary report the poller observed. It is what
	// orchestratorInfoFor's Busy field reads for every snapshot this session
	// appears in (ListOrchestrators, runningOrchestratorInfo, ...), so a fresh
	// mount or reconnect renders the true state directly rather than depending
	// on having witnessed the ai-activity event that last changed it. See
	// reconcileOrchestratorActivity in session_heartbeat.go.
	aiBusy bool
	// aiBusyAtUnix is when that report was written, carried for the same
	// reason shellStartedAtUnix is: "working" alone cannot tell the operator
	// whether a turn just started or has been going for ten minutes, which is
	// the whole question a busy indicator is asked (#1343, finishing #1228).
	aiBusyAtUnix int64
	// shellRunning, shellCommand and shellStartedAtUnix are the last background
	// shell report the poller observed — a fact independent of aiBusy,
	// since a shell can keep running after the turn that started it ends. Same
	// snapshot treatment as aiBusy and for the same reason: orchestratorInfoFor
	// reads these directly so a fresh mount or reconnect renders the true state
	// without depending on the orchestrator-shell-activity event.
	shellRunning       bool
	shellCommand       string
	shellStartedAtUnix int64
	// pacingNudgeCount / pacingCapped / pacingLastNudgeAtUnix track the pacing
	// nudge bound (orchestrator_pacing.go): how many consecutive un-answered
	// nudges this orchestrator has been sent, whether the cap notice already
	// fired, and when the last one went out, so a fresh busy report or real
	// operator input can tell a rearm from a repeat.
	pacingNudgeCount      int
	pacingCapped          bool
	pacingLastNudgeAtUnix int64
	// pacingLastReason is the reason decideOrchestratorPacing last returned for
	// this orchestrator, so the reconciler logs a transition rather than
	// repeating the same line every 15s tick. A configured orchestrator this
	// desktop holds no session for has nowhere to keep this, so it is remembered
	// in App.unmanagedPacingReason instead.
	// See logOrchestratorPacingTransition.
	pacingLastReason orchestratorPacingReason
	// pacingAutoNudgeCount / pacingLastAutoNudgeAtUnix are the cumulative
	// record of every automatic pacer nudge ever delivered to this session,
	// and when the last one went out. Unlike pacingNudgeCount/pacingCapped
	// (the cap's live budget, zeroed by rearmOrchestratorPacing on every
	// answer), no pacing decision ever resets these: they are what lets the
	// hover card tell "never nudged" apart from "nudged repeatedly, answering
	// every time" once the budget has rearmed back to zero. They are not
	// immortal, though -- this struct itself does not survive a desktop
	// restart, so spawnOrchestratorSession seeds them from
	// orchestrator_nudge_history.go's persisted record on every (re)spawn of
	// the same orchestrator id, and every update writes back through to that
	// record. A genuinely new orchestrator (no persisted record for its id)
	// starts at zero, same as it always did.
	pacingAutoNudgeCount      int
	pacingLastAutoNudgeAtUnix int64
	// pacingWhipCount / pacingLastWhipAtUnix are the same cumulative record
	// for explicit operator-triggered whips (WhipNow), kept separate from the
	// automatic count so the card can say which mechanism acted.
	pacingWhipCount      int
	pacingLastWhipAtUnix int64
	// pacingLastCappedAtUnix is when this session last crossed the cap,
	// cumulative like the counters above: rearmOrchestratorPacing clears
	// pacingCapped (the live "currently at the cap" gauge) on every answer,
	// but this timestamp survives, so a session that resumed after being
	// capped still reports having been capped rather than reading identically
	// to one that never was.
	pacingLastCappedAtUnix int64
	// pacingHistoryUnreadable is set when the persisted nudge-history file
	// exists but could not be parsed at spawn time. It is not itself
	// cumulative history -- it says the cumulative fields above may be wrong
	// (silently reset to zero) rather than genuinely zero, so the hover card
	// can say "unknown" instead of asserting "never nudged".
	pacingHistoryUnreadable bool
}

// orchestratorEnvInput is the frontend's env selection for create/update.
// Role is empty for undeclared -- the same "empty means undeclared, never a
// default" contract eruncommon.OrchestratorEnvConfig.Role documents.
type orchestratorEnvInput struct {
	Tenant      string                         `json:"tenant"`
	Environment string                         `json:"environment"`
	Directory   string                         `json:"directory"`
	Role        eruncommon.OrchestratorEnvRole `json:"role"`
}

// orchestratorLaunchInput is the dialog's gateway, model and effort for
// create/update (eruncommon.OrchestratorConfig.UseGateway/Model/Effort). An
// empty Model or Effort is the default, never a value of its own.
type orchestratorLaunchInput struct {
	UseGateway bool   `json:"useGateway"`
	Model      string `json:"model"`
	Effort     string `json:"effort"`
}

// orchestratorLaunchChoices is what the dialog's gateway, model and effort
// controls offer. GatewayModels is the catalog's driveable set, empty when no
// catalog is configured; GatewayUndriveableModels the catalog entries the
// gateway cannot drive, so a recorded one can be labelled as such rather than
// as an uncurated id; DefaultModel and DefaultEffort are what an empty
// setting launches on off the gateway, GatewayDefaultModel what an empty model
// launches on through it ("" when the catalog names none, which falls back to
// DefaultModel).
type orchestratorLaunchChoices struct {
	GatewayConfigured        bool     `json:"gatewayConfigured"`
	GatewayModels            []string `json:"gatewayModels"`
	GatewayUndriveableModels []string `json:"gatewayUndriveableModels"`
	GatewayDefaultModel      string   `json:"gatewayDefaultModel"`
	DefaultModel             string   `json:"defaultModel"`
	EffortLevels             []string `json:"effortLevels"`
	DefaultEffort            string   `json:"defaultEffort"`
}

// orchestratorEnvCandidate is an env the operator considered linking, eligible
// or not. Mirrored distinguishes the kinds of review directory an eligible env
// carries: a workspace-sync mirror the operator may place anywhere, or the env's
// own directory on this machine, whose path is derived and fixed (both a
// local-agent worktree and a host environment's directory derive it this way).
// Neither
// applies to a runtime env: DefaultDirectory is "" and Mirrored is false, and
// RequiredRole names the one role (eruncommon.OrchestratorEnvRoleRuntime) it
// must be linked with — the dialog uses this to offer that role directly
// instead of the mirror/worktree directory controls, which have nothing to
// show for a link with no review directory. RequiredRole is "" when no single
// role is required, which is every type but runtime — the picker still offers
// only the roles that type allows, so a host env's "" excludes runtime rather
// than meaning "any role". An ineligible env carries no directory —
// IneligibleReason explains, in operator language, why it cannot be linked at
// all.
type orchestratorEnvCandidate struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	// EnvironmentType is the env's resolved type, carried so the dialog can
	// reason about the candidate the way the shared gate does instead of
	// re-deriving it: it decides which roles the picker offers (a host env
	// takes no runtime role) and which words describe the directory row (a
	// host env's is its own directory, with no pod mounting it).
	EnvironmentType  eruncommon.EnvironmentType     `json:"environmentType"`
	Eligible         bool                           `json:"eligible"`
	DefaultDirectory string                         `json:"defaultDirectory"`
	Mirrored         bool                           `json:"mirrored"`
	RequiredRole     eruncommon.OrchestratorEnvRole `json:"requiredRole,omitempty"`
	IneligibleReason string                         `json:"ineligibleReason"`
}

// orchestratorEnvInfo is the JSON-safe view of one linked environment.
// Activity is nil until the environment-activity poller (environment_activity.go)
// has observed this environment at least once — the same "nil means not yet
// observed" contract uiEnvironment.Activity already carries, reused here
// rather than duplicated so the two surfaces cannot drift on what "not yet
// observed" means.
type orchestratorEnvInfo struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Directory   string `json:"directory"`
	// Role is empty for undeclared -- see orchestratorEnvInput.Role.
	Role     eruncommon.OrchestratorEnvRole `json:"role"`
	Activity *uiEnvironmentActivitySnapshot `json:"activity,omitempty"`
	// Usage is the environment-usage poller's last cached reading for this env
	// (environment_usage.go), joined the same way Activity is — nil until the
	// poller has observed it at least once.
	Usage *uiEnvironmentUsageSnapshot `json:"usage,omitempty"`
}

// orchestratorInfo is the JSON-safe view the frontend renders and attaches to.
// Busy carries the same signal as the ai-activity event this orchestrator's
// SessionID is keyed by, so a snapshot fetched after a busy transition — a
// fresh mount, a window reopen, a reconnecting listener — renders the true
// state without having had to witness that event. The frontend seeds its
// event-keyed store from this field on every fetch (loadOrchestrators) rather
// than keeping a second source of truth; see aiActivitySlice.ts.
//
// ShellRunning/ShellCommand/ShellStartedAtUnix are the same treatment for a
// background shell: independent of Busy, since a shell can outlive the
// turn that started it, and carried directly rather than left to the
// orchestrator-shell-activity event alone. ShellStartedAtUnix is when the
// running shell was reported started, unix seconds, 0 when ShellRunning is
// false — the frontend derives elapsed time from it rather than the desktop
// formatting a string that immediately goes stale.
//
// NudgeCount/NudgeCapped/LastNudgeAtUnix carry orchestratorSession's pacing
// state (orchestrator_pacing.go) the same way: read directly rather than left
// to the operator to infer from the pane, so a hover can tell a session erun
// has given up nudging from one that has never needed a nudge. Zero/false for
// a stopped orchestrator: this live budget genuinely does not survive past
// its session, and there is nothing mid-cycle to report while nothing runs.
// NudgeCount/NudgeCapped are the cap's own live budget, reset on every answer —
// they say only whether the session is *currently* at the cap.
//
// AutoNudgeCount/LastAutoNudgeAtUnix and WhipCount/LastWhipAtUnix are the
// cumulative history behind that budget: how many automatic nudges, and how
// many explicit operator whips, this session has ever received, and when the
// last of each went out. No pacing decision resets these, and — unlike the
// live budget above — they are not lost when the session that reported them
// goes away: orchestrator_nudge_history.go persists them per orchestrator id
// and this is restored across a desktop restart or an explicit
// Stop-then-Start, so a session that answers every nudge still reports
// having been nudged rather than collapsing back to "never nudged". They do
// reset to zero for a genuinely new orchestrator (no prior record for its
// id), including one that reuses a deleted orchestrator's name-derived id.
// LastCappedAtUnix is the same treatment for the cap itself: it survives the
// rearm that clears NudgeCapped, so a session that has since resumed still
// reports having hit the cap before. HistoryUnreadable is true when the
// persisted record exists but could not be parsed, so the cumulative fields
// above are an unverified zero rather than an asserted "never nudged" — the
// frontend renders this case distinctly (orchestratorNudgeSummary.ts).
type orchestratorInfo struct {
	ID                     string                `json:"id"`
	Name                   string                `json:"name"`
	Environments           []orchestratorEnvInfo `json:"environments"`
	Tenants                []string              `json:"tenants"`
	Directories            []string              `json:"directories"`
	SessionID              int                   `json:"sessionId"`
	Status                 string                `json:"status"`
	Busy                   bool                  `json:"busy"`
	BusyAtUnix             int64                 `json:"busyAtUnix,omitempty"`
	Transient              bool                  `json:"transient"`
	ShellRunning           bool                  `json:"shellRunning"`
	ShellCommand           string                `json:"shellCommand,omitempty"`
	ShellStartedAtUnix     int64                 `json:"shellStartedAtUnix,omitempty"`
	NudgeCount             int                   `json:"nudgeCount"`
	NudgeCapped            bool                  `json:"nudgeCapped"`
	LastNudgeAtUnix        int64                 `json:"lastNudgeAtUnix,omitempty"`
	AutoNudgeCount         int                   `json:"autoNudgeCount"`
	LastAutoNudgeAtUnix    int64                 `json:"lastAutoNudgeAtUnix,omitempty"`
	WhipCount              int                   `json:"whipCount"`
	LastWhipAtUnix         int64                 `json:"lastWhipAtUnix,omitempty"`
	LastCappedAtUnix       int64                 `json:"lastCappedAtUnix,omitempty"`
	NudgeHistoryUnreadable bool                  `json:"nudgeHistoryUnreadable,omitempty"`
	// PacingUnreachable is true when this desktop holds no session for the
	// orchestrator while its own hooks report one running elsewhere, so erun
	// cannot pace it from here — see orchestratorPacingUnreachable. It is the
	// hover card's could-not half: without it, a session this desktop cannot
	// nudge reads exactly like one that needed no nudge.
	PacingUnreachable bool `json:"pacingUnreachable,omitempty"`
	// RestartRequired is true when this orchestrator's live session was spawned
	// with an environment scope that no longer matches its persisted one. A
	// live Claude Code session resolves --mcp-config once at launch, so nothing
	// short of a fresh spawn (Restart) can re-wire it: the session keeps tools
	// for an unlinked environment and has none for a newly linked one until
	// then. See orchestratorScopeMismatch.
	RestartRequired bool `json:"restartRequired"`
	// RoleChanged is true when a linked environment's Role was edited since
	// the live session launched. Distinct from RestartRequired: a role edit
	// never changes which MCP tools the session holds, so the dialog offers
	// the same Restart affordance without claiming the specific "tools
	// missing" reason that does not apply here. See orchestratorRolesChanged.
	RoleChanged bool `json:"roleChanged"`
	// Alias is the erun platform alias this orchestrator acts as, empty when it
	// declares none of its own (eruncommon.OrchestratorConfig.Alias). It is the
	// dialog's own field, seeded from here and sent back on save, so a value
	// written by `erun orchestrator set-alias` survives an unrelated edit made
	// in the desktop.
	Alias string `json:"alias,omitempty"`
	// UseGateway, Model and Effort are the launch settings as configured, never
	// as the live session launched them: the dialog seeds from here and sends
	// them back on save, so a row carrying launched values would re-persist them
	// over a later `erun orchestrator set-*`. Empty Model or Effort is the
	// default.
	UseGateway bool   `json:"useGateway,omitempty"`
	Model      string `json:"model,omitempty"`
	Effort     string `json:"effort,omitempty"`
	// LaunchSettingsChanged is true when the definition, resolved now, would
	// launch on a different model, effort or gateway than the live session runs
	// on -- or no longer resolves at all. Like RoleChanged it asks
	// for a Restart without claiming tools are missing: a session's model,
	// effort and gateway are fixed when it launches.
	LaunchSettingsChanged bool `json:"launchSettingsChanged"`
}

func orchestratorSessionKey(id string) string {
	return "orchestrator\x00" + id
}

// orchestratorIDFromSessionKey reverses orchestratorSessionKey, for the paths
// that hold a managed terminal but need the orchestrator id it belongs to
// (the pacing nudge rearm on real operator input, the respawn refusal check).
// ok is false for any key this function did not itself produce.
func orchestratorIDFromSessionKey(key string) (string, bool) {
	const prefix = "orchestrator\x00"
	if !strings.HasPrefix(key, prefix) {
		return "", false
	}
	return strings.TrimPrefix(key, prefix), true
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

// orchestratableEnv reports whether role may be linked against env — a thin
// wrapper over eruncommon.OrchestratorEnvRoleAllowed, the single decision this
// gate and the CLI's SetOrchestratorEnvRole both consult so neither can drift
// from the other on what a role is allowed to be. A local-agent or
// remote-agent env has a worktree to review and an in-pod agent to delegate
// to, so any role is fine; a host env has no pod and so no in-pod agent, but it
// does carry a real directory on this machine (see
// orchestratorReviewDirectory), so code, build and undeclared are fine for it —
// while the runtime role is not, since "operate directly" has no referent
// without a pod. A runtime env is the mirror image, so only the runtime role
// may be declared for it.
func orchestratableEnv(env eruncommon.EnvConfig, role eruncommon.OrchestratorEnvRole) bool {
	return eruncommon.OrchestratorEnvRoleAllowed(env.ResolvedType(), role)
}

// orchestratorIneligibilityReason explains, in the operator's language, why
// role cannot be declared for env — a thin wrapper over
// eruncommon.OrchestratorEnvRoleIneligibilityReason, reusing orchestratableEnv's
// own reasoning rather than restating it. Returns "" when the link is allowed.
func orchestratorIneligibilityReason(env eruncommon.EnvConfig, role eruncommon.OrchestratorEnvRole) string {
	return eruncommon.OrchestratorEnvRoleIneligibilityReason(env.ResolvedType(), role)
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
func orchestratorReviewDirectory(tenant string, env eruncommon.EnvConfig) (string, bool) {
	switch env.ResolvedType() {
	case eruncommon.EnvironmentTypeLocalAgent, eruncommon.EnvironmentTypeHost:
		return strings.TrimSpace(env.LocalRepoPath), false
	case eruncommon.EnvironmentTypeRuntime:
		return "", false
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
  caused to exist must reach MERGE, which the platform verifies against the
  recorded gate build and the remote's own tip. A closed pull request asserts a
  landing rather than proving one, so it is never the finish line. Do not finish
  delegated work by opening a pull request.
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

// orchestratorAgentMarker records, per installed agent, the sha256 of the
// <name>.md erun last installed — the sidecar-file counterpart of
// orchestratorSkillMarker, since an agent is one flat file rather than a
// directory. Mirrors the runtime pod's agents-install.sh marker.
const orchestratorAgentMarker = ".erun-agent-baked-sha256"

// ensureOrchestratorAgents installs every erun reusable agent into
// ~/.claude/agents so the orchestrator's Claude session can delegate to
// erun-builder/erun-reviewer by default, with no operator install step.
// Mirrors ensureOrchestratorSkills, adapted for flat <name>.md files instead of
// skill directories.
func ensureOrchestratorAgents() error {
	root, err := hostAgentsSource()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read agents source %s: %w", root, err)
	}
	home, homeErr := os.UserHomeDir()
	if strings.TrimSpace(home) == "" {
		if homeErr == nil {
			homeErr = errors.New("it resolved empty")
		}
		return fmt.Errorf("resolve the home directory to install agents into: %w", homeErr)
	}
	destRoot := filepath.Join(home, ".claude", "agents")
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if err := installOrRefreshOrchestratorAgent(filepath.Join(root, entry.Name()), filepath.Join(destRoot, entry.Name())); err != nil {
			return fmt.Errorf("install agent %s: %w", entry.Name(), err)
		}
	}
	return nil
}

// installOrRefreshOrchestratorAgent reconciles one installed agent file against
// its shipped source: install when absent; refresh when the installed copy is
// still the one erun wrote (marker matches, or a legacy copy with no marker);
// leave a copy the operator edited in place untouched. Mirrors
// installOrRefreshOrchestratorSkill for a single file instead of a directory.
func installOrRefreshOrchestratorAgent(src, dst string) error {
	if _, err := os.Stat(dst); err != nil {
		return copyOrchestratorAgent(src, dst) // absent — install
	}
	bakedSHA, err := fileSHA256(src)
	if err != nil {
		return err
	}
	instSHA, err := fileSHA256(dst)
	if err != nil {
		return err
	}
	marker := dst + orchestratorAgentMarker
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
		return copyOrchestratorAgent(src, dst)
	}
	return nil // edited in place — preserve the operator's copy
}

// copyOrchestratorAgent replaces dst with a fresh copy of src and records the
// shipped file's hash in its sidecar marker, so a later launch can distinguish
// an untouched copy from an edited one. Mirrors copyOrchestratorSkill for a
// single file instead of a directory tree.
func copyOrchestratorAgent(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return err
	}
	sha, err := fileSHA256(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst+orchestratorAgentMarker, []byte(sha+"\n"), 0o644)
}

// orchestratorContractFallback is printed when the shared CLAUDE.md is somehow
// missing, so a session still boots knowing it is under the contract.
const orchestratorContractFallback = "You are a host-side erun orchestrator. Read and follow the CLAUDE.md in this directory and the erun-orchestrate skill before doing anything, even a trivial-looking question."

// orchestratorSkillHookCommand is the SessionStart hook command written into the
// orchestrators root's .claude/settings.json. It INJECTS the operating contract
// into the session by printing the shared CLAUDE.md to plain stdout (added to the
// session context), so an orchestrator always has its contract in context instead
// of being asked to load a skill it can skip. Plain stdout — not JSON — sidesteps
// the 10,000-char additionalContext cap.
//
// Runs through node rather than cat/echo chained with a POSIX shell: this is a
// SessionStart hook, and Windows' own hook shell (PowerShell) parses
// `[ -n ... ]` test syntax as something else entirely rather than executing
// it. Node reads each file directly and writes its bytes straight to stdout,
// so neither file's body ever passes through the shell at all -- only the
// two forward-slashed paths are baked into the script, and node resolves
// identically regardless of the host's own hook shell.
func orchestratorSkillHookCommand(dir string) string {
	claudeMd := filepath.ToSlash(filepath.Join(dir, "CLAUDE.md"))
	// The per-orchestrator layer, injected AFTER the shared contract so the
	// ordering is the precedence rule: a role file can add to the common
	// contract or override a line of it, and a reader can see which won.
	//
	// ERUN_ORCHESTRATOR_ID is set at launch and resolved at run time, since
	// this settings file is shared by every orchestrator. A transient
	// (Investigate) session has an empty id by design, so the guard skips it
	// and it gets the shared contract only.
	//
	// Absent file is a silent no-op: most orchestrators will not have one, and
	// a missing file must neither fail the hook nor write to stderr.
	roleDir := filepath.ToSlash(dir)
	script := `try{const fs=require("fs");` +
		`try{process.stdout.write(fs.readFileSync("` + claudeMd + `","utf8"));}` +
		`catch(e){process.stdout.write("` + orchestratorContractFallback + `\n");}` +
		`const id=process.env.ERUN_ORCHESTRATOR_ID;` +
		`if(id){try{process.stdout.write(fs.readFileSync("` + roleDir + `/CLAUDE."+id+".md","utf8"));}catch(e){}}` +
		`}catch(e){}`
	return `node -e '` + script + `'`
}

// orchestratorRoleFileSeed is written once into CLAUDE.<id>.md and never
// rewritten. Deliberately the inverse of the shared CLAUDE.md, which erun
// overwrites on every launch: one layer is erun's and always current, the other
// is the operator's and always preserved. That asymmetry is the feature.
const orchestratorRoleFileSeed = `<!--
This file is yours. erun creates it once and never overwrites it, unlike the
shared CLAUDE.md in this directory, which erun rewrites on every launch.

It is injected on every session boundary -- start, resume, clear, and compact --
immediately AFTER the shared contract, so anything here can add to that
contract or override a line of it.

Put this orchestrator's standing role here: what it owns, what it must not do,
how it should report. A role is standing, so it belongs here rather than in a
task note, which is read once and superseded.

Do NOT list this orchestrator's environments here. The shared contract requires
knowing scope from erun's config and never from disk; a list baked in here would
contradict it and go stale the next time the orchestrator's links change.
-->
`

// ensureOrchestratorRoleFile creates the per-orchestrator role file if it is
// absent and leaves it strictly alone otherwise. Called on every launch rather
// than only at creation, so an orchestrator that already existed gets one too.
// An empty id (a transient session) seeds nothing.
//
// The id is slugified to [a-z0-9-] by orchestratorIDStem, so for any id erun
// mints the filename is safe by construction; it is still resolved with an exact
// filepath.Join and never a glob, so a hand-edited config.yaml id cannot widen
// what gets read.
func ensureOrchestratorRoleFile(dir, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	path := filepath.Join(dir, "CLAUDE."+id+".md")
	if _, err := os.Stat(path); err == nil {
		return nil // the operator's file; never rewritten
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(orchestratorRoleFileSeed), 0o644)
}

// ensureOrchestratorSessionStartHook writes a SessionStart hook into the shared
// orchestrators root's .claude/settings.json, merging so it never clobbers other
// keys, hook events, or hook blocks already there -- including a SessionStart
// block the operator added themselves. SessionStart fires on a new session
// (startup), a reopened one (resume), a cleared one (clear), and a compacted
// one (compact), so every orchestrator has its contract re-injected on every
// session boundary rather than only the first two.
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
	hooks["SessionStart"] = mergeOrchestratorHookBlocks(
		hooks["SessionStart"], orchestratorSessionStartHook(dir), isOrchestratorSessionStartHookBlock)
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
	// A background shell's start and its completion are both only visible in a
	// tool call's own result, which PreToolUse never carries — so unlike the
	// turn-boundary busy report above, this one lives on PostToolUse alone.
	// See orchestrator_shell_activity.go.
	hooks["PostToolUse"] = mergeOrchestratorHookBlocks(
		hooks["PostToolUse"], orchestratorShellActivityHookBlocks(), isOrchestratorShellActivityHookBlock)
	// Not installing the retired conversation recorders is not enough: every
	// machine that ever ran an older build still carries them, and they would go
	// on writing files nothing reads on every turn boundary. Strip them wherever
	// they are found.
	for event := range hooks {
		hooks[event] = pruneRetiredSessionRecorderHooks(hooks[event])
	}
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
// fires. ASCII-only and apostrophe-free, since it is embedded as a plain JS
// string inside a script wrapped in a single-quoted shell argument -- a
// literal single quote in the text would close that argument early.
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
// Every failure path falls through to node's default exit code, 0. A guard
// that wedged a session on a transcript it could not parse would cost more
// than the stalls it prevents, and an already nudged turn (stop_hook_active)
// is let go so a session is corrected once rather than looped.
//
// Runs through node rather than a POSIX shell reading its own stdin with sed
// and grep: this fires on every Stop event of every orchestrator, and
// Windows' own hook shell (PowerShell) parses `[ -f ... ]` test syntax as
// something else entirely rather than executing it. Node needs no helper
// binary on PATH -- the AI harness that launched the session is itself an
// npm package -- and resolves identically regardless of the host's own hook
// shell.
//
// It reads the turn's last ASSISTANT entry, never the raw tail of the
// transcript. A firing is itself recorded in the transcript, and the record
// carries this command — every trigger phrase included — so a window of raw
// lines matches the guard's own echo from then on and the session can never
// end again. It is also what let an operator's own question ("would you like
// me to…") refuse the reply that answered it. Only what the turn said can
// decide whether the turn handed back a decision.
//
// Within that entry, only the turn's own unquoted words count. A report that
// quotes an earlier violation -- or the contract, or the operator -- is
// describing an offer, not making one, and re-firing on it refuses the very
// turn that documents the fix. Quoted, code-spanned, and blockquoted spans are
// cut before the trigger phrases are matched, so a genuine offer still fires
// and a citation of one does not.
func orchestratorNoAskStopGuardCommand() string {
	script := `/*` + orchestratorNoAskGuardMarker + `*/` +
		`let d="";process.stdin.on("data",c=>{d+=c});process.stdin.on("end",()=>{try{` +
		`const j=JSON.parse(d);` +
		`if(j.stop_hook_active===true)return;` +
		`const fs=require("fs");` +
		`const lines=fs.readFileSync(j.transcript_path,"utf8").split("\n");` +
		`const tail=lines.slice(-40);` +
		`let said="";` +
		`for(const line of tail){if(/"type"\s*:\s*"assistant"/.test(line))said=line;}` +
		`let spoken=said;` +
		`try{const c=JSON.parse(said).message.content;` +
		`if(typeof c==="string")spoken=c;` +
		`else if(Array.isArray(c)){let own="";for(const b of c){if(b&&typeof b.text==="string")own+=b.text+"\n";}if(own)spoken=own;}}catch(e){}` +
		`const cited=/\x60\x60\x60[\s\S]*?\x60\x60\x60|\x60[^\x60]*\x60|"[^"]*"|\u201C[^\u201D]*\u201D|(^|[\s(\[{:,])\u0027[^\u0027\n]*\u0027|^[ \t]*>.*$/gm;` +
		`const trigger=/say the word|let me know if|let me know whether|shall i |do you want me to|would you like me to|next action is yours|if you.d like me to|your call/i;` +
		`if(!trigger.test(spoken.replace(cited," ")))return;` +
		`process.stderr.write("` + orchestratorNoAskStopGuardReason + `");` +
		`process.exit(2);` +
		`}catch(e){}});`
	return `node -e '` + script + `'`
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

// orchestratorSessionStartMatcher is a single pattern matching every source
// this hook cares about, rather than separate matcher blocks carrying the same
// three commands: SessionStart's matcher is tested as a regex against the
// event's source, and alternation is already how this file scopes a matcher to
// more than one value (see orchestratorShellActivityHookBlocks' "TaskOutput|
// TaskStop"). Several blocks with identical hooks would mean every SessionStart
// command is stored multiple times on disk for no behavioral gain.
//
// clear and compact both fire SessionStart, and both were missing here (#1232):
// a /clear drops the injected CLAUDE.<id>.md role layer from context even
// though the file survives on disk, and a compaction forks the transcript to a
// new session id that the live-session record then never picks up. Printing
// the contract again on either is harmless (plain stdout, not a file append);
// omitting it is not.
const orchestratorSessionStartMatcher = "startup|resume|clear|compact"

// orchestratorSessionStartHook is the SessionStart hook block: the contract-
// injecting command runs on new starts (startup), reopens (resume), and every
// session boundary that can otherwise drop it from context (clear, compact).
func orchestratorSessionStartHook(dir string) []any {
	command := map[string]any{"type": "command", "command": orchestratorSkillHookCommand(dir)}
	// A session killed mid-turn never writes its end, so a new or reopened one
	// would inherit the previous run's "working" and spin on arrival with nothing
	// running. Clearing it here is what makes that guarantee real.
	idle := map[string]any{"type": "command", "command": orchestratorActivityHookCommand(false)}
	// A background shell report has the exact same hazard: a shell the previous
	// run started (or its clear) can outlive that run entirely, and nothing else
	// resets it at a boundary — see orchestrator_shell_activity.go.
	shellIdle := map[string]any{"type": "command", "command": orchestratorShellActivityResetHookCommand()}
	return []any{map[string]any{
		"matcher": orchestratorSessionStartMatcher,
		"hooks":   []any{command, idle, shellIdle},
	}}
}

// isOrchestratorSessionStartHookBlock reports whether a settings hook block is
// one of ours, so a rewrite recognizes and replaces any block(s) it previously
// wrote -- whether that is today's single combined-matcher block or the pair of
// per-source blocks an earlier version of this file installed -- instead of
// stacking another copy beside them. The contract-injection command's fallback
// text is unique to us and present regardless of which shape wrote it.
func isOrchestratorSessionStartHookBlock(block any) bool {
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
		if command, ok := entry["command"].(string); ok && strings.Contains(command, orchestratorContractFallback) {
			return true
		}
	}
	return false
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

// envInfos joins each linked env against the environment-activity poller's
// last observation for it, keyed the same way the poller itself keys the
// sidebar's per-env state (selectionKey) — so an orchestrator's card and the
// sidebar row for the same environment can never disagree about what "busy"
// or "outage" means, because both read the one map the poller writes.
func envInfos(envs []eruncommon.OrchestratorEnvConfig, envActivity map[string]environmentActivityState, envUsage map[string]environmentUsageReading) []orchestratorEnvInfo {
	out := make([]orchestratorEnvInfo, 0, len(envs))
	for _, env := range envs {
		info := orchestratorEnvInfo{Tenant: env.Tenant, Environment: env.Environment, Directory: env.Directory, Role: env.Role}
		key := selectionKey(uiSelection{Tenant: env.Tenant, Environment: env.Environment})
		if state, ok := envActivity[key]; ok {
			info.Activity = &uiEnvironmentActivitySnapshot{
				Reachable:   state.reachable,
				Observed:    state.observed,
				Outage:      state.outage,
				CheckFailed: state.checkFailed,
				Busy:        state.busy,
				Detail:      state.detail,
			}
		}
		if reading, ok := envUsage[key]; ok {
			info.Usage = uiEnvironmentUsageSnapshotFrom(reading)
		}
		out = append(out, info)
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

// directoryPaths lists the orchestrator's own directories for the payload the
// desktop renders. Only the non-empty ones survive, so a definition with a blank
// entry (hand-edited config) reports the directories it actually has rather than
// a row the operator cannot act on.
func directoryPaths(dirs []eruncommon.OrchestratorDirectoryConfig) []string {
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if path := strings.TrimSpace(dir.Directory); path != "" {
			out = append(out, path)
		}
	}
	return out
}

// orchestratorShellSnapshot is the background-shell half of orchestratorInfo,
// grouped into one value so orchestratorInfoFor's call sites read as "this
// session's shell state" rather than three more positional bools and strings
// indistinguishable from the ones beside them.
type orchestratorShellSnapshot struct {
	Running       bool
	Command       string
	StartedAtUnix int64
}

// orchestratorBusySnapshot is the turn-busy half of orchestratorInfo, grouped
// for the same reason orchestratorShellSnapshot is: the alternative is another
// positional bool and int64 beside the ones already there, indistinguishable at
// the call site.
type orchestratorBusySnapshot struct {
	Busy   bool
	AtUnix int64
}

// orchestratorPacingSnapshot is the pacing/nudge half of orchestratorInfo,
// grouped for the same reason orchestratorBusySnapshot and
// orchestratorShellSnapshot are: read from orchestratorSession's own pacing
// fields (orchestrator_pacing.go) rather than left for the frontend to infer
// from the pane.
type orchestratorPacingSnapshot struct {
	NudgeCount      int
	Capped          bool
	LastNudgeAtUnix int64
	// AutoNudgeCount/LastAutoNudgeAtUnix and WhipCount/LastWhipAtUnix are the
	// cumulative history behind NudgeCount/Capped's live budget — see
	// orchestratorSession's pacingAutoNudgeCount/pacingWhipCount doc comment.
	AutoNudgeCount      int
	LastAutoNudgeAtUnix int64
	WhipCount           int
	LastWhipAtUnix      int64
	// LastCappedAtUnix is the same cumulative treatment for the cap: it
	// survives a rearm that clears Capped, so a session that has since
	// resumed still reports having hit the cap before.
	LastCappedAtUnix int64
	// HistoryUnreadable is the orchestratorSession field of the same name:
	// the cumulative fields above are known to be a possibly-wrong zero
	// rather than a genuine "never nudged", because the persisted record
	// could not be read back.
	HistoryUnreadable bool
	// Unreachable is true for a configured orchestrator this desktop holds no
	// session for while its own records show one running — the population the
	// pacer can decide for but never nudge (see orchestratorPacingUnreachable).
	// It is a property of the record, not of a session, so it is set by the
	// caller that knows which orchestrators have no session here.
	Unreachable bool
}

// orchestratorPacingSnapshotFromSession reads a live session's pacing state,
// gauge and history alike, into the JSON-safe snapshot shape -- the one place
// that mapping happens, so every caller (ListOrchestrators, runningOrchestratorInfo)
// stays in lockstep as pacing gains fields.
func orchestratorPacingSnapshotFromSession(session *orchestratorSession) orchestratorPacingSnapshot {
	return orchestratorPacingSnapshot{
		NudgeCount:          session.pacingNudgeCount,
		Capped:              session.pacingCapped,
		LastNudgeAtUnix:     session.pacingLastNudgeAtUnix,
		AutoNudgeCount:      session.pacingAutoNudgeCount,
		LastAutoNudgeAtUnix: session.pacingLastAutoNudgeAtUnix,
		WhipCount:           session.pacingWhipCount,
		LastWhipAtUnix:      session.pacingLastWhipAtUnix,
		LastCappedAtUnix:    session.pacingLastCappedAtUnix,
		HistoryUnreadable:   session.pacingHistoryUnreadable,
	}
}

// orchestratorPacingSnapshotFromHistory builds the pacing snapshot for an
// orchestrator with no live session (ListOrchestrators' "stopped" case) from
// the persisted record alone. The live-cap fields (NudgeCount/Capped/
// LastNudgeAtUnix) are correctly left zero: nothing is mid-nudge-cycle when
// nothing is running.
func orchestratorPacingSnapshotFromHistory(entry orchestratorNudgeHistoryEntry, unreadable bool) orchestratorPacingSnapshot {
	return orchestratorPacingSnapshot{
		AutoNudgeCount:      entry.AutoNudgeCount,
		LastAutoNudgeAtUnix: entry.LastAutoNudgeAtUnix,
		WhipCount:           entry.WhipCount,
		LastWhipAtUnix:      entry.LastWhipAtUnix,
		LastCappedAtUnix:    entry.LastCappedAtUnix,
		HistoryUnreadable:   unreadable,
	}
}

func orchestratorInfoFor(id, name, alias string, envs []eruncommon.OrchestratorEnvConfig, dirs []eruncommon.OrchestratorDirectoryConfig, status string, sessionID int, busy orchestratorBusySnapshot, transient bool, shell orchestratorShellSnapshot, pacing orchestratorPacingSnapshot, envActivity map[string]environmentActivityState, envUsage map[string]environmentUsageReading, restartRequired, roleChanged bool) orchestratorInfo {
	return orchestratorInfo{
		ID:                     id,
		Name:                   name,
		Alias:                  alias,
		Environments:           envInfos(envs, envActivity, envUsage),
		Tenants:                tenantsFromEnvs(envs),
		Directories:            directoryPaths(dirs),
		SessionID:              sessionID,
		Status:                 status,
		Busy:                   busy.Busy,
		BusyAtUnix:             busy.AtUnix,
		ShellRunning:           shell.Running,
		ShellCommand:           shell.Command,
		ShellStartedAtUnix:     shell.StartedAtUnix,
		Transient:              transient,
		NudgeCount:             pacing.NudgeCount,
		NudgeCapped:            pacing.Capped,
		LastNudgeAtUnix:        pacing.LastNudgeAtUnix,
		AutoNudgeCount:         pacing.AutoNudgeCount,
		LastAutoNudgeAtUnix:    pacing.LastAutoNudgeAtUnix,
		WhipCount:              pacing.WhipCount,
		LastWhipAtUnix:         pacing.LastWhipAtUnix,
		LastCappedAtUnix:       pacing.LastCappedAtUnix,
		NudgeHistoryUnreadable: pacing.HistoryUnreadable,
		PacingUnreachable:      pacing.Unreachable,
		RestartRequired:        restartRequired,
		RoleChanged:            roleChanged,
	}
}

// orchestratorScopeMismatch reports whether a running agent's wired
// environment scope (wiredScope, i.e. what its MCP config held when the agent
// last started) has drifted from the environments it is configured with right
// now. True means the live agent still holds tools for an environment it was
// unlinked from, is missing tools for one it was newly linked to, or both —
// and only restarting it (RestartOrchestrator) fixes it.
func orchestratorScopeMismatch(wiredScope []string, configuredEnvs []eruncommon.OrchestratorEnvConfig) bool {
	return !equalOrchestratorScope(normalizedOrchestratorScope(wiredScope), orchestratorScopeOf(configuredEnvs))
}

// normalizedOrchestratorScope reads a recorded scope the way orchestratorScopeOf
// renders one, so a record that held no environments compares equal to none.
func normalizedOrchestratorScope(scope []string) []string {
	out := make([]string, 0, len(scope))
	out = append(out, scope...)
	sort.Strings(out)
	return out
}

// orchestratorRolesChanged reports whether any environment linked in both
// wiredEnvs and configuredEnvs now carries a different Role. Unlike scope,
// nothing the running session already resolved is keyed on Role -- it wires no
// MCP tool differently for a code environment than a build one -- so this is
// not the same "the process is stale" fact orchestratorScopeMismatch reports.
// It exists so the Edit orchestrator dialog can tell the operator their edit
// is not guaranteed live yet without overloading RestartRequired's specific,
// accurate "tools are missing" wording with a change that never affects tools.
func orchestratorRolesChanged(wiredEnvs, configuredEnvs []eruncommon.OrchestratorEnvConfig) bool {
	wiredRoles := make(map[string]eruncommon.OrchestratorEnvRole, len(wiredEnvs))
	for _, env := range wiredEnvs {
		wiredRoles[env.Tenant+"/"+env.Environment] = env.Role
	}
	for _, env := range configuredEnvs {
		if wired, ok := wiredRoles[env.Tenant+"/"+env.Environment]; ok && wired != env.Role {
			return true
		}
	}
	return false
}

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

// orchestratorLaunchCommand resolves what the orchestrator's PTY runs, through
// the host shell so an npm claude.cmd / .ps1 shim resolves (ConPTY can't exec a
// .cmd directly). A persisted orchestrator attaches to its background agent
// (agentID); the agent, not the PTY, owns the conversation. A transient
// (Investigate) session has no agent and runs a fresh session in the PTY,
// seeded with initialPrompt. launch supplies its model and effort.
func orchestratorLaunchCommand(launch eruncommon.OrchestratorLaunch, agentID, initialPrompt, mcpConfigPath string) (string, []string, error) {
	if _, err := exec.LookPath(defaultAITool); err != nil {
		return "", nil, fmt.Errorf("the %q CLI was not found on PATH; install it to run an orchestrator", defaultAITool)
	}
	if strings.TrimSpace(agentID) != "" {
		if !claudeAgentIDPattern.MatchString(agentID) {
			return "", nil, fmt.Errorf("cannot attach to Claude background agent %q: not an agent id", agentID)
		}
		shell, args := orchestratorAttachCommand(runtime.GOOS, agentID)
		return shell, args, nil
	}
	shell, args := buildOrchestratorLaunch(runtime.GOOS, launch, initialPrompt, mcpConfigPath)
	return shell, args, nil
}

// claudeAgentIDPattern is the shape of a background agent id. It is spliced
// into a shell line, so nothing else is accepted.
var claudeAgentIDPattern = regexp.MustCompile(`^[0-9a-f]{6,64}$`)

// buildOrchestratorLaunch composes a transient session's host-shell invocation
// at launch's effort on launch's model. Both are spliced unquoted, which is safe
// only because eruncommon.ResolveOrchestratorLaunch admits nothing but a plain
// model token and a known effort level.
func buildOrchestratorLaunch(goos string, launch eruncommon.OrchestratorLaunch, initialPrompt, mcpConfigPath string) (string, []string) {
	quote := shellQuote
	if goos == "windows" {
		quote = powerShellQuote
	}
	command := defaultAITool + launch.EffortFlags + orchestratorNoAskFlag + " --model " + launch.Model
	if strings.TrimSpace(mcpConfigPath) != "" {
		command += " --mcp-config " + quote(mcpConfigPath)
	}
	if strings.TrimSpace(initialPrompt) != "" {
		command += " " + orchestratorPromptArg(goos, initialPrompt)
	}
	shell, shellArgs := resolveLocalShellCommand(goos)
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
	configs, _, err := a.loadOrchestratorConfigsWithCatalog()
	return configs, err
}

// loadOrchestratorConfigsWithCatalog is loadOrchestratorConfigs plus the
// gateway catalog from the same read, for a poll that needs both.
func (a *App) loadOrchestratorConfigsWithCatalog() ([]eruncommon.OrchestratorConfig, *eruncommon.OpenRouterConfig, error) {
	config, _, err := a.deps.store.LoadERunConfig()
	if errors.Is(err, eruncommon.ErrNotInitialized) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return config.Orchestrators, config.OpenRouter, nil
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

// ListOrchestratorAliasChoices returns the erun platform aliases configured on
// this host, for the Edit orchestrator dialog's Platform alias picker.
//
// A picker rather than a text field, for the reason the environment candidate
// list is one: the writer refuses an alias this host cannot resolve
// (eruncommon.ValidateOrchestratorAlias), so offering the resolved list is what
// keeps the dialog from letting an operator choose something the save will
// reject. An empty list is a real answer — no erun alias is configured on this
// machine — and the control renders it as such rather than as an empty choice.
func (a *App) ListOrchestratorAliasChoices() ([]string, error) {
	return a.configuredERunAliases()
}

// ListOrchestratorLaunchChoices returns what the Edit orchestrator dialog's
// gateway, model and effort controls offer, read from the same catalog the
// save validates against and the launch resolves through, so the dialog cannot
// offer a model the save refuses. An uninitialized root config is no catalog.
func (a *App) ListOrchestratorLaunchChoices() (orchestratorLaunchChoices, error) {
	catalog, err := eruncommon.ResolveOpenRouterConfig(a.deps.store)
	if err != nil && !errors.Is(err, eruncommon.ErrNotInitialized) {
		return orchestratorLaunchChoices{}, fmt.Errorf("cannot read the gateway catalog from the ERun settings: %w", err)
	}
	return orchestratorLaunchChoicesFor(catalog), nil
}

// configuredERunAliases is the one filter for "which configured cloud aliases
// are erun platform aliases", shared by this picker and the tenant platform
// resolution, so the two cannot come to disagree about what the host has.
func (a *App) configuredERunAliases() ([]string, error) {
	providers, err := eruncommon.ListCloudProviders(a.deps.store)
	if err != nil {
		return nil, err
	}
	aliases := []string{}
	for _, provider := range providers {
		if provider.Provider == eruncommon.CloudProviderERun {
			aliases = append(aliases, provider.Alias)
		}
	}
	return aliases, nil
}

// ListOrchestratorEnvCandidates returns every environment the operator could
// consider linking, eligible or not — an env orchestratableEnv rejects is
// still listed, disabled, with IneligibleReason explaining why, rather than
// silently dropped: an operator who knows an env exists must be able to see
// that it was considered. An eligible env also carries the host directory the
// orchestrator reviews it in: a mirror the sync fills for a remote-agent env,
// or the env's own directory for a local-agent or host env, which is already on
// this machine — because the pod hostPath-mounts it in the local-agent case, and
// because there is no pod at all in the host one.
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
			requiredRole := eruncommon.OrchestratorEnvRoleRequiredFor(env.ResolvedType())
			candidate := orchestratorEnvCandidate{
				Tenant:          tenant.Name,
				Environment:     env.Name,
				EnvironmentType: env.ResolvedType(),
				// A candidate is eligible if it can be linked under whatever
				// role requiredRole names ("" when no single role is required,
				// which is every type but runtime; the runtime role for a
				// runtime env) — the role picker enforces the specific choice
				// once the operator selects the environment, including the
				// runtime role a host env is refused.
				Eligible: orchestratableEnv(env, requiredRole),
			}
			if candidate.Eligible {
				candidate.DefaultDirectory, candidate.Mirrored = orchestratorReviewDirectory(tenant.Name, env)
				candidate.RequiredRole = requiredRole
			} else {
				candidate.IneligibleReason = orchestratorIneligibilityReason(env, requiredRole)
			}
			out = append(out, candidate)
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
// and a link from outliving a repository path that moved. A runtime env has no
// review directory at all (orchestratorReviewDirectory answers explicitly), so
// its "" is expected rather than the missing-repository-path failure a
// local-agent/host env with an unset LocalRepoPath would hit.
func (a *App) resolveEnvInputs(inputs []orchestratorEnvInput) ([]eruncommon.OrchestratorEnvConfig, error) {
	refs := make([]eruncommon.OrchestratorEnvConfig, 0, len(inputs))
	for _, input := range inputs {
		tenant := strings.TrimSpace(input.Tenant)
		environment := strings.TrimSpace(input.Environment)
		if tenant == "" || environment == "" {
			continue
		}
		ref, err := a.resolveEnvInput(tenant, environment, input)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// resolveOrchestratorDirectories validates the orchestrator's own directories.
// Each must be an absolute path to a directory that exists on this machine: the
// orchestrator authors and builds there directly, so a path that is not there is
// a link that cannot work. Duplicates collapse, so adding the same path twice is
// one row rather than two identical ones.
func resolveOrchestratorDirectories(dirs []string) ([]eruncommon.OrchestratorDirectoryConfig, error) {
	out := make([]eruncommon.OrchestratorDirectoryConfig, 0, len(dirs))
	seen := make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		path := strings.TrimSpace(dir)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("orchestrator directory %q must be an absolute path", path)
		}
		clean := filepath.Clean(path)
		if seen[clean] {
			continue
		}
		info, statErr := os.Stat(clean)
		if statErr != nil || !info.IsDir() {
			return nil, fmt.Errorf("orchestrator directory %s is not a directory on this machine", clean)
		}
		seen[clean] = true
		out = append(out, eruncommon.OrchestratorDirectoryConfig{Directory: clean})
	}
	return out, nil
}

// resolveOrchestratorScope resolves both halves of what an orchestrator operates
// on -- the environments it links and the directories of its own -- and requires
// at least one of them. It deliberately does not require an environment: a
// directory the orchestrator works in itself is a complete definition, which is
// the whole point of having directories at all.
func (a *App) resolveOrchestratorScope(envs []orchestratorEnvInput, dirs []string) ([]eruncommon.OrchestratorEnvConfig, []eruncommon.OrchestratorDirectoryConfig, error) {
	refs, err := a.resolveEnvInputs(envs)
	if err != nil {
		return nil, nil, err
	}
	dirRefs, err := resolveOrchestratorDirectories(dirs)
	if err != nil {
		return nil, nil, err
	}
	if len(refs) == 0 && len(dirRefs) == 0 {
		return nil, nil, fmt.Errorf("an orchestrator must link at least one environment or name at least one directory")
	}
	return refs, dirRefs, nil
}

// resolveEnvInput resolves one candidate's role and review directory,
// split out of resolveEnvInputs to keep that loop within the lint
// complexity budget.
func (a *App) resolveEnvInput(tenant, environment string, input orchestratorEnvInput) (eruncommon.OrchestratorEnvConfig, error) {
	env, _, err := a.deps.store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return eruncommon.OrchestratorEnvConfig{}, fmt.Errorf("load %s/%s: %w", tenant, environment, err)
	}
	if !input.Role.IsValid() {
		return eruncommon.OrchestratorEnvConfig{}, fmt.Errorf("%s/%s: invalid role %q", tenant, environment, input.Role)
	}
	if !orchestratableEnv(env, input.Role) {
		return eruncommon.OrchestratorEnvConfig{}, fmt.Errorf("%s/%s: %s", tenant, environment, orchestratorIneligibilityReason(env, input.Role))
	}
	derived, mirrored := orchestratorReviewDirectory(tenant, env)
	dir := derived
	if mirrored {
		if supplied := strings.TrimSpace(input.Directory); supplied != "" {
			dir = supplied
		}
	} else if dir == "" && env.ResolvedType() != eruncommon.EnvironmentTypeRuntime {
		return eruncommon.OrchestratorEnvConfig{}, fmt.Errorf("%s/%s has no repository path on this machine; set one before linking it to an orchestrator", tenant, environment)
	}
	return eruncommon.OrchestratorEnvConfig{Tenant: tenant, Environment: environment, Directory: dir, Role: input.Role}, nil
}

// wireEnvironmentReview prepares the env's host review directory. A remote-agent
// env needs the mirror created and its one-way sync turned on so the window fills
// from the pod (SSHD must be deployed for it to flow; enabling it here is the
// configuration half, activated on the env's next open/deploy). A local-agent env
// needs neither: the pod hostPath-mounts the worktree, so the directory already
// exists and no sync is involved — creating it would hand the orchestrator an
// empty review window instead of surfacing a repository path that is not there.
// A runtime env needs neither either, for a different reason: it has no
// worktree and no mirror to prepare at all, by design (see
// orchestratorReviewDirectory), so there is nothing to wire.
func (a *App) wireEnvironmentReview(ref eruncommon.OrchestratorEnvConfig) error {
	env, _, err := a.deps.store.LoadEnvConfig(ref.Tenant, ref.Environment)
	if err != nil {
		return fmt.Errorf("load %s/%s: %w", ref.Tenant, ref.Environment, err)
	}
	if env.ResolvedType() == eruncommon.EnvironmentTypeRuntime {
		return nil
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

// linkOrchestratorEnvironments prepares every ref the same way, shared by
// CreateOrchestrator and UpdateOrchestrator: wire its review directory, then
// kick off its MCP port-forward so a linked environment with a healthy pod is
// reachable the moment an orchestrator session asks for its tools, instead of
// only working because an operator happened to run `erun open` for it earlier
// and nobody tells them that step is required. The forward ensure
// reuses ensureEnvRuntimeOnce, the same mechanism a terminal tab spawn already
// gets: fire-and-forget, deduped per (re)start window, and carrying its own
// not-force-start and surfaced-failure contract, so a stopped or undeployed
// environment is left alone and a genuine failure to open still reaches the
// operator rather than leaving a configured MCP entry pointed at nothing.
func (a *App) linkOrchestratorEnvironments(refs []eruncommon.OrchestratorEnvConfig) error {
	for _, ref := range refs {
		if err := a.wireEnvironmentReview(ref); err != nil {
			return err
		}
		// A host env has no pod and no runtime to ensure — its worktree is
		// already this machine's own directory, so there is nothing to forward
		// to and nothing to start. Ensuring anyway would fail on an env that was
		// never going to have a runtime, and surface that failure as a
		// runtime-unreachable warning about a working environment.
		if a.orchestratorRefNamesHostEnv(ref) {
			continue
		}
		a.ensureEnvRuntimeOnce(uiSelection{Tenant: ref.Tenant, Environment: ref.Environment})
	}
	return nil
}

// orchestratorRefNamesHostEnv reports whether a linked ref names a host
// environment. A ref whose env config no longer loads answers false, so the
// caller's ensure path still runs and surfaces that failure rather than this
// silently absorbing it.
func (a *App) orchestratorRefNamesHostEnv(ref eruncommon.OrchestratorEnvConfig) bool {
	env, _, err := a.deps.store.LoadEnvConfig(ref.Tenant, ref.Environment)
	if err != nil {
		return false
	}
	return env.ResolvedType() == eruncommon.EnvironmentTypeHost
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
func (a *App) CreateOrchestrator(name string, envs []orchestratorEnvInput, dirs []string, alias string, launch orchestratorLaunchInput) (orchestratorInfo, error) {
	refs, dirRefs, err := a.resolveOrchestratorScope(envs, dirs)
	if err != nil {
		return orchestratorInfo{}, err
	}
	alias, launch, _, err = a.validateOrchestratorSettings(alias, launch)
	if err != nil {
		return orchestratorInfo{}, err
	}
	if err := a.linkOrchestratorEnvironments(refs); err != nil {
		return orchestratorInfo{}, err
	}
	configs, err := a.loadOrchestratorConfigs()
	if err != nil {
		return orchestratorInfo{}, err
	}
	id := uniqueOrchestratorID(orchestratorDisplayName(name, refs), configs)
	displayName := orchestratorDisplayName(name, refs)
	def := withOrchestratorLaunchSettings(eruncommon.OrchestratorConfig{ID: id, Name: displayName, Alias: alias, Environments: refs, Directories: dirRefs}, launch)
	if err := a.saveOrchestratorConfigs(append(configs, def)); err != nil {
		return orchestratorInfo{}, err
	}
	info := orchestratorInfoFor(id, displayName, alias, refs, dirRefs, "stopped", 0, orchestratorBusySnapshot{}, false, orchestratorShellSnapshot{}, orchestratorPacingSnapshot{}, a.envActivitySnapshot(), a.envUsageSnapshot(), false, false)
	return orchestratorInfoWithLaunchSettings(info, launch, false), nil
}

// UpdateOrchestrator edits an existing orchestrator's linked environments, the
// directories it names for itself, its name, the platform alias it acts as, and
// its gateway, model and effort, re-wiring sync for the current set.
//
// Every field comes from the caller rather than being read back off the stored
// definition, because the save below replaces the whole entry: a field this
// literal omits is not left alone, it is erased. alias and the launch settings
// in particular are settable from the terminal (`erun orchestrator set-*`) as
// well as from this dialog, so the dialog must send back what it was shown even
// when the operator edited something else entirely.
func (a *App) UpdateOrchestrator(id, name string, envs []orchestratorEnvInput, dirs []string, alias string, launch orchestratorLaunchInput) (orchestratorInfo, error) {
	id = strings.TrimSpace(id)
	refs, dirRefs, err := a.resolveOrchestratorScope(envs, dirs)
	if err != nil {
		return orchestratorInfo{}, err
	}
	alias, launch, catalog, err := a.validateOrchestratorSettings(alias, launch)
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
	if err := a.linkOrchestratorEnvironments(refs); err != nil {
		return orchestratorInfo{}, err
	}
	displayName := orchestratorDisplayName(name, refs)
	configs[index] = withOrchestratorLaunchSettings(eruncommon.OrchestratorConfig{ID: id, Name: displayName, Alias: alias, Environments: refs, Directories: dirRefs}, launch)
	if err := a.saveOrchestratorConfigs(configs); err != nil {
		return orchestratorInfo{}, err
	}
	status, sessionID, busy, shell, pacing, restartRequired, roleChanged := a.updatedOrchestratorRunningSnapshot(id, refs)
	info := orchestratorInfoFor(id, displayName, alias, refs, dirRefs, status, sessionID, busy, false, shell, pacing, a.envActivitySnapshot(), a.envUsageSnapshot(), restartRequired, roleChanged)
	return orchestratorInfoWithLaunchSettings(info, launch, a.orchestratorLaunchSettingsChanged(configs[index], catalog)), nil
}

// updatedOrchestratorRunningSnapshot is UpdateOrchestrator's own read of live
// session state, split out to keep that function's complexity within budget.
// "stopped" (the zero snapshots, restartRequired false) when nothing is
// running for id.
func (a *App) updatedOrchestratorRunningSnapshot(id string, configuredEnvs []eruncommon.OrchestratorEnvConfig) (status string, sessionID int, busy orchestratorBusySnapshot, shell orchestratorShellSnapshot, pacing orchestratorPacingSnapshot, restartRequired, roleChanged bool) {
	info, ok := a.runningOrchestratorInfo(id)
	if !ok {
		// Not running is not "never nudged": read the persisted cumulative
		// history the same way ListOrchestrators' stopped branch does, so
		// saving an edit to a stopped orchestrator does not flash its hover
		// card back to "Not nudged".
		entry, _, unreadable := orchestratorNudgeHistoryFor(a.deps.orchestratorNudgeHistoryPath, id)
		return "stopped", 0, orchestratorBusySnapshot{}, orchestratorShellSnapshot{}, orchestratorPacingSnapshotFromHistory(entry, unreadable), false, false
	}
	busy = orchestratorBusySnapshot{Busy: info.Busy, AtUnix: info.BusyAtUnix}
	shell = orchestratorShellSnapshot{Running: info.ShellRunning, Command: info.ShellCommand, StartedAtUnix: info.ShellStartedAtUnix}
	pacing = orchestratorPacingSnapshot{
		NudgeCount:          info.NudgeCount,
		Capped:              info.NudgeCapped,
		LastNudgeAtUnix:     info.LastNudgeAtUnix,
		AutoNudgeCount:      info.AutoNudgeCount,
		LastAutoNudgeAtUnix: info.LastAutoNudgeAtUnix,
		WhipCount:           info.WhipCount,
		LastWhipAtUnix:      info.LastWhipAtUnix,
		LastCappedAtUnix:    info.LastCappedAtUnix,
		HistoryUnreadable:   info.NudgeHistoryUnreadable,
	}
	// The session already running still holds whatever it was spawned with;
	// saving a different scope here does not touch it (see
	// orchestratorScopeMismatch). Restart is the only thing that re-wires it,
	// so the operator is told rather than shown "running" as if the save took
	// effect immediately. A role-only edit doesn't affect wiring the same way
	// (see orchestratorRolesChanged), but is still reported so the dialog can
	// tell the operator their edit is not guaranteed live yet.
	if wired, wiredScope, ok := a.orchestratorWiredEnvs(id); ok {
		restartRequired = orchestratorScopeMismatch(wiredScope, configuredEnvs)
		roleChanged = orchestratorRolesChanged(wired, configuredEnvs)
	}
	return "running", info.SessionID, busy, shell, pacing, restartRequired, roleChanged
}

// StartOrchestrator opens a persisted orchestrator: it attaches to the
// orchestrator's Claude background agent, creating the agent when there is
// none, and reuses a PTY that is already attached.
func (a *App) StartOrchestrator(id string, cols, rows int) (orchestratorInfo, error) {
	id = strings.TrimSpace(id)
	if info, ok := a.runningOrchestratorInfo(id); ok {
		return a.withConfiguredLaunchSettings(info), nil
	}
	def, err := a.findOrchestratorConfig(id)
	if err != nil {
		return orchestratorInfo{}, err
	}
	launch, err := a.resolvePersistedOrchestratorLaunch(def)
	if err != nil {
		return orchestratorInfo{}, err
	}
	return a.spawnOrchestratorSession(orchestratorSpawn{
		id:     def.ID,
		name:   def.Name,
		alias:  def.Alias,
		launch: launch,
		envs:   a.refreshLinkedEnvDirectories(def.Environments),
		dirs:   def.Directories,
		cols:   cols,
		rows:   rows,
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

// RestartOrchestrator stops a persisted orchestrator's background agent and
// starts it again, so the operator can recycle a stuck agent, or apply changed
// settings, without losing context: the agent comes back on its own current
// conversation and re-reads the settings and MCP config written for it. The
// definition is resolved before teardown because stopOrchestratorSession
// forgets the live session, and a transient (Investigate) orchestrator has no
// persisted definition to respawn — findOrchestratorConfig then returns an
// error, so transients are intentionally not restartable. The launch is
// resolved before teardown too, so settings that cannot launch leave the
// running session in place rather than stopping it for nothing.
func (a *App) RestartOrchestrator(id string, cols, rows int) (orchestratorInfo, error) {
	id = strings.TrimSpace(id)
	def, err := a.findOrchestratorConfig(id)
	if err != nil {
		return orchestratorInfo{}, err
	}
	launch, err := a.resolvePersistedOrchestratorLaunch(def)
	if err != nil {
		return orchestratorInfo{}, err
	}
	a.stopOrchestratorSession(id)
	return a.spawnOrchestratorSession(orchestratorSpawn{
		id:      def.ID,
		name:    def.Name,
		alias:   def.Alias,
		launch:  launch,
		envs:    a.refreshLinkedEnvDirectories(def.Environments),
		dirs:    def.Directories,
		restart: true,
		cols:    cols,
		rows:    rows,
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
	shell := orchestratorShellSnapshot{Running: session.shellRunning, Command: session.shellCommand, StartedAtUnix: session.shellStartedAtUnix}
	pacing := orchestratorPacingSnapshotFromSession(session)
	return orchestratorInfoFor(session.id, session.name, session.alias, session.envs, session.dirs, "running", session.serial, orchestratorBusySnapshot{Busy: session.aiBusy, AtUnix: session.aiBusyAtUnix}, session.transient, shell, pacing, a.envActivity, a.envUsage, false, false), true
}

// orchestratorWiredEnvs returns the environments id's live session was opened
// with and the scope its agent's MCP config actually holds — what the agent
// last started with, not necessarily what the orchestrator is configured with
// right now — or ok=false when nothing is running for it.
func (a *App) orchestratorWiredEnvs(id string) ([]eruncommon.OrchestratorEnvConfig, []string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	session := a.orchestrators[id]
	managed := a.sessions[orchestratorSessionKey(id)]
	if session == nil || managed == nil || managed.closed {
		return nil, nil, false
	}
	return session.envs, session.wiredScope, true
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

// orchestratorSpawn is one session launch: which orchestrator, what it runs
// on, and -- for a transient session -- what it is seeded with.
type orchestratorSpawn struct {
	id            string
	name          string
	alias         string
	launch        orchestratorLaunchPlan
	envs          []eruncommon.OrchestratorEnvConfig
	dirs          []eruncommon.OrchestratorDirectoryConfig
	initialPrompt string
	transient     bool
	// restart stops a running background agent before attaching, so it starts
	// again on what this spawn writes for it.
	restart bool
	cols    int
	rows    int
}

// wireOrchestratorMCP writes the per-orchestrator MCP config and tells the
// operator about anything that did not wire, returning the config path ("" when
// there is none). Split out of spawnOrchestratorSession to keep that function
// inside the module's complexity budget.
//
// Both failure shapes are non-fatal: the orchestrator still launches. What it
// must not do is launch quietly. A session with linked environments and none of
// their tools looks working and is not, and a session missing just ONE
// environment's tools is worse -- it works, right up to the first call into the
// environment that is not there, which an agent reads as "not linked" rather
// than "failed to wire".
func (a *App) wireOrchestratorMCP(id, name string, envs []eruncommon.OrchestratorEnvConfig) string {
	// The config written below is read once, by a client that connects through it
	// as it launches, so an environment that is merely unopened is indistinguishable
	// to that client from one that is broken. Open the edges that are not answering
	// before the config naming them exists; anything still dead afterwards is
	// reported as unreachable by the probe inside the write.
	a.repairOrchestratorMCPEdges(envs)
	path, skipped, unreachable, err := a.writeOrchestratorMCPConfig(id, envs)
	hostEnvs, problems := splitOrchestratorMCPHostSkips(skipped)
	for _, skip := range skipped {
		log.Printf("erun-app: orchestrator %s: no MCP tools for %s: %s", id, skip.Label, skip.Reason)
	}
	if err != nil {
		log.Printf("erun-app: write orchestrator MCP config for %s: %v", id, err)
		a.emitOrchestratorNotification("warning", id, orchestratorMCPUnwiredNotice(name, err), orchestratorMCPUnwiredAction(err))
		return ""
	}
	// A host env is not a wiring problem -- it never had an MCP edge to lose --
	// so it stays out of problems and gets the informational line below rather
	// than a warning prescribing a restart that fixes nothing. It is still
	// counted in the partial notice's denominator, and named there, so the count
	// cannot describe a smaller orchestrator than the one that is linked.
	if len(problems) > 0 {
		a.emitAppNotification("warning", orchestratorMCPPartialNotice(name, len(envs)-len(skipped), len(envs), hostEnvs, problems))
	}
	if len(hostEnvs) > 0 {
		a.emitAppNotification("info", orchestratorMCPHostEnvNotice(name, hostEnvs))
	}
	// An unreachable edge is wired anyway: the proxy already recovers a
	// transient outage per call, so this is reported, never treated as a skip.
	for _, env := range unreachable {
		log.Printf("erun-app: orchestrator %s: wired %s but its edge is not answering", id, env.Label)
		// Record the entry so the sweep can log the exit. Without this the log's
		// last word on the wiring is the outage, forever.
		a.recordOrchestratorEdgeOutage(id, env.Label)
	}
	if len(unreachable) > 0 {
		a.reportUnreachableOrchestratorEdges(name, unreachable)
	}
	return path
}

// reportUnreachableOrchestratorEdges warns about every linked environment whose
// edge did not answer, one env-scoped notice each. Reported per environment
// rather than as a single combined notice because the remedy is per environment
// and the notice is what carries it: a combined notice names several envs, so
// there is no one env its deploy action could target, and dropping the action
// there left exactly the orchestrators with the most edges down — the ones that
// need it most — with prose and nothing to click. One notice per env gives each
// the same action, scoping, and later lifecycle clear the single-env case gets.
// An edge whose label is not a well-formed <tenant>/<environment> still gets its
// warning; it just has no env for an action to target.
func (a *App) reportUnreachableOrchestratorEdges(name string, unreachable []orchestratorMCPUnreachable) {
	for _, edge := range unreachable {
		notice := orchestratorMCPUnreachableNotice(name, []orchestratorMCPUnreachable{edge})
		tenant, environment, ok := orchestratorMCPUnreachableEnv(edge.Label)
		if !ok {
			a.emitAppNotification("warning", notice)
			continue
		}
		a.emitEnvNotification("warning", tenant, environment,
			notificationSourceOrchestratorEdgeUnreachable, notice, notificationActionDeploy)
	}
}

// spawnOrchestratorSession opens an orchestrator in a desktop PTY and tracks the
// live session. A persisted orchestrator attaches to its background agent; a
// transient (Investigate) one runs its session in the PTY itself.
func (a *App) spawnOrchestratorSession(spawn orchestratorSpawn) (orchestratorInfo, error) {
	id, name, envs := spawn.id, spawn.name, spawn.envs
	cols, rows := clampTerminalSize(spawn.cols, spawn.rows)
	// Wire each linked env's erun MCP into the orchestrator session so it drives
	// its envs through the MCP (raw/build/deploy/…) rather than raw kubectl.
	// Non-fatal: the orchestrator still launches without the env MCP, but an
	// agent with linked envs and none of their tools looks working and is not,
	// so the operator is told why instead of discovering it tool by tool.
	mcpConfigPath := a.wireOrchestratorMCP(id, name, envs)
	// Every orchestrator launches in the shared $HOME/orchestrators root, which
	// carries the one CLAUDE.md and the `<tenant>-<env>` mirror subdirectories.
	dir, err := a.ensureOrchestratorWorkspaceFor(id)
	if err != nil {
		return orchestratorInfo{}, err
	}
	sessionEnv := orchestratorSessionEnv(id, spawn.launch.env)
	launch := orchestratorPTYLaunch{dir: dir, env: sessionEnv, launch: spawn.launch.launch, mcpConfigPath: mcpConfigPath, cols: cols, rows: rows}
	wired := orchestratorAgentLaunch{scope: orchestratorScopeOf(envs), launched: resolvedLaunchOf(spawn.launch.launch)}
	if !spawn.transient {
		launch.agent, err = a.prepareOrchestratorAgent(spawn, launch)
		if err != nil {
			return orchestratorInfo{}, err
		}
		wired = launch.agent
	}
	session, err := a.startOrchestratorPTY(launch, spawn.initialPrompt)
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
	if !spawn.transient {
		// A transient (Investigate) session is not restartable at all
		// (RestartOrchestrator's comment above), and its bounded lifecycle is
		// investigation_bounds.go's to manage, not tryReconnect's — so it gets
		// no respawn closure and a crash simply ends it.
		managed.respawn = a.orchestratorRespawnFunc(spawn, launch)
	}
	newSession := &orchestratorSession{
		id:         id,
		serial:     serial,
		agentID:    launch.agent.agentID,
		transient:  spawn.transient,
		name:       name,
		alias:      spawn.alias,
		launched:   wired.launched,
		envs:       envs,
		wiredScope: wired.scope,
		dirs:       spawn.dirs,
		startedAt:  time.Now(),
	}
	a.restoreOrchestratorNudgeHistory(newSession)
	a.sessions[key] = managed
	a.orchestrators[id] = newSession
	pacing := orchestratorPacingSnapshotFromSession(newSession)
	a.mu.Unlock()

	a.spawnStreamSession(managed)
	// Record what is open now rather than on the way out: the desktop is just as
	// likely to be killed or to crash as to be quit cleanly, and only a record
	// written here survives that. A transient (Investigate) session has no
	// persisted definition to reopen, so it is deliberately not recorded.
	if !spawn.transient {
		if err := recordOpenOrchestrator(a.deps.orchestratorOpenPath, id); err != nil {
			log.Printf("erun-app: record open orchestrator %s: %v", id, err)
		}
	}
	info := orchestratorInfoFor(id, name, spawn.alias, envs, spawn.dirs, "running", serial, orchestratorBusySnapshot{}, spawn.transient, orchestratorShellSnapshot{}, pacing, a.envActivitySnapshot(), a.envUsageSnapshot(), false, false)
	info.RestartRequired = orchestratorScopeMismatch(wired.scope, envs)
	return orchestratorInfoWithLaunchSettings(info, spawn.launch.settings, wired.launched != resolvedLaunchOf(spawn.launch.launch)), nil
}

// orchestratorSessionEnv is the environment an orchestrator session runs with:
// the desktop's own markers plus the resolved launch's (the subagent model, and
// through a gateway its routing and credential).
func orchestratorSessionEnv(id string, launchEnv []string) []string {
	env := []string{
		appSessionEnvVar + "=1",
		// The orchestrator's own id, so an agent driving from its shell can
		// record itself as the return target for a rebuild+restart (see the
		// erun-orchestrate skill), and its hooks can name whose report they
		// write. Empty for transient/Investigate sessions.
		eruncommon.OrchestratorIDEnvVar + "=" + id,
	}
	env = append(env, launchEnv...)
	// An orchestrator has no pod, so the outputs convention an in-pod agent
	// follows needs a host directory to point at. Without it a host-side agent
	// has nowhere its deliverables are expected, and the operator has no way to
	// see them. A transient session has no id and so no directory of its own.
	if outputsDir, err := ensureOrchestratorOutputsDir(id); err == nil {
		env = append(env, eruncommon.RuntimeOutputsDirEnvVar+"="+outputsDir)
	} else if strings.TrimSpace(id) != "" {
		log.Printf("erun-app: orchestrator outputs dir for %s: %v", id, err)
	}
	return env
}

// orchestratorPTYLaunch is what the desktop's PTY for one orchestrator is
// started with, kept so a crash recovery reattaches the same way.
type orchestratorPTYLaunch struct {
	dir           string
	env           []string
	launch        eruncommon.OrchestratorLaunch
	mcpConfigPath string
	agent         orchestratorAgentLaunch
	cols          int
	rows          int
}

// prepareOrchestratorAgent writes what a persisted orchestrator's agent reads
// when it starts -- its settings file and an MCP config at its fixed path --
// and resolves the agent to attach to. The agent, not the PTY, receives the
// session environment: a background agent never inherits the environment of
// the command that started it.
func (a *App) prepareOrchestratorAgent(spawn orchestratorSpawn, launch orchestratorPTYLaunch) (orchestratorAgentLaunch, error) {
	mcpConfigPath := orchestratorMCPConfigPath(spawn.id)
	if launch.mcpConfigPath == "" {
		if err := writeEmptyOrchestratorMCPConfig(mcpConfigPath); err != nil {
			return orchestratorAgentLaunch{}, err
		}
	}
	settingsPath := orchestratorAgentSettingsPath(spawn.id)
	if err := writeOrchestratorAgentSettings(settingsPath, spawn.launch.launch, launch.env); err != nil {
		return orchestratorAgentLaunch{}, fmt.Errorf("write the settings for %s's background agent: %w", spawn.id, err)
	}
	_, effortFlags := eruncommon.ClaudeBackgroundEffort(spawn.launch.launch.Effort)
	return a.ensureOrchestratorAgent(orchestratorAgentRequest{
		spec: orchestratorAgentSpec{
			id:            spawn.id,
			dir:           launch.dir,
			settingsPath:  settingsPath,
			mcpConfigPath: mcpConfigPath,
			effortFlags:   effortFlags,
		},
		scope:    orchestratorScopeOf(spawn.envs),
		launched: resolvedLaunchOf(spawn.launch.launch),
		restart:  spawn.restart,
	})
}

// startOrchestratorPTY starts the desktop's terminal for one orchestrator:
// `claude attach` for a background agent, the session itself for a transient.
func (a *App) startOrchestratorPTY(launch orchestratorPTYLaunch, initialPrompt string) (terminalSession, error) {
	executable, args, err := a.deps.resolveOrchestratorLaunch(launch.launch, launch.agent.agentID, initialPrompt, launch.mcpConfigPath)
	if err != nil {
		return nil, err
	}
	return a.deps.startTerminal(startTerminalSessionParams{
		Dir:        resolveTerminalStartDir(launch.dir),
		Executable: executable,
		Args:       args,
		Env:        launch.env,
		Cols:       launch.cols,
		Rows:       launch.rows,
	})
}

// orchestratorRespawnFunc builds the closure tryReconnect calls when an
// orchestrator's terminal exits unexpectedly: it resolves the agent again and
// reattaches, which restarts an agent that stopped. The agent kept its own
// conversation throughout, so nothing has to be told to resume.
//
// tryReconnect only ever calls this after orchestratorReconnectRefused has
// already refused a clean exit and an operator Stop, so every call here is a
// real crash being recovered from.
func (a *App) orchestratorRespawnFunc(spawn orchestratorSpawn, launch orchestratorPTYLaunch) func() (terminalSession, error) {
	spawn.restart = false
	return func() (terminalSession, error) {
		agent, err := a.prepareOrchestratorAgent(spawn, launch)
		if err != nil {
			return nil, err
		}
		launch.agent = agent
		a.mu.Lock()
		if session := a.orchestrators[spawn.id]; session != nil {
			session.agentID = agent.agentID
		}
		a.mu.Unlock()
		return a.startOrchestratorPTY(launch, "")
	}
}

// unreachableOrchestratorIDs names every configured orchestrator this desktop
// cannot pace from here: one report read each, since the question is whether the
// orchestrator's OWN hooks are reporting a session, which no in-memory state can
// answer. See orchestratorPacingUnreachable.
func unreachableOrchestratorIDs(configs []eruncommon.OrchestratorConfig, now time.Time) map[string]bool {
	out := make(map[string]bool, len(configs))
	for _, config := range configs {
		id := strings.TrimSpace(config.ID)
		if id == "" || !orchestratorPacingUnreachable(id, now) {
			continue
		}
		out[id] = true
	}
	return out
}

// ListOrchestrators merges the persisted definitions (each tagged running or
// stopped) with any transient running sessions (Investigate), id-ordered.
func (a *App) ListOrchestrators() []orchestratorInfo {
	// The catalog comes from the same read: each running row resolves its
	// definition against it to tell whether a restart would change anything.
	configs, catalog, err := a.loadOrchestratorConfigsWithCatalog()
	if err != nil {
		configs, catalog = nil, nil
	}
	// Read once for the whole list rather than once per orchestrator: the
	// "stopped" branch below has no live session to read cumulative pacing
	// history from, so it falls back to this persisted record instead of the
	// zero snapshot a stopped orchestrator otherwise reported unconditionally.
	historyEntries, historyUnreadable := readOrchestratorNudgeHistoryEntries(a.deps.orchestratorNudgeHistoryPath)
	// Gathered before the lock for the same reason the history above is: one
	// small report read per configured orchestrator, and the result is only
	// consulted for the ones this desktop turns out to hold no session for.
	unreachable := unreachableOrchestratorIDs(configs, time.Now())
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]orchestratorInfo, 0, len(configs)+len(a.orchestrators))
	seen := make(map[string]struct{}, len(configs))
	for _, config := range configs {
		status := "stopped"
		sessionID := 0
		busy := orchestratorBusySnapshot{}
		shell := orchestratorShellSnapshot{}
		historyEntry, _ := orchestratorNudgeHistoryEntryIn(historyEntries, config.ID)
		pacing := orchestratorPacingSnapshotFromHistory(historyEntry, historyUnreadable)
		// Only the stopped branch can be unpaced-but-running: a running one is
		// a session this desktop holds, and the branch below rebuilds the
		// snapshot from that session, which drops this flag by construction.
		pacing.Unreachable = unreachable[config.ID]
		restartRequired := false
		roleChanged := false
		launchSettings := orchestratorLaunchSettingsOf(config)
		launchSettingsChanged := false
		if session := a.orchestrators[config.ID]; session != nil {
			if managed := a.sessions[orchestratorSessionKey(config.ID)]; managed != nil && !managed.closed {
				status = "running"
				sessionID = session.serial
				busy = orchestratorBusySnapshot{Busy: session.aiBusy, AtUnix: session.aiBusyAtUnix}
				shell = orchestratorShellSnapshot{Running: session.shellRunning, Command: session.shellCommand, StartedAtUnix: session.shellStartedAtUnix}
				pacing = orchestratorPacingSnapshotFromSession(session)
				// The persisted config is the operator's intent; the session
				// still runs on whatever it was spawned with. A poll that
				// shows the new set as if it were live is exactly the defect
				// this flag exists to stop (see orchestratorScopeMismatch).
				restartRequired = orchestratorScopeMismatch(session.wiredScope, config.Environments)
				roleChanged = orchestratorRolesChanged(session.envs, config.Environments)
				launchSettingsChanged = orchestratorLaunchDiffers(config, catalog, session.launched)
			}
		}
		info := orchestratorInfoFor(config.ID, config.Name, config.Alias, config.Environments, config.Directories, status, sessionID, busy, false, shell, pacing, a.envActivity, a.envUsage, restartRequired, roleChanged)
		out = append(out, orchestratorInfoWithLaunchSettings(info, launchSettings, launchSettingsChanged))
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
		shell := orchestratorShellSnapshot{Running: session.shellRunning, Command: session.shellCommand, StartedAtUnix: session.shellStartedAtUnix}
		pacing := orchestratorPacingSnapshotFromSession(session)
		out = append(out, orchestratorInfoFor(id, session.name, session.alias, session.envs, session.dirs, "running", session.serial, orchestratorBusySnapshot{Busy: session.aiBusy, AtUnix: session.aiBusyAtUnix}, true, shell, pacing, a.envActivity, a.envUsage, false, false))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// StopOrchestrator ends a running session while keeping a persisted definition:
// it detaches the desktop's terminal and stops the orchestrator's background
// agent, whose conversation is kept for the next start. A transient session is
// forgotten entirely.
func (a *App) StopOrchestrator(id string) error {
	id = strings.TrimSpace(id)
	stopped := a.stopOrchestratorSession(id)
	_, err := a.findOrchestratorConfig(id)
	if err != nil {
		if stopped {
			return nil
		}
		return fmt.Errorf("orchestrator %q not found", id)
	}
	if err := a.stopOrchestratorAgent(id); err != nil {
		return fmt.Errorf("stop the Claude background agent for %s: %w", id, err)
	}
	return nil
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
		_ = a.closeManaged(managed)
	}
	// Stopping is the operator saying this orchestrator should not come back, so
	// it stays closed on every later launch. A restart clears and re-records in
	// the same breath, which is the same statement about what is open.
	if err := clearOpenOrchestrator(a.deps.orchestratorOpenPath, id); err != nil {
		log.Printf("erun-app: clear open orchestrator %s: %v", id, err)
	}
	return session != nil || managed != nil
}

// DeleteOrchestrator stops the session if running, removes the orchestrator's
// background agent, and removes the persisted definition. The linked
// environments' sync config is left intact, and so is the agent's conversation
// transcript, which is Claude's.
//
// It also clears this id's persisted nudge history: the id is a
// name-derived slug (uniqueOrchestratorID), not a uuid, so creating a new
// orchestrator with the same name after this delete reuses it. Leaving the
// old record in place would hand that unrelated new orchestrator a nudge
// history that was never its own.
func (a *App) DeleteOrchestrator(id string) error {
	id = strings.TrimSpace(id)
	a.stopOrchestratorSession(id)
	if err := a.deleteOrchestratorAgent(id); err != nil {
		return fmt.Errorf("remove the Claude background agent for %s: %w", id, err)
	}
	if err := clearOrchestratorNudgeHistoryEntry(a.deps.orchestratorNudgeHistoryPath, id); err != nil {
		log.Printf("erun-app: clear orchestrator nudge history %s: %v", id, err)
	}
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
