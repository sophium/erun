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
	"github.com/sophium/erun/erun-ui/internal/terminal"
)

// A persisted orchestrator runs as a Claude background agent named after its id;
// the agent owns the conversation. It is looked up by exact name, then addressed by
// id, because `claude attach <name>` matches partially and cannot restart a stopped
// agent. Launch arguments are replayed verbatim, so changing them means recreating it.

// claudeBackgroundAgent is one `claude agents --json` entry; PID is absent when stopped.
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

// claudeAgentCLI is injectable so no test starts a real agent.
type claudeAgentCLI interface {
	List() ([]claudeBackgroundAgent, error)
	Create(dir string, args []string) (string, error)
	Stop(id string) error
	Remove(id string) error
}

// Creating an agent may start Claude's background service first.
const claudeAgentCommandTimeout = 90 * time.Second

type execClaudeAgentCLI struct{}

var newDefaultClaudeAgents = func() claudeAgentCLI { return execClaudeAgentCLI{} }

func (execClaudeAgentCLI) run(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), claudeAgentCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, terminal.DefaultAITool, args...)
	cmd.Dir = dir
	core.HideConsoleWindow(cmd)
	return cmd.CombinedOutput()
}

func (cli execClaudeAgentCLI) List() ([]claudeBackgroundAgent, error) {
	ctx, cancel := context.WithTimeout(context.Background(), claudeAgentCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, terminal.DefaultAITool, "agents", "--json", "--all")
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

// parseClaudeBackgroundAgents drops interactive sessions, which are never an orchestrator's agent.
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

// errOrchestratorWorkspaceUntrusted: only an interactive session can record folder trust.
var errOrchestratorWorkspaceUntrusted = errors.New("workspace not trusted")

// parseClaudeAgentCreated reads the new agent's id; Claude may print service
// startup lines before it.
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

func findOrchestratorAgents(agents []claudeBackgroundAgent, id string) []claudeBackgroundAgent {
	var out []claudeBackgroundAgent
	for _, agent := range agents {
		if agent.Name == id {
			out = append(out, agent)
		}
	}
	return out
}

type orchestratorAgentSpec struct {
	id            string
	dir           string
	settingsPath  string
	mcpConfigPath string
	effortFlags   []string
}

// resume names the conversation a recreated agent continues; "" for a new agent.
func (spec orchestratorAgentSpec) args(resume string) []string {
	args := []string{"--bg", "-n", spec.id, "--settings", spec.settingsPath, "--mcp-config", spec.mcpConfigPath}
	args = append(args, spec.effortFlags...)
	args = append(args, strings.Fields(orchestratorNoAskFlag)...)
	if resume != "" {
		args = append(args, "--resume", resume)
	}
	return args
}

// fingerprint covers what Claude replays on restart and only recreation changes.
func (spec orchestratorAgentSpec) fingerprint() string {
	return filepath.Clean(spec.dir) + "\x00" + strings.Join(spec.args(""), "\x00")
}

// orchestratorAgentRecord lets erun recognise an agent of the same name made by
// someone else.
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

func removeOrchestratorAgentFiles(id string) {
	for _, path := range []string{orchestratorAgentRecordPath(id), orchestratorAgentSettingsPath(id)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "erun: remove %s: %v\n", path, err)
		}
	}
}

// writeOrchestratorAgentSettings writes a private file: it may carry the gateway credential.
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

// writeEmptyOrchestratorMCPConfig keeps the launch arguments stable and drops tools
// from a previous wiring.
func writeEmptyOrchestratorMCPConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("{\n  \"mcpServers\": {}\n}\n"), 0o600)
}

type orchestratorAgentLaunch struct {
	agentID  string
	scope    []string
	launched orchestratorResolvedLaunch
}

type orchestratorAgentRequest struct {
	spec     orchestratorAgentSpec
	scope    []string
	launched orchestratorResolvedLaunch
	restart  bool
}

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

func (a *App) recreateOrchestratorAgent(req orchestratorAgentRequest, agent claudeBackgroundAgent, erunMadeIt bool) (orchestratorAgentLaunch, error) {
	a.emitOrchestratorNotification("warning", req.spec.id, orchestratorAgentRecreatedNotice(req.spec.id, agent, erunMadeIt), "")
	if err := a.removeOrchestratorAgent(agent); err != nil {
		return orchestratorAgentLaunch{}, err
	}
	return a.createOrchestratorAgent(req, agent.SessionID)
}

// attachableOrchestratorAgent: a running agent keeps the scope and launch it last
// started on; a stopped one starts on the settings just written.
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

// The transcript survives deletion, which lets a recreated agent resume it.
func (a *App) removeOrchestratorAgent(agent claudeBackgroundAgent) error {
	if agent.running() {
		if err := a.deps.claudeAgents.Stop(agent.ID); err != nil {
			return err
		}
	}
	return a.deps.claudeAgents.Remove(agent.ID)
}

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

func orchestratorAgentRecreatedNotice(id string, agent claudeBackgroundAgent, erunMadeIt bool) string {
	reason := "it was not started by erun"
	if erunMadeIt {
		reason = "its launch arguments changed (for example its effort moved to or from max), and a background agent only reads those when it is created"
	}
	return fmt.Sprintf("Recreating the Claude background agent for %s (%s): %s. "+
		"It continues the same conversation.", id, agent.ID, reason)
}

// Runs through the host shell: an npm claude.cmd shim only resolves there.
func orchestratorAttachCommand(goos, agentID string) (string, []string) {
	shell, shellArgs := terminal.LocalShellCommand(goos)
	flag := "-lc"
	if goos == "windows" {
		flag = "-Command"
	}
	return shell, append(shellArgs, flag, terminal.DefaultAITool+" attach "+agentID)
}
