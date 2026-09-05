package eruncommon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

// An agent run is a job kind rather than an opaque command because the AI tools
// say nothing until they exit: a multi-hour run sits at zero captured bytes
// while it is actively editing files, so "read the log" cannot answer what it is
// doing. Naming the kind lets erun invoke the tool in the streaming mode it
// already has and fold the events into one progress view, which is what keeps
// the answer the same shape across tools and across a vendor reshaping its
// stream. Everything vendor-specific stops at the parsers in this file.

const (
	// EnvironmentJobKindCommand is a plain argv job; its log is whatever the
	// work printed.
	EnvironmentJobKindCommand = "command"
	// EnvironmentJobKindAgent is an AI tool run in streaming mode; its log is the
	// tool's event stream and its progress is normalized from it.
	EnvironmentJobKindAgent = "agent"
	// EnvironmentJobKindTask is Go work run asynchronously in this process
	// rather than a subprocess a supervisor waits on. Its typed return value is
	// captured directly onto Result once it finishes, and liveness is decided
	// by this process's own pid rather than a supervisor's. Its log is whatever
	// the work wrote to the writer it was handed — there is no subprocess whose
	// stdio could be captured instead, and a failed task with no log at all was
	// undiagnosable after the fact.
	EnvironmentJobKindTask = "task"

	agentToolClaude = "claude"
	agentToolCodex  = "codex"

	// agentTextLimit bounds every free-text field folded into progress, so the
	// job record stays small no matter how much the agent says.
	agentTextLimit = 240
	// agentPendingLimit bounds the partial trailing line the reader carries
	// between polls, so a stream that never emits a newline cannot grow it.
	agentPendingLimit = 1 << 20
)

// AgentJobTools are the AI tools a job can run as an agent, in the order the
// error message lists them.
var AgentJobTools = []string{agentToolClaude, agentToolCodex}

// AgentJobCommand builds the argv for an agent run. Each tool is invoked in its
// streaming mode — the only mode that emits before the run ends — so the job's
// existing incremental output read works for an agent job with no change to its
// contract.
func AgentJobCommand(tool, prompt string) ([]string, error) {
	name := strings.ToLower(strings.TrimSpace(tool))
	text := strings.TrimSpace(prompt)
	if text == "" {
		return nil, fmt.Errorf("an agent job needs a prompt to run")
	}
	switch name {
	case agentToolClaude:
		// --verbose is what makes stream-json emit per event instead of one
		// closing envelope, which is the whole point of running it this way.
		//
		// --disallowedTools ScheduleWakeup: this run is a one-shot supervised
		// process with no later invocation to wake into, unlike the orchestrator's
		// own persistent session (ai_launch.go), which keeps the tool. Offering it
		// here let the agent "successfully" schedule a wakeup that will never
		// fire and end its turn, while the supervisor still recorded a clean exit
		// -- the run looked done and the work it was waiting on was still in
		// flight (erun#1731). Denying the tool removes it from what the model is
		// even offered, verified against a real `claude -p` run, rather than
		// merely rejecting the call after the model has already committed to it.
		return []string{agentToolClaude, "-p", text, "--output-format", "stream-json", "--verbose", "--disallowedTools", "ScheduleWakeup"}, nil
	case agentToolCodex:
		return []string{agentToolCodex, "exec", "--json", text}, nil
	default:
		return nil, fmt.Errorf("unsupported agent tool %q: expected one of %s", tool, strings.Join(AgentJobTools, ", "))
	}
}

// AgentJobResumeCommand builds the argv for a bounded follow-up turn of a run
// AgentJobCommand already started, resumed via the session/thread id that turn's
// own stream reported (AgentJobProgress.SessionID) rather than restated from
// scratch. Verified live against the installed claude (2.1.222): a fact told to
// one `claude -p --session-id <id>` process was correctly recalled by a wholly
// separate `claude -p --resume <id>` process afterwards, so this is genuine
// conversational continuation, not a self-contained restatement of task facts.
// codex's non-interactive twin (`codex exec resume <id> <prompt>`) is built the
// same way but its own context-survival was not verified the same way: the
// sandbox this was designed in had no authenticated codex, only an unauthenticated
// CLI whose requests 401 before any content reaches the model. See
// erun-common/AGENTS.md's note on this for the full evidence trail.
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

// NormalizeAgentJobTool resolves the tool name a job records, so a caller's
// casing never reaches the record or the argv.
func NormalizeAgentJobTool(tool string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(tool))
	for _, candidate := range AgentJobTools {
		if candidate == name {
			return name, nil
		}
	}
	return "", fmt.Errorf("unsupported agent tool %q: expected one of %s", tool, strings.Join(AgentJobTools, ", "))
}

// AgentJobProgress is erun's normalized view of an agent run. A caller reads the
// same fields whether the run is claude or codex, and never the vendor's own
// event shape — that is what makes it a contract rather than a passthrough.
type AgentJobProgress struct {
	Tool string `json:"tool,omitempty"`
	// SessionID is the tool's own identifier for this conversation -- claude's
	// session_id (present on every stream-json event from the first) or codex's
	// thread_id (its thread.started event) -- captured so a bounded follow-up
	// turn can resume the real conversation instead of restating the task from
	// scratch. Empty until the tool's first event names it, and never guessed.
	SessionID string `json:"sessionId,omitempty"`
	// Activity is the one-line answer to "what is it doing right now", such as
	// "editing erun-common/mcp_client.go". It clears when the run reports a
	// result, because at that point the answer is the outcome.
	Activity   string `json:"activity,omitempty"`
	LastTool   string `json:"lastTool,omitempty"`
	LastTarget string `json:"lastTarget,omitempty"`
	Turns      int    `json:"turns"`
	ToolsRun   int    `json:"toolsRun"`
	// Events counts the stream events folded in so far, which is what tells a
	// caller the stream is alive even when nothing else moved.
	Events int `json:"events"`
	// LastMessage is the most recent thing the agent said.
	LastMessage string `json:"lastMessage,omitempty"`
	// Result is the run's own closing summary, present only once the stream
	// reports one. It is never a substitute for the job's exit status.
	Result string `json:"result,omitempty"`
	// Error is the last error the stream reported, which an agent can emit and
	// still exit zero.
	Error string `json:"error,omitempty"`

	// lastMessageID separates one assistant turn from the next; a tool emits
	// several events per message and counting events would over-report turns.
	lastMessageID string
}

// Summary renders progress as the single line a status view shows, and is empty
// when nothing has been observed yet — an agent that has not emitted is honestly
// reported as such rather than as idle.
func (p AgentJobProgress) Summary() string {
	parts := make([]string, 0, 3)
	if activity := strings.TrimSpace(p.Activity); activity != "" {
		parts = append(parts, activity)
	}
	if p.Turns > 0 || p.ToolsRun > 0 {
		parts = append(parts, pluralizeAgentCount(p.Turns, "turn")+", "+pluralizeAgentCount(p.ToolsRun, "tool"))
	}
	if result := strings.TrimSpace(p.Result); result != "" {
		parts = append(parts, "result: "+result)
	} else if message := strings.TrimSpace(p.LastMessage); message != "" && len(parts) < 2 {
		parts = append(parts, message)
	}
	return strings.Join(parts, ", ")
}

// agentProgressReader folds an agent's event stream into progress as bytes
// arrive. It is fed directly from the child process's stdout/stderr writes
// (agentProgressReader.feed), independent of whatever the job's output log
// retains — the byte cap that bounds the log on disk must not also silence
// progress, since the progress fields it folds into are themselves small and
// bounded. Two of the process's own pipe-copying goroutines can write
// concurrently, so every method locks.
type agentProgressReader struct {
	tool string

	mu       sync.Mutex
	pending  []byte
	progress AgentJobProgress
}

func newAgentProgressReader(tool string) *agentProgressReader {
	return &agentProgressReader{tool: tool, progress: AgentJobProgress{Tool: tool}}
}

// snapshot returns progress as it currently stands.
func (r *agentProgressReader) snapshot() AgentJobProgress {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.progress
}

// feed folds newly-arrived bytes into progress. It is called from the same
// writer that captures the job's output, but it sees every byte the process
// wrote, not only the bytes the output cap kept.
func (r *agentProgressReader) feed(chunk []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.consume(chunk)
}

// consume splits the arrived bytes on newlines, folding complete lines and
// carrying the partial trailing one — a feed can land mid-line, and re-reading
// it from the start would double-count everything before it. Caller holds mu.
func (r *agentProgressReader) consume(chunk []byte) {
	buffer := append(r.pending, chunk...)
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
	// The tools interleave plain diagnostic lines with their JSON events; a line
	// that is not an event is not a parse failure worth reporting.
	if !strings.HasPrefix(trimmed, "{") {
		return
	}
	applied := false
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
	// SessionID rides on every event claude's stream-json mode emits, from the
	// very first ("system"/"init") one -- unlike Result, which arrives only on
	// the closing event.
	SessionID string `json:"session_id"`
}

type claudeStreamMessage struct {
	ID string `json:"id"`
	// Content is raw because the same key carries an array of blocks on an
	// assistant event and a plain string on some others.
	Content json.RawMessage `json:"content"`
}

type claudeContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// applyClaudeAgentEvent folds one `claude -p --output-format stream-json` event.
func applyClaudeAgentEvent(progress *AgentJobProgress, raw []byte) bool {
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

func applyClaudeAssistantEvent(progress *AgentJobProgress, event claudeStreamEvent) {
	if event.Message == nil {
		return
	}
	// One assistant message arrives as several events; the id is what separates
	// a new turn from another block of the same one.
	if id := strings.TrimSpace(event.Message.ID); id != "" && id != progress.lastMessageID {
		progress.lastMessageID = id
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

// agentToolInput is the union of the input keys the tools name a target with.
// The first non-empty one in the order read below is what a reader recognises
// the call by, so an unknown tool still reports something usable.
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
	// ThreadID names the conversation on codex's own "thread.started" event,
	// the first event a `codex exec --json` run emits -- codex's twin of
	// claude's per-event session_id above.
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

// applyCodexAgentEvent folds one `codex exec --json` thread event. Codex reports
// work as items rather than tool calls, so the item type is what the activity is
// named from.
func applyCodexAgentEvent(progress *AgentJobProgress, raw []byte) bool {
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

// applyCodexThreadStartedEvent captures the thread id off codex's first event,
// split out of applyCodexAgentEvent's switch so a plain call keeps that
// switch's own cyclomatic complexity from growing.
func applyCodexThreadStartedEvent(progress *AgentJobProgress, event codexStreamEvent) {
	if id := strings.TrimSpace(event.ThreadID); id != "" {
		progress.SessionID = id
	}
}

func applyCodexItem(progress *AgentJobProgress, item *codexStreamItem, completed bool) {
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
	// Only a completed item counts as work run; started and updated are the same
	// item still in flight, and counting each would inflate the total.
	if completed {
		progress.ToolsRun++
	}
	progress.LastTool = tool
	progress.LastTarget = target
	progress.Activity = agentActivityLabel(tool, target)
}

// codexItemTarget maps a codex thread item onto erun's own tool vocabulary, so a
// codex file_change and a claude Edit read the same way to a caller.
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

// agentActivityLabel turns a tool call into the line the desktop and the status
// view show, so an agent job reports "editing <file>" rather than "running".
func agentActivityLabel(tool, target string) string {
	verb := agentToolVerb(tool)
	if strings.TrimSpace(target) == "" {
		return verb
	}
	return verb + " " + target
}

// agentToolVerbs is the fixed vocabulary an activity line is normalized into. It
// is a closed set on purpose: a caller renders the same phrases whichever AI
// tool produced the event, and an unrecognised tool still reads sensibly.
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

func pluralizeAgentCount(count int, noun string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, noun)
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

func collapseAgentWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// truncateAgentText bounds a folded value on a rune boundary, so a record can
// never carry an agent's whole essay and never carries a broken rune either.
func truncateAgentText(value string) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= agentTextLimit {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:agentTextLimit])) + "…"
}
