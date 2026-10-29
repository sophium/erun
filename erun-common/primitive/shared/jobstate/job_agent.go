package jobstate

import (
	"fmt"
	"strings"
)

// AgentJobProgress is erun's vendor-neutral view of an agent run (claude or codex).
type AgentJobProgress struct {
	Tool string `json:"tool,omitempty"`
	// SessionID is the tool's own conversation id (claude session_id, codex thread_id),
	// used to resume a follow-up turn. Empty until the tool names it.
	SessionID string `json:"sessionId,omitempty"`
	// Activity clears once the run reports a result.
	Activity    string `json:"activity,omitempty"`
	LastTool    string `json:"lastTool,omitempty"`
	LastTarget  string `json:"lastTarget,omitempty"`
	Turns       int    `json:"turns"`
	ToolsRun    int    `json:"toolsRun"`
	Events      int    `json:"events"`
	LastMessage string `json:"lastMessage,omitempty"`
	// Result is never a substitute for the job's exit status.
	Result string `json:"result,omitempty"`
	// Error can be set even when the agent exits zero.
	Error string `json:"error,omitempty"`

	// LastMessageID separates turns; one message spans several events.
	LastMessageID string
}

// Summary is empty until something has been observed.
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
