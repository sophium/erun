package ai

import (
	"github.com/sophium/erun/erun-common/primitive/open/ailaunch"
)

// ClaudeEffortLevels returns a copy of the effort levels in ascending order.
func ClaudeEffortLevels() []string {
	out := make([]string, len(ailaunch.ClaudeEffortLevels))
	copy(out, ailaunch.ClaudeEffortLevels)
	return out
}

// DefaultClaudeEffortLevel is the effort used when none was chosen.
const DefaultClaudeEffortLevel = ailaunch.DefaultClaudeEffort

// ClaudeBackgroundEffort splits effort into settings keys (re-read on restart)
// and launch flags; the effortLevel setting rejects "max", so it stays a flag.
func ClaudeBackgroundEffort(effort string) (map[string]any, []string) {
	switch {
	case effort == ailaunch.ClaudeEffortUltracode:
		return map[string]any{"ultracode": true}, nil
	case effort == "max":
		return map[string]any{}, []string{"--effort", "max"}
	case ailaunch.ValidClaudeEffort(effort):
		return map[string]any{"effortLevel": effort}, nil
	}
	return map[string]any{}, nil
}
