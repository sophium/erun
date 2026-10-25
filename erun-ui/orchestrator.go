package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/runtimeview"
)

// An orchestrator is a host-side AI session that drives its linked environments.
// It never writes into a pod-backed environment's review directory: the in-pod
// agent owns that worktree. A host environment has no pod, so it works there directly.

// orchestratorSession is a live orchestrator PTY; transient (Investigate) sessions
// carry their own display metadata since they have no config definition.
type orchestratorSession struct {
	id     string
	serial int
	// Empty for a transient session, which runs in the PTY itself.
	agentID   string
	transient bool
	name      string
	// Held so the Edit dialog's snapshot keeps the alias instead of erasing it on save.
	alias string
	// launched is only compared against the current resolution to flag a restart;
	// rows report configured values.
	launched orchestratorResolvedLaunch
	envs     []configpkg.OrchestratorEnvConfig
	// wiredScope is the env scope the agent's MCP config held at start; a running
	// agent keeps those tools whatever envs says now.
	wiredScope []string
	// dirs belong to no environment; held so the Edit dialog's snapshot reports them.
	dirs      []configpkg.OrchestratorDirectoryConfig
	startedAt time.Time
	// aiBusy is read by every snapshot so a fresh mount renders the true state
	// without having seen the ai-activity event.
	aiBusy       bool
	aiBusyAtUnix int64
	// Independent of aiBusy: a shell can outlive the turn that started it.
	shellRunning          bool
	shellCommand          string
	shellStartedAtUnix    int64
	pacingNudgeCount      int
	pacingCapped          bool
	pacingLastNudgeAtUnix int64
	// pacingLastReason lets the reconciler log transitions instead of every tick.
	pacingLastReason orchestratorPacingReason
	// Cumulative nudge history: never reset by pacing decisions, and seeded from
	// the persisted nudge history on every spawn.
	pacingAutoNudgeCount      int
	pacingLastAutoNudgeAtUnix int64
	pacingWhipCount           int
	pacingLastWhipAtUnix      int64
	// Survives the rearm that clears pacingCapped.
	pacingLastCappedAtUnix int64
	// pacingHistoryUnreadable means the cumulative fields may be a false zero.
	pacingHistoryUnreadable bool
}

// orchestratorEnvInput is the frontend's env selection; empty Role means undeclared.
type orchestratorEnvInput struct {
	Tenant      string                    `json:"tenant"`
	Environment string                    `json:"environment"`
	Directory   string                    `json:"directory"`
	Role        model.OrchestratorEnvRole `json:"role"`
}

// orchestratorLaunchInput: empty Model or Effort means the default.
type orchestratorLaunchInput struct {
	UseGateway bool   `json:"useGateway"`
	Model      string `json:"model"`
	Effort     string `json:"effort"`
}

// orchestratorLaunchChoices is what the dialog's gateway, model and effort controls offer.
type orchestratorLaunchChoices struct {
	GatewayConfigured        bool     `json:"gatewayConfigured"`
	GatewayModels            []string `json:"gatewayModels"`
	GatewayUndriveableModels []string `json:"gatewayUndriveableModels"`
	GatewayDefaultModel      string   `json:"gatewayDefaultModel"`
	DefaultModel             string   `json:"defaultModel"`
	EffortLevels             []string `json:"effortLevels"`
	DefaultEffort            string   `json:"defaultEffort"`
}

// orchestratorEnvCandidate is an env the operator considered linking. A runtime
// env has no directory and RequiredRole names the only role it allows.
type orchestratorEnvCandidate struct {
	Tenant           string                    `json:"tenant"`
	Environment      string                    `json:"environment"`
	EnvironmentType  configpkg.EnvironmentType `json:"environmentType"`
	Eligible         bool                      `json:"eligible"`
	DefaultDirectory string                    `json:"defaultDirectory"`
	Mirrored         bool                      `json:"mirrored"`
	RequiredRole     model.OrchestratorEnvRole `json:"requiredRole,omitempty"`
	IneligibleReason string                    `json:"ineligibleReason"`
}

// orchestratorEnvInfo is the JSON view of one linked environment; nil Activity
// means not yet observed.
type orchestratorEnvInfo struct {
	Tenant      string                        `json:"tenant"`
	Environment string                        `json:"environment"`
	Directory   string                        `json:"directory"`
	Role        model.OrchestratorEnvRole     `json:"role"`
	Activity    *runtimeview.ActivitySnapshot `json:"activity,omitempty"`
	Usage       *runtimeview.UsageSnapshot    `json:"usage,omitempty"`
}

// orchestratorInfo is the JSON view the frontend renders. Nudge* is the live cap
// budget; AutoNudge*/Whip*/LastCappedAtUnix are cumulative and persisted.
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
	// PacingUnreachable: another desktop runs this orchestrator, so it cannot be paced here.
	PacingUnreachable bool `json:"pacingUnreachable,omitempty"`
	// RestartRequired: the live session's env scope differs from the persisted one;
	// --mcp-config is only resolved at launch.
	RestartRequired bool `json:"restartRequired"`
	// RoleChanged asks for a restart without claiming tools are missing.
	RoleChanged bool   `json:"roleChanged"`
	Alias       string `json:"alias,omitempty"`
	// Configured values, never the launched ones, so a save does not overwrite
	// a later `erun orchestrator set-*`.
	UseGateway            bool   `json:"useGateway,omitempty"`
	Model                 string `json:"model,omitempty"`
	Effort                string `json:"effort,omitempty"`
	LaunchSettingsChanged bool   `json:"launchSettingsChanged"`
}

// orchestratableEnv reports whether role may be linked against env.
func orchestratableEnv(env configpkg.EnvConfig, role model.OrchestratorEnvRole) bool {
	return configpkg.OrchestratorEnvRoleAllowed(env.ResolvedType(), role)
}

// orchestratorIneligibilityReason returns "" when the link is allowed.
func orchestratorIneligibilityReason(env configpkg.EnvConfig, role model.OrchestratorEnvRole) string {
	return configpkg.OrchestratorEnvRoleIneligibilityReason(env.ResolvedType(), role)
}

type orchestratorShellSnapshot struct {
	Running       bool
	Command       string
	StartedAtUnix int64
}

type orchestratorBusySnapshot struct {
	Busy   bool
	AtUnix int64
}

type orchestratorPacingSnapshot struct {
	NudgeCount          int
	Capped              bool
	LastNudgeAtUnix     int64
	AutoNudgeCount      int
	LastAutoNudgeAtUnix int64
	WhipCount           int
	LastWhipAtUnix      int64
	LastCappedAtUnix    int64
	HistoryUnreadable   bool
	// Unreachable is set by the caller that knows which orchestrators have no session here.
	Unreachable bool
}

// orchestratorPacingSnapshotFromSession is the single session-to-snapshot mapping.
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

// orchestratorPacingSnapshotFromHistory leaves the live-cap fields zero: nothing is running.
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

func orchestratorInfoFor(id, name, alias string, envs []configpkg.OrchestratorEnvConfig, dirs []configpkg.OrchestratorDirectoryConfig, status string, sessionID int, busy orchestratorBusySnapshot, transient bool, shell orchestratorShellSnapshot, pacing orchestratorPacingSnapshot, envActivity map[string]runtimeview.ActivityState, envUsage map[string]runtimeview.UsageReading, restartRequired, roleChanged bool) orchestratorInfo {
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
