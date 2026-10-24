package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/ai"
	"github.com/sophium/erun/erun-common/command/orchestrator"
)

// A persisted orchestrator runs as a Claude Code background agent named after
// its orchestrator id. The agent owns the conversation: `/clear`, a compaction,
// a desktop quit or a crash of the desktop's terminal all leave it on its own
// current conversation, and opening the orchestrator attaches to it. Nothing on
// the desktop's side names a conversation, so nothing here can go stale.
//
// The agent is addressed by its name only through an exact match over
// `claude agents --json --all`: `claude attach <name>` matches part of a name
// and cannot restart a stopped agent, so every command after the lookup uses
// the agent's own id.
//
// Everything a restart can deliver is written to a per-orchestrator settings
// file the agent re-reads whenever it starts (model, effort, environment). The
// launch arguments are replayed verbatim by Claude's daemon, so a change to
// them -- or an agent erun did not create -- needs the agent recreated, which
// keeps its conversation by resuming the session it was on.

// claudeBackgroundAgent is one background session as `claude agents --json`
// lists it. PID is absent for a stopped session.
type claudeBackgroundAgent struct {
	ID        string `json:"id"`
	PID       int    `json:"pid"`
	Cwd       string `json:"cwd"`
	Kind      string `json:"kind"`
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
}

func (agent claudeBackgroundAgent) running() bool {
	return agent.PID > 0
}

// claudeAgentCLI is the part of the claude CLI the desktop drives an agent's
// lifecycle through. Injectable so no test ever starts a real agent.
type claudeAgentCLI interface {
	List() ([]claudeBackgroundAgent, error)
	Create(dir string, args []string) (string, error)
	Stop(id string) error
	Remove(id string) error
}

// claudeAgentCommandTimeout bounds one lifecycle command. Creating an agent may
// start Claude's background service first, which is the slow case.
const claudeAgentCommandTimeout = 90 * time.Second

type execClaudeAgentCLI struct{}

// newDefaultClaudeAgents is the CLI an App drives when none is injected. The
// package's tests replace it with an in-memory fake, so a test that does not
// care about agents still never starts a real one.
var newDefaultClaudeAgents = func() claudeAgentCLI { return execClaudeAgentCLI{} }

func (execClaudeAgentCLI) run(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), claudeAgentCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, defaultAITool, args...)
	cmd.Dir = dir
	core.HideConsoleWindow(cmd)
	return cmd.CombinedOutput()
}

func (cli execClaudeAgentCLI) List() ([]claudeBackgroundAgent, error) {
	ctx, cancel := context.WithTimeout(context.Background(), claudeAgentCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, defaultAITool, "agents", "--json", "--all")
	core.HideConsoleWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("cannot list Claude background agents: %w", err)
	}
	return parseClaudeBackgroundAgents(out)
}

func (cli execClaudeAgentCLI) Create(dir string, args []string) (string, error) {
	out, err := cli.run(dir, args...)
	return parseClaudeAgentCreated(string(out), err, dir)
}

func (cli execClaudeAgentCLI) Stop(id string) error {
	if out, err := cli.run("", "stop", id); err != nil {
		return fmt.Errorf("claude stop %s: %w: %s", id, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (cli execClaudeAgentCLI) Remove(id string) error {
	if out, err := cli.run("", "rm", id); err != nil {
		return fmt.Errorf("claude rm %s: %w: %s", id, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// parseClaudeBackgroundAgents keeps the background entries of a `claude agents
// --json` listing; interactive sessions are listed beside them and are never an
// orchestrator's agent.
func parseClaudeBackgroundAgents(data []byte) ([]claudeBackgroundAgent, error) {
	var all []claudeBackgroundAgent
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("cannot read the Claude background agent list: %w", err)
	}
	out := make([]claudeBackgroundAgent, 0, len(all))
	for _, agent := range all {
		if agent.Kind == "background" && strings.TrimSpace(agent.ID) != "" {
			out = append(out, agent)
		}
	}
	return out, nil
}

var claudeAgentCreatedPattern = regexp.MustCompile(`backgrounded\s*·\s*([0-9a-f]{6,})`)

// errOrchestratorWorkspaceUntrusted is Claude refusing to start a background
// agent in a folder nobody has trusted yet. Only an interactive session can
// record that trust, so the remedy is the operator's.
var errOrchestratorWorkspaceUntrusted = errors.New("workspace not trusted")

// parseClaudeAgentCreated reads the new agent's id out of `claude --bg`. The
// line naming it is not always the first: Claude may start its background
// service before it.
func parseClaudeAgentCreated(output string, runErr error, dir string) (string, error) {
	if strings.Contains(output, "Workspace not trusted") {
		return "", fmt.Errorf("%w: Claude Code will not start a background agent in %s until that folder is trusted. "+
			"Run `claude` once in %s, accept the trust prompt, exit it, then open the orchestrator again",
			errOrchestratorWorkspaceUntrusted, dir, dir)
	}
	if match := claudeAgentCreatedPattern.FindStringSubmatch(output); match != nil {
		return match[1], nil
	}
	detail := strings.TrimSpace(output)
	if runErr != nil {
		return "", fmt.Errorf("cannot start the Claude background agent: %w: %s", runErr, detail)
	}
	return "", fmt.Errorf("cannot start the Claude background agent: claude --bg did not name the agent it started: %s", detail)
}

// findOrchestratorAgents returns every background agent named exactly id.
func findOrchestratorAgents(agents []claudeBackgroundAgent, id string) []claudeBackgroundAgent {
	var out []claudeBackgroundAgent
	for _, agent := range agents {
		if agent.Name == id {
			out = append(out, agent)
		}
	}
	return out
}

// orchestratorAgentSpec is everything an orchestrator's agent is launched with.
type orchestratorAgentSpec struct {
	id            string
	dir           string
	settingsPath  string
	mcpConfigPath string
	// effortFlags carries the one effort level the settings file cannot hold.
	effortFlags []string
}

// args is the `claude --bg` invocation. resume names the conversation a
// recreated agent continues; a new agent passes "".
func (spec orchestratorAgentSpec) args(resume string) []string {
	args := []string{"--bg", "-n", spec.id, "--settings", spec.settingsPath, "--mcp-config", spec.mcpConfigPath}
	args = append(args, spec.effortFlags...)
	args = append(args, strings.Fields(orchestratorNoAskFlag)...)
	if resume != "" {
		args = append(args, "--resume", resume)
	}
	return args
}

// fingerprint identifies the launch arguments and folder an agent was created
// with: exactly what Claude replays on every restart and what only recreating
// the agent changes.
func (spec orchestratorAgentSpec) fingerprint() string {
	return filepath.Clean(spec.dir) + "\x00" + strings.Join(spec.args(""), "\x00")
}

// orchestratorAgentRecord is what erun knows about the agent it created for an
// orchestrator. It names which agent erun made, so an agent of the same name
// made by someone else is recognised, and the launch that agent last started
// on, which a running agent keeps until it restarts.
type orchestratorAgentRecord struct {
	AgentID     string                     `json:"agentId"`
	Fingerprint string                     `json:"fingerprint"`
	Scope       []string                   `json:"scope,omitempty"`
	Launched    orchestratorResolvedLaunch `json:"launched"`
}

func orchestratorAgentRecordPath(id string) string {
	return filepath.Join(orchestratorStateDir(), "orchestrator-agents", sanitizeOrchestratorFileID(id)+".json")
}

func orchestratorAgentSettingsPath(id string) string {
	return filepath.Join(orchestratorStateDir(), "orchestrator-settings-"+sanitizeOrchestratorFileID(id)+".json")
}

func orchestratorStateDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	return filepath.Join(configDir, "ERun")
}

func readOrchestratorAgentRecord(id string) (orchestratorAgentRecord, bool) {
	data, err := os.ReadFile(orchestratorAgentRecordPath(id))
	if err != nil {
		return orchestratorAgentRecord{}, false
	}
	var record orchestratorAgentRecord
	if json.Unmarshal(data, &record) != nil || strings.TrimSpace(record.AgentID) == "" {
		return orchestratorAgentRecord{}, false
	}
	return record, true
}

func writeOrchestratorAgentRecord(id string, record orchestratorAgentRecord) error {
	path := orchestratorAgentRecordPath(id)
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// removeOrchestratorAgentFiles forgets everything erun wrote for an
// orchestrator's agent.
func removeOrchestratorAgentFiles(id string) {
	for _, path := range []string{orchestratorAgentRecordPath(id), orchestratorAgentSettingsPath(id)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "erun: remove %s: %v\n", path, err)
		}
	}
}

// writeOrchestratorAgentSettings writes the settings file an agent reads every
// time it starts. It carries the gateway credential when the launch routes
// through one, so it is private to the operator.
func writeOrchestratorAgentSettings(path string, launch orchestrator.OrchestratorLaunch, env []string) error {
	settings, _ := ai.ClaudeBackgroundEffort(launch.Effort)
	settings["model"] = launch.Model
	values := map[string]string{}
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			values[key] = value
		}
	}
	settings["env"] = values
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// writeEmptyOrchestratorMCPConfig gives an orchestrator with nothing wired an
// empty MCP config at its usual path, so the agent's launch arguments do not
// change with its environment count, and an agent that starts again does not
// pick up tools a previous wiring left behind.
func writeEmptyOrchestratorMCPConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("{\n  \"mcpServers\": {}\n}\n"), 0o600)
}

// orchestratorAgentLaunch is a resolved agent ready to attach: which agent, and
// the scope and launch it is running on.
type orchestratorAgentLaunch struct {
	agentID  string
	scope    []string
	launched orchestratorResolvedLaunch
}

// orchestratorAgentRequest is one open of an orchestrator's agent.
type orchestratorAgentRequest struct {
	spec     orchestratorAgentSpec
	scope    []string
	launched orchestratorResolvedLaunch
	// restart stops a running agent first, so it starts again on the settings
	// just written.
	restart bool
}

// ensureOrchestratorAgent finds or creates the agent for one orchestrator, so
// the caller can attach to it. The settings file has already been written.
func (a *App) ensureOrchestratorAgent(req orchestratorAgentRequest) (orchestratorAgentLaunch, error) {
	id := req.spec.id
	agents, err := a.deps.claudeAgents.List()
	if err != nil {
		return orchestratorAgentLaunch{}, err
	}
	matches := findOrchestratorAgents(agents, id)
	switch len(matches) {
	case 0:
		return a.createOrchestratorAgent(req, orchestratorLegacyConversation(a.deps.orchestratorOpenPath, id))
	case 1:
	default:
		return orchestratorAgentLaunch{}, orchestratorDuplicateAgentsError(id, matches)
	}
	agent := matches[0]
	record, recorded := readOrchestratorAgentRecord(id)
	if !recorded || record.AgentID != agent.ID || record.Fingerprint != req.spec.fingerprint() {
		return a.recreateOrchestratorAgent(req, agent, recorded && record.AgentID == agent.ID)
	}
	return a.attachableOrchestratorAgent(req, agent, record)
}

// recreateOrchestratorAgent replaces an agent whose launch arguments erun cannot
// change in place, continuing the conversation it was on, and tells the
// operator why.
func (a *App) recreateOrchestratorAgent(req orchestratorAgentRequest, agent claudeBackgroundAgent, erunMadeIt bool) (orchestratorAgentLaunch, error) {
	a.emitOrchestratorNotification("warning", req.spec.id, orchestratorAgentRecreatedNotice(req.spec.id, agent, erunMadeIt), "")
	if err := a.removeOrchestratorAgent(agent); err != nil {
		return orchestratorAgentLaunch{}, err
	}
	return a.createOrchestratorAgent(req, agent.SessionID)
}

// attachableOrchestratorAgent readies an agent erun created with the current
// launch arguments. A running agent is attached as it is and keeps the scope
// and launch it last started on; a stopped one (or one a restart just stopped)
// starts again on attach, on the settings just written.
func (a *App) attachableOrchestratorAgent(req orchestratorAgentRequest, agent claudeBackgroundAgent, record orchestratorAgentRecord) (orchestratorAgentLaunch, error) {
	if agent.running() && req.restart {
		if err := a.deps.claudeAgents.Stop(agent.ID); err != nil {
			return orchestratorAgentLaunch{}, err
		}
		agent.PID = 0
	}
	if agent.running() {
		return orchestratorAgentLaunch{agentID: agent.ID, scope: record.Scope, launched: record.Launched}, nil
	}
	record.Scope, record.Launched = req.scope, req.launched
	if err := writeOrchestratorAgentRecord(req.spec.id, record); err != nil {
		return orchestratorAgentLaunch{}, err
	}
	return orchestratorAgentLaunch{agentID: agent.ID, scope: req.scope, launched: req.launched}, nil
}

func (a *App) createOrchestratorAgent(req orchestratorAgentRequest, resume string) (orchestratorAgentLaunch, error) {
	agentID, err := a.deps.claudeAgents.Create(req.spec.dir, req.spec.args(resume))
	if err != nil {
		return orchestratorAgentLaunch{}, err
	}
	record := orchestratorAgentRecord{AgentID: agentID, Fingerprint: req.spec.fingerprint(), Scope: req.scope, Launched: req.launched}
	if err := writeOrchestratorAgentRecord(req.spec.id, record); err != nil {
		return orchestratorAgentLaunch{}, err
	}
	return orchestratorAgentLaunch{agentID: agentID, scope: req.scope, launched: req.launched}, nil
}

// removeOrchestratorAgent stops and deletes one agent. The conversation
// transcript is Claude's and survives, which is what lets a recreated agent
// resume it.
func (a *App) removeOrchestratorAgent(agent claudeBackgroundAgent) error {
	if agent.running() {
		if err := a.deps.claudeAgents.Stop(agent.ID); err != nil {
			return err
		}
	}
	return a.deps.claudeAgents.Remove(agent.ID)
}

// stopOrchestratorAgent stops the agent named exactly id, if one is running.
func (a *App) stopOrchestratorAgent(id string) error {
	agents, err := a.deps.claudeAgents.List()
	if err != nil {
		return err
	}
	for _, agent := range findOrchestratorAgents(agents, id) {
		if agent.running() {
			if err := a.deps.claudeAgents.Stop(agent.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// deleteOrchestratorAgent removes every agent named exactly id and the files
// erun kept for it.
func (a *App) deleteOrchestratorAgent(id string) error {
	agents, err := a.deps.claudeAgents.List()
	if err != nil {
		return err
	}
	for _, agent := range findOrchestratorAgents(agents, id) {
		if err := a.removeOrchestratorAgent(agent); err != nil {
			return err
		}
	}
	removeOrchestratorAgentFiles(id)
	return nil
}

func orchestratorDuplicateAgentsError(id string, matches []claudeBackgroundAgent) error {
	ids := make([]string, 0, len(matches))
	for _, agent := range matches {
		ids = append(ids, agent.ID)
	}
	return fmt.Errorf("%d Claude background agents are named %q (%s), so erun cannot tell which one is this orchestrator. "+
		"Remove the ones that are not with `claude rm <id>`, then open it again",
		len(matches), id, strings.Join(ids, ", "))
}

// orchestratorAgentRecreatedNotice says why an agent is being replaced. The
// conversation is kept either way, which is the part the operator needs to
// know is safe.
func orchestratorAgentRecreatedNotice(id string, agent claudeBackgroundAgent, erunMadeIt bool) string {
	reason := "it was not started by erun"
	if erunMadeIt {
		reason = "its launch arguments changed (for example its effort moved to or from max), and a background agent only reads those when it is created"
	}
	return fmt.Sprintf("Recreating the Claude background agent for %s (%s): %s. "+
		"It continues the same conversation.", id, agent.ID, reason)
}

// orchestratorAttachCommand is the terminal command that opens an agent in the
// desktop's PTY. It runs through the host shell for the same reason a launch
// does: an npm claude.cmd shim only resolves there.
func orchestratorAttachCommand(goos, agentID string) (string, []string) {
	shell, shellArgs := resolveLocalShellCommand(goos)
	flag := "-lc"
	if goos == "windows" {
		flag = "-Command"
	}
	return shell, append(shellArgs, flag, defaultAITool+" attach "+agentID)
}
