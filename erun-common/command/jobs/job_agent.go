package jobs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/sophium/erun/erun-common/primitive/shared"
)

// Agent runs are their own job kind because AI tools print nothing until exit;
// streaming mode lets erun fold events into progress. Vendor specifics stay in
// this file's parsers.

const (
	EnvironmentJobKindCommand = "command"
	// EnvironmentJobKindAgent is an AI tool run in streaming mode; progress is
	// normalized from its event stream.
	EnvironmentJobKindAgent = "agent"
	// EnvironmentJobKindTask is Go work run in this process: its result is
	// captured directly and liveness is this process's pid.
	EnvironmentJobKindTask = "task"

	agentToolClaude = "claude"
	agentToolCodex  = "codex"

	agentTextLimit = 240
	// agentPendingLimit bounds the carried partial line for streams without newlines.
	agentPendingLimit = 1 << 20
)

// AgentJobTools are the AI tools a job can run as an agent.
var AgentJobTools = []string{agentToolClaude, agentToolCodex}

// AgentJobCommand builds the argv for an agent run in the tool's streaming mode.
func AgentJobCommand(tool, prompt string) ([]string, error) {
	name := strings.ToLower(strings.TrimSpace(tool))
	text := strings.TrimSpace(prompt)
	if text == "" {
		return nil, fmt.Errorf("an agent job needs a prompt to run")
	}
	switch name {
	case agentToolClaude:
		// --verbose makes stream-json emit per event. ScheduleWakeup is denied:
		// a one-shot run has nothing to wake into and would end its turn early.
		return []string{agentToolClaude, "-p", text, "--output-format", "stream-json", "--verbose", "--disallowedTools", "ScheduleWakeup"}, nil
	case agentToolCodex:
		return []string{agentToolCodex, "exec", "--json", text}, nil
	default:
		return nil, fmt.Errorf("unsupported agent tool %q: expected one of %s", tool, strings.Join(AgentJobTools, ", "))
	}
}

// AgentJobCommandPrompt is AgentJobCommand's exact inverse and returns "" for
// any other argv shape; the resumed turn restates the task so its final
// message still answers it.
func AgentJobCommandPrompt(tool string, command []string) string {
	name := strings.ToLower(strings.TrimSpace(tool))
	switch name {
	case agentToolClaude:
		if len(command) >= 3 && command[1] == "-p" {
			return strings.TrimSpace(command[2])
		}
	case agentToolCodex:
		if len(command) >= 4 && command[1] == "exec" && command[2] == "--json" {
			return strings.TrimSpace(command[3])
		}
	}
	return ""
}

// AgentJobResumeCommand builds a follow-up turn resuming the reported session;
// codex's context survival across resume is unverified.
func AgentJobResumeCommand(tool, sessionID, prompt string) ([]string, error) {
	name := strings.ToLower(strings.TrimSpace(tool))
	id := strings.TrimSpace(sessionID)
	text := strings.TrimSpace(prompt)
	if id == "" {
		return nil, fmt.Errorf("a resumed agent turn needs the session id the earlier turn reported")
	}
	if text == "" {
		return nil, fmt.Errorf("a resumed agent turn needs a prompt to run")
	}
	switch name {
	case agentToolClaude:
		return []string{agentToolClaude, "-p", text, "--resume", id, "--output-format", "stream-json", "--verbose", "--disallowedTools", "ScheduleWakeup"}, nil
	case agentToolCodex:
		return []string{agentToolCodex, "exec", "resume", "--json", id, text}, nil
	default:
		return nil, fmt.Errorf("unsupported agent tool %q: expected one of %s", tool, strings.Join(AgentJobTools, ", "))
	}
}

// NormalizeAgentJobTool resolves the tool name so caller casing never reaches the record.
func NormalizeAgentJobTool(tool string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(tool))
	for _, candidate := range AgentJobTools {
		if candidate == name {
			return name, nil
		}
	}
	return "", fmt.Errorf("unsupported agent tool %q: expected one of %s", tool, strings.Join(AgentJobTools, ", "))
}

// agentProgressReader folds the event stream as bytes arrive, fed from the
// process writes so the log's byte cap cannot silence progress. Two pipe
// goroutines write concurrently, so every method locks.
type agentProgressReader struct {
	tool string

	mu       sync.Mutex
	pending  []byte
	progress shared.AgentJobProgress
}

func newAgentProgressReader(tool string) *agentProgressReader {
	return &agentProgressReader{tool: tool, progress: shared.AgentJobProgress{Tool: tool}}
}

func (r *agentProgressReader) snapshot() shared.AgentJobProgress {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.progress
}

func (r *agentProgressReader) feed(chunk []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.consume(chunk)
}

// consume carries a partial trailing line between feeds. Caller holds mu.
func (r *agentProgressReader) consume(chunk []byte) {
	buffer := make([]byte, 0, len(r.pending)+len(chunk))
	buffer = append(buffer, r.pending...)
	buffer = append(buffer, chunk...)
	for {
		index := bytes.IndexByte(buffer, '\n')
		if index < 0 {
			break
		}
		r.apply(buffer[:index])
		buffer = buffer[index+1:]
	}
	if len(buffer) > agentPendingLimit {
		buffer = nil
	}
	r.pending = append([]byte(nil), buffer...)
}

func (r *agentProgressReader) apply(line []byte) {
	trimmed := strings.TrimSpace(string(line))
	// Plain diagnostic lines interleave with events and are not parse failures.
	if !strings.HasPrefix(trimmed, "{") {
		return
	}
	var applied bool
	if r.tool == agentToolCodex {
		applied = applyCodexAgentEvent(&r.progress, []byte(trimmed))
	} else {
		applied = applyClaudeAgentEvent(&r.progress, []byte(trimmed))
	}
	if applied {
		r.progress.Events++
	}
}

type claudeStreamEvent struct {
	Type     string               `json:"type"`
	Result   string               `json:"result"`
	NumTurns int                  `json:"num_turns"`
	IsError  bool                 `json:"is_error"`
	Message  *claudeStreamMessage `json:"message"`
	// SessionID is on every event, unlike Result which only the last carries.
	SessionID string `json:"session_id"`
}

type claudeStreamMessage struct {
	ID string `json:"id"`
	// Content is an array of blocks on assistant events and a string on others.
	Content json.RawMessage `json:"content"`
}

type claudeContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

func applyClaudeAgentEvent(progress *shared.AgentJobProgress, raw []byte) bool {
	var event claudeStreamEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return false
	}
	if id := strings.TrimSpace(event.SessionID); id != "" {
		progress.SessionID = id
	}
	switch event.Type {
	case "assistant":
		applyClaudeAssistantEvent(progress, event)
	case "result":
		if event.NumTurns > 0 {
			progress.Turns = event.NumTurns
		}
		if result := strings.TrimSpace(event.Result); result != "" {
			if event.IsError {
				progress.Error = truncateAgentText(result)
			} else {
				progress.Result = truncateAgentText(result)
			}
		}
		progress.Activity = ""
	case "":
		return false
	}
	return true
}

func applyClaudeAssistantEvent(progress *shared.AgentJobProgress, event claudeStreamEvent) {
	if event.Message == nil {
		return
	}
	// One message spans several events; the id separates turns.
	if id := strings.TrimSpace(event.Message.ID); id != "" && id != progress.LastMessageID {
		progress.LastMessageID = id
		progress.Turns++
	}
	var blocks []claudeContentBlock
	if json.Unmarshal(event.Message.Content, &blocks) != nil {
		return
	}
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if text := strings.TrimSpace(block.Text); text != "" {
				progress.LastMessage = truncateAgentText(text)
			}
		case "tool_use":
			progress.ToolsRun++
			progress.LastTool = block.Name
			progress.LastTarget = agentToolTarget(block.Input)
			progress.Activity = agentActivityLabel(block.Name, progress.LastTarget)
		}
	}
}

// agentToolInput is the union of the input keys tools name a target with.
type agentToolInput struct {
	FilePath     string `json:"file_path"`
	Path         string `json:"path"`
	NotebookPath string `json:"notebook_path"`
	Pattern      string `json:"pattern"`
	Command      string `json:"command"`
	URL          string `json:"url"`
	Query        string `json:"query"`
	Skill        string `json:"skill"`
	Description  string `json:"description"`
	Prompt       string `json:"prompt"`
}

func agentToolTarget(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var input agentToolInput
	if json.Unmarshal(raw, &input) != nil {
		return ""
	}
	for _, candidate := range []string{
		input.FilePath, input.Path, input.NotebookPath, input.Pattern,
		input.Command, input.URL, input.Query, input.Skill,
		input.Description, input.Prompt,
	} {
		if value := strings.TrimSpace(candidate); value != "" {
			return truncateAgentText(collapseAgentWhitespace(value))
		}
	}
	return ""
}

type codexStreamEvent struct {
	Type    string           `json:"type"`
	Message string           `json:"message"`
	Item    *codexStreamItem `json:"item"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
	// ThreadID arrives on codex's first event, its twin of claude's session_id.
	ThreadID string `json:"thread_id"`
}

type codexStreamItem struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Command string `json:"command"`
	Query   string `json:"query"`
	Server  string `json:"server"`
	Tool    string `json:"tool"`
	Changes []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	} `json:"changes"`
}

func applyCodexAgentEvent(progress *shared.AgentJobProgress, raw []byte) bool {
	var event codexStreamEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return false
	}
	switch event.Type {
	case "thread.started":
		applyCodexThreadStartedEvent(progress, event)
	case "turn.started":
		progress.Turns++
	case "item.started", "item.updated", "item.completed":
		applyCodexItem(progress, event.Item, event.Type == "item.completed")
	case "turn.completed":
		progress.Activity = ""
	case "turn.failed":
		progress.Activity = ""
		if event.Error != nil {
			progress.Error = truncateAgentText(event.Error.Message)
		}
	case "error":
		progress.Error = truncateAgentText(event.Message)
	case "":
		return false
	}
	return true
}

func applyCodexThreadStartedEvent(progress *shared.AgentJobProgress, event codexStreamEvent) {
	if id := strings.TrimSpace(event.ThreadID); id != "" {
		progress.SessionID = id
	}
}

func applyCodexItem(progress *shared.AgentJobProgress, item *codexStreamItem, completed bool) {
	if item == nil {
		return
	}
	if item.Type == "agent_message" {
		if text := strings.TrimSpace(item.Text); text != "" {
			progress.LastMessage = truncateAgentText(collapseAgentWhitespace(text))
			progress.Result = progress.LastMessage
		}
		return
	}
	tool, target := codexItemTarget(item)
	if tool == "" {
		return
	}
	// Only completed items count; started/updated are the same item in flight.
	if completed {
		progress.ToolsRun++
	}
	progress.LastTool = tool
	progress.LastTarget = target
	progress.Activity = agentActivityLabel(tool, target)
}

func codexItemTarget(item *codexStreamItem) (string, string) {
	switch item.Type {
	case "command_execution":
		return "Bash", truncateAgentText(collapseAgentWhitespace(item.Command))
	case "file_change":
		return "Edit", truncateAgentText(codexChangedPaths(item))
	case "web_search":
		return "WebSearch", truncateAgentText(collapseAgentWhitespace(item.Query))
	case "mcp_tool_call":
		return "Mcp", truncateAgentText(codexMcpToolName(item))
	case "reasoning":
		return "Think", ""
	default:
		return "", ""
	}
}

func codexChangedPaths(item *codexStreamItem) string {
	paths := make([]string, 0, len(item.Changes))
	for _, change := range item.Changes {
		if path := strings.TrimSpace(change.Path); path != "" {
			paths = append(paths, path)
		}
	}
	return strings.Join(paths, ", ")
}

func codexMcpToolName(item *codexStreamItem) string {
	name := strings.TrimSpace(item.Tool)
	server := strings.TrimSpace(item.Server)
	switch {
	case server != "" && name != "":
		return server + "." + name
	case name != "":
		return name
	default:
		return server
	}
}

func agentActivityLabel(tool, target string) string {
	verb := agentToolVerb(tool)
	if strings.TrimSpace(target) == "" {
		return verb
	}
	return verb + " " + target
}

// agentToolVerbs is closed on purpose so every AI tool renders the same phrases.
var agentToolVerbs = map[string]string{
	"Edit":         "editing",
	"MultiEdit":    "editing",
	"Write":        "editing",
	"NotebookEdit": "editing",
	"Read":         "reading",
	"NotebookRead": "reading",
	"Bash":         "running",
	"BashOutput":   "running",
	"Grep":         "searching",
	"Glob":         "searching",
	"WebFetch":     "fetching",
	"WebSearch":    "fetching",
	"Task":         "delegating to",
	"Agent":        "delegating to",
	"Workflow":     "delegating to",
	"Think":        "thinking",
	"Mcp":          "calling",
	"":             "working",
}

func agentToolVerb(tool string) string {
	if verb, ok := agentToolVerbs[tool]; ok {
		return verb
	}
	return "using " + tool + " on"
}

func collapseAgentWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func truncateAgentText(value string) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= agentTextLimit {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:agentTextLimit])) + "…"
}
