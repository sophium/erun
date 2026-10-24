package main

import (
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/ai"
	"github.com/sophium/erun/erun-common/command/orchestrator"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
)

// orchestratorNoAskFlag denies the question tool: a question stalls an unattended
// orchestrator. orchestratorNoAskStopGuardCommand catches the closing-sentence form.
const orchestratorNoAskFlag = " --disallowedTools AskUserQuestion"

// orchestratorLaunchCommand runs through the host shell so npm .cmd/.ps1 shims
// resolve under ConPTY. A persisted orchestrator attaches to its background
// agent; a transient one runs a fresh session seeded with initialPrompt.
func orchestratorLaunchCommand(launch orchestrator.OrchestratorLaunch, agentID, initialPrompt, mcpConfigPath string) (string, []string, error) {
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

// claudeAgentIDPattern restricts the id because it is spliced into a shell line.
var claudeAgentIDPattern = regexp.MustCompile(`^[0-9a-f]{6,64}$`)

// buildOrchestratorLaunch splices model and effort unquoted, which is safe only
// because ResolveOrchestratorLaunch admits plain tokens.
func buildOrchestratorLaunch(goos string, launch orchestrator.OrchestratorLaunch, initialPrompt, mcpConfigPath string) (string, []string) {
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

// orchestratorPromptArg prefixes `--` so a multi-value flag cannot swallow the
// prompt, and shell-quotes it so `$`, backticks and backslashes survive verbatim.
func orchestratorPromptArg(goos, prompt string) string {
	prompt = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(prompt)
	if goos == "windows" {
		return "-- " + powerShellQuote(prompt)
	}
	return "-- " + shellQuote(prompt)
}

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
func (a *App) wireOrchestratorMCP(id, name string, envs []configpkg.OrchestratorEnvConfig) string {
	// The client reads the config once at launch, so open idle edges first.
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
	// A host env has no edge: it is named in the denominator, never as a problem.
	if len(problems) > 0 {
		a.emitAppNotification("warning", orchestratorMCPPartialNotice(name, len(envs)-len(skipped), len(envs), hostEnvs, problems))
	}
	if len(hostEnvs) > 0 {
		a.emitAppNotification("info", orchestratorMCPHostEnvNotice(name, hostEnvs))
	}
	// The proxy recovers transient outages per call, so this is reported, not skipped.
	for _, env := range unreachable {
		log.Printf("erun-app: orchestrator %s: wired %s but its edge is not answering", id, env.Label)
		// Recorded so the sweep can log the recovery.
		a.recordOrchestratorEdgeOutage(id, env.Label)
	}
	if len(unreachable) > 0 {
		a.reportUnreachableOrchestratorEdges(name, unreachable)
	}
	return path
}

// reportUnreachableOrchestratorEdges posts one notice per env, since the deploy
// action is per environment; a malformed label still warns without an action.
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

func (a *App) spawnOrchestratorSession(spawn orchestratorSpawn) (orchestratorInfo, error) {
	id, name, envs := spawn.id, spawn.name, spawn.envs
	cols, rows := clampTerminalSize(spawn.cols, spawn.rows)
	mcpConfigPath := a.wireOrchestratorMCP(id, name, envs)
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
		// Transient sessions are bounded elsewhere and get no respawn.
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
	// Record on open, not on exit: a crash or kill runs no exit path.
	if !spawn.transient {
		if err := recordOpenOrchestrator(a.deps.orchestratorOpenPath, id); err != nil {
			log.Printf("erun-app: record open orchestrator %s: %v", id, err)
		}
	}
	info := orchestratorInfoFor(id, name, spawn.alias, envs, spawn.dirs, "running", serial, orchestratorBusySnapshot{}, spawn.transient, orchestratorShellSnapshot{}, pacing, a.envActivitySnapshot(), a.envUsageSnapshot(), false, false)
	info.RestartRequired = orchestratorScopeMismatch(wired.scope, envs)
	return orchestratorInfoWithLaunchSettings(info, spawn.launch.settings, wired.launched != resolvedLaunchOf(spawn.launch.launch)), nil
}

func orchestratorSessionEnv(id string, launchEnv []string) []string {
	env := []string{
		appSessionEnvVar + "=1",
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
	launch        orchestrator.OrchestratorLaunch
	mcpConfigPath string
	agent         orchestratorAgentLaunch
	cols          int
	rows          int
}

// prepareOrchestratorAgent writes the agent's settings and MCP config. The agent,
// not the PTY, gets the session env: a background agent never inherits it.
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
	_, effortFlags := ai.ClaudeBackgroundEffort(spawn.launch.launch.Effort)
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

// orchestratorRespawnFunc reattaches after a crash; the agent kept its conversation.
// tryReconnect calls it only after refusing clean exits and operator stops.
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
