package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
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
	envs     []configpkg.OrchestratorEnvConfig
	// wiredScope is the environment scope the agent's MCP config held when the
	// agent last started. An agent that was already running when this PTY
	// attached keeps the tools it started with, whatever envs says now.
	wiredScope []string
	// dirs are the orchestrator's own directories: paths it operates in that
	// belong to no environment. Held on the session so a running orchestrator
	// still reports them (the Edit dialog reads its scope from this snapshot),
	// and read from the persisted definition rather than re-derived, since
	// nothing about a directory changes behind the operator's back.
	dirs      []configpkg.OrchestratorDirectoryConfig
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
	Tenant      string                        `json:"tenant"`
	Environment string                        `json:"environment"`
	Directory   string                        `json:"directory"`
	Role        configpkg.OrchestratorEnvRole `json:"role"`
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
	EnvironmentType  configpkg.EnvironmentType     `json:"environmentType"`
	Eligible         bool                          `json:"eligible"`
	DefaultDirectory string                        `json:"defaultDirectory"`
	Mirrored         bool                          `json:"mirrored"`
	RequiredRole     configpkg.OrchestratorEnvRole `json:"requiredRole,omitempty"`
	IneligibleReason string                        `json:"ineligibleReason"`
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
	Role     configpkg.OrchestratorEnvRole  `json:"role"`
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
func orchestratableEnv(env configpkg.EnvConfig, role configpkg.OrchestratorEnvRole) bool {
	return configpkg.OrchestratorEnvRoleAllowed(env.ResolvedType(), role)
}

// orchestratorIneligibilityReason explains, in the operator's language, why
// role cannot be declared for env — a thin wrapper over
// eruncommon.OrchestratorEnvRoleIneligibilityReason, reusing orchestratableEnv's
// own reasoning rather than restating it. Returns "" when the link is allowed.
func orchestratorIneligibilityReason(env configpkg.EnvConfig, role configpkg.OrchestratorEnvRole) string {
	return configpkg.OrchestratorEnvRoleIneligibilityReason(env.ResolvedType(), role)
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

func orchestratorInfoFor(id, name, alias string, envs []configpkg.OrchestratorEnvConfig, dirs []configpkg.OrchestratorDirectoryConfig, status string, sessionID int, busy orchestratorBusySnapshot, transient bool, shell orchestratorShellSnapshot, pacing orchestratorPacingSnapshot, envActivity map[string]environmentActivityState, envUsage map[string]environmentUsageReading, restartRequired, roleChanged bool) orchestratorInfo {
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

func (a *App) loadOrchestratorConfigs() ([]configpkg.OrchestratorConfig, error) {
	configs, _, err := a.loadOrchestratorConfigsWithCatalog()
	return configs, err
}

// loadOrchestratorConfigsWithCatalog is loadOrchestratorConfigs plus the
// gateway catalog from the same read, for a poll that needs both.
func (a *App) loadOrchestratorConfigsWithCatalog() ([]configpkg.OrchestratorConfig, *configpkg.OpenRouterConfig, error) {
	config, _, err := a.deps.store.LoadERunConfig()
	if errors.Is(err, configpkg.ErrNotInitialized) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return config.Orchestrators, config.OpenRouter, nil
}

func (a *App) saveOrchestratorConfigs(orchestrators []configpkg.OrchestratorConfig) error {
	existing, _, err := a.deps.store.LoadERunConfig()
	if errors.Is(err, configpkg.ErrNotInitialized) {
		existing = configpkg.ERunConfig{}
	} else if err != nil {
		return err
	}
	existing.Orchestrators = orchestrators
	return a.deps.store.SaveERunConfig(existing)
}

func (a *App) findOrchestratorConfig(id string) (configpkg.OrchestratorConfig, error) {
	configs, err := a.loadOrchestratorConfigs()
	if err != nil {
		return configpkg.OrchestratorConfig{}, err
	}
	for _, config := range configs {
		if config.ID == id {
			return config, nil
		}
	}
	return configpkg.OrchestratorConfig{}, fmt.Errorf("orchestrator %q not found", id)
}

func orchestratorDisplayName(name string, envs []configpkg.OrchestratorEnvConfig) string {
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		return trimmed
	}
	if tenants := tenantsFromEnvs(envs); len(tenants) > 0 {
		return strings.Join(tenants, ", ")
	}
	return "orchestrator"
}
