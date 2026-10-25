package orchestrator

import (
	"fmt"
	"log"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/ai"
	orchcmd "github.com/sophium/erun/erun-common/command/orchestrator"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// NoAskFlag denies the question tool: a question stalls an unattended
// orchestrator. orchestratorNoAskStopGuardCommand catches the closing-sentence form.
const NoAskFlag = " --disallowedTools AskUserQuestion"

type orchestratorSpawn struct {
	id            string
	name          string
	alias         string
	launch        orchestratorLaunchPlan
	envs          []configpkg.OrchestratorEnvConfig
	dirs          []configpkg.OrchestratorDirectoryConfig
	initialPrompt string
	transient     bool
	// restart stops a running background agent so it restarts on what this spawn writes.
	restart bool
	cols    int
	rows    int
}

// wireOrchestratorMCP writes the MCP config and returns its path ("" when none).
// Failures are non-fatal but always reported: a missing env's tools read as "not linked".
func (s *Service) wireOrchestratorMCP(id, name string, envs []configpkg.OrchestratorEnvConfig) string {
	// The client reads the config once at launch, so open idle edges first.
	s.repairOrchestratorMCPEdges(envs)
	path, skipped, unreachable, err := s.writeOrchestratorMCPConfig(id, envs)
	hostEnvs, problems := splitOrchestratorMCPHostSkips(skipped)
	for _, skip := range skipped {
		log.Printf("erun-app: orchestrator %s: no MCP tools for %s: %s", id, skip.Label, skip.Reason)
	}
	if err != nil {
		log.Printf("erun-app: write orchestrator MCP config for %s: %v", id, err)
		s.emitOrchestratorNotification("warning", id, orchestratorMCPUnwiredNotice(name, err), orchestratorMCPUnwiredAction(err))
		return ""
	}
	// A host env has no edge: it is named in the denominator, never as a problem.
	if len(problems) > 0 {
		s.emitAppNotification("warning", orchestratorMCPPartialNotice(name, len(envs)-len(skipped), len(envs), hostEnvs, problems))
	}
	if len(hostEnvs) > 0 {
		s.emitAppNotification("info", orchestratorMCPHostEnvNotice(name, hostEnvs))
	}
	// The proxy recovers transient outages per call, so this is reported, not skipped.
	for _, env := range unreachable {
		log.Printf("erun-app: orchestrator %s: wired %s but its edge is not answering", id, env.Label)
		// Recorded so the sweep can log the recovery.
		s.recordOrchestratorEdgeOutage(id, env.Label)
	}
	if len(unreachable) > 0 {
		s.reportUnreachableOrchestratorEdges(name, unreachable)
	}
	return path
}

// reportUnreachableOrchestratorEdges posts one notice per env, since the deploy
// action is per environment; a malformed label still warns without an action.
func (s *Service) reportUnreachableOrchestratorEdges(name string, unreachable []orchestratorMCPUnreachable) {
	for _, edge := range unreachable {
		notice := orchestratorMCPUnreachableNotice(name, []orchestratorMCPUnreachable{edge})
		tenant, environment, ok := orchestratorMCPUnreachableEnv(edge.Label)
		if !ok {
			s.emitAppNotification("warning", notice)
			continue
		}
		s.emitEnvNotification("warning", tenant, environment,
			notificationSourceOrchestratorEdgeUnreachable, notice, uimodel.NotificationActionDeploy)
	}
}

func (s *Service) spawnOrchestratorSession(spawn orchestratorSpawn) (Info, error) {
	id, name, envs := spawn.id, spawn.name, spawn.envs
	mcpConfigPath := s.wireOrchestratorMCP(id, name, envs)
	dir, err := s.ensureOrchestratorWorkspaceFor(id)
	if err != nil {
		return Info{}, err
	}
	sessionEnv := orchestratorSessionEnv(id, spawn.launch.env)
	launch := orchestratorPTYLaunch{dir: dir, env: sessionEnv, launch: spawn.launch.launch, mcpConfigPath: mcpConfigPath, cols: spawn.cols, rows: spawn.rows}
	wired := orchestratorAgentLaunch{scope: orchestratorScopeOf(envs), launched: resolvedLaunchOf(spawn.launch.launch)}
	if !spawn.transient {
		launch.agent, err = s.prepareOrchestratorAgent(spawn, launch)
		if err != nil {
			return Info{}, err
		}
		wired = launch.agent
	}
	session, err := s.startOrchestratorPTY(launch, spawn.initialPrompt)
	if err != nil {
		return Info{}, err
	}

	var managed Terminal
	var pacing orchestratorPacingSnapshot
	s.withSessions(func(l Registry) {
		var respawn func() (PTY, error)
		if !spawn.transient {
			// Transient sessions are bounded elsewhere and get no respawn.
			respawn = s.orchestratorRespawnFunc(spawn, launch)
		}
		managed = l.Register(id, session, respawn)
		newSession := &orchestratorSession{
			id:         id,
			serial:     managed.Serial(),
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
		s.restoreOrchestratorNudgeHistory(newSession)
		s.orchestrators[id] = newSession
		pacing = orchestratorPacingSnapshotFromSession(newSession)
	})

	managed.Stream()
	// Record on open, not on exit: a crash or kill runs no exit path.
	if !spawn.transient {
		if err := recordOpenOrchestrator(s.d.OpenPath(), id); err != nil {
			log.Printf("erun-app: record open orchestrator %s: %v", id, err)
		}
	}
	info := orchestratorInfoFor(id, name, spawn.alias, envs, spawn.dirs, "running", managed.Serial(), orchestratorBusySnapshot{}, spawn.transient, orchestratorShellSnapshot{}, pacing, s.d.ActivitySnapshot(), s.d.UsageSnapshot(), false, false)
	info.RestartRequired = orchestratorScopeMismatch(wired.scope, envs)
	return orchestratorInfoWithLaunchSettings(info, spawn.launch.settings, wired.launched != resolvedLaunchOf(spawn.launch.launch)), nil
}

func orchestratorSessionEnv(id string, launchEnv []string) []string {
	env := []string{
		orchcmd.DesktopSessionEnvVar + "=1",
		// Hooks and the erun-orchestrate skill read this id; empty for transient sessions.
		jobstate.OrchestratorIDEnvVar + "=" + id,
	}
	env = append(env, launchEnv...)
	// An orchestrator has no pod, so $ERUN_OUTPUTS_DIR needs a host directory.
	if outputsDir, err := ensureOrchestratorOutputsDir(id); err == nil {
		env = append(env, core.RuntimeOutputsDirEnvVar+"="+outputsDir)
	} else if strings.TrimSpace(id) != "" {
		log.Printf("erun-app: orchestrator outputs dir for %s: %v", id, err)
	}
	return env
}

type orchestratorPTYLaunch struct {
	dir           string
	env           []string
	launch        orchcmd.OrchestratorLaunch
	mcpConfigPath string
	agent         orchestratorAgentLaunch
	cols          int
	rows          int
}

// prepareOrchestratorAgent writes the agent's settings and MCP config. The agent,
// not the PTY, gets the session env: a background agent never inherits it.
func (s *Service) prepareOrchestratorAgent(spawn orchestratorSpawn, launch orchestratorPTYLaunch) (orchestratorAgentLaunch, error) {
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
	_, effortFlags := ai.ClaudeBackgroundEffort(spawn.launch.launch.Effort)
	return s.ensureOrchestratorAgent(orchestratorAgentRequest{
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

func (s *Service) startOrchestratorPTY(launch orchestratorPTYLaunch, initialPrompt string) (PTY, error) {
	executable, args, err := s.d.ResolveLaunch(launch.launch, launch.agent.agentID, initialPrompt, launch.mcpConfigPath)
	if err != nil {
		return nil, err
	}
	return s.d.StartTerminal(StartParams{
		Dir:        launch.dir,
		Executable: executable,
		Args:       args,
		Env:        launch.env,
		Cols:       launch.cols,
		Rows:       launch.rows,
	})
}

// orchestratorRespawnFunc reattaches after a crash; the agent kept its conversation.
// tryReconnect calls it only after refusing clean exits and operator stops.
func (s *Service) orchestratorRespawnFunc(spawn orchestratorSpawn, launch orchestratorPTYLaunch) func() (PTY, error) {
	spawn.restart = false
	return func() (PTY, error) {
		agent, err := s.prepareOrchestratorAgent(spawn, launch)
		if err != nil {
			return nil, err
		}
		launch.agent = agent
		s.withState(func() {
			if session := s.orchestrators[spawn.id]; session != nil {
				session.agentID = agent.agentID
			}
		})
		return s.startOrchestratorPTY(launch, "")
	}
}
