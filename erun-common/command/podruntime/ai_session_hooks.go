package podruntime

import (
	"github.com/sophium/erun/erun-common/base/model"
)

// AI tool hooks are the only producer of AwaitingInput; output heuristics cannot represent it.

// AISessionHookVerbPath is the CLI verb an installed hook runs; the tool passes the session id on stdin.
const AISessionHookVerbPath = "erun activity ai-session hook"

// AISessionHookBinding binds a tool hook event to a model event.
type AISessionHookBinding struct {
	ToolEvent  string
	ModelEvent model.AISessionEventKind
}

// AISessionHookBindings is the hook mapping the pod's settings composer mirrors. Busy is
// reported on every tool call so long turns are not read as idle. SessionStart and
// subagent stops are deliberately unbound: neither is a session turn boundary.
func AISessionHookBindings() []AISessionHookBinding {
	return []AISessionHookBinding{
		{ToolEvent: "UserPromptSubmit", ModelEvent: model.AISessionEventTurnStart},
		{ToolEvent: "PreToolUse", ModelEvent: model.AISessionEventToolUse},
		{ToolEvent: "PostToolUse", ModelEvent: model.AISessionEventToolUse},
		{ToolEvent: "Stop", ModelEvent: model.AISessionEventTurnEnd},
		{ToolEvent: "Notification", ModelEvent: model.AISessionEventNotify},
		{ToolEvent: "SessionEnd", ModelEvent: model.AISessionEventExit},
	}
}
