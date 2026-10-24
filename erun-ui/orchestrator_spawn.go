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
	"github.com/sophium/erun/erun-common/primitive/shared"
)

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

// claudeAgentIDPattern is the shape of a background agent id. It is spliced
// into a shell line, so nothing else is accepted.
var claudeAgentIDPattern = regexp.MustCompile(`^[0-9a-f]{6,64}$`)

// buildOrchestratorLaunch composes a transient session's host-shell invocation
// at launch's effort on launch's model. Both are spliced unquoted, which is safe
// only because eruncommon.ResolveOrchestratorLaunch admits nothing but a plain
// model token and a known effort level.
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

// orchestratorSpawn is one session launch: which orchestrator, what it runs
// on, and -- for a transient session -- what it is seeded with.
type orchestratorSpawn struct {
	id            string
	name          string
	alias         string
	launch        orchestratorLaunchPlan
	envs          []configpkg.OrchestratorEnvConfig
	dirs          []configpkg.OrchestratorDirectoryConfig
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
func (a *App) wireOrchestratorMCP(id, name string, envs []configpkg.OrchestratorEnvConfig) string {
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
		shared.OrchestratorIDEnvVar + "=" + id,
	}
	env = append(env, launchEnv...)
	// An orchestrator has no pod, so the outputs convention an in-pod agent
	// follows needs a host directory to point at. Without it a host-side agent
	// has nowhere its deliverables are expected, and the operator has no way to
	// see them. A transient session has no id and so no directory of its own.
	if outputsDir, err := ensureOrchestratorOutputsDir(id); err == nil {
		env = append(env, core.RuntimeOutputsDirEnvVar+"="+outputsDir)
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
	launch        orchestrator.OrchestratorLaunch
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
