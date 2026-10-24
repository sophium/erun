package shared

import (
	"fmt"
	"strings"
)

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

	// LastMessageID separates one assistant turn from the next; a tool emits
	// several events per message and counting events would over-report turns.
	LastMessageID string
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

func pluralizeAgentCount(count int, noun string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, noun)
	}
	return fmt.Sprintf("%d %ss", count, noun)
}
