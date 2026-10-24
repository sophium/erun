package ai

import (
	"github.com/sophium/erun/erun-common"
)

// ClaudeEffortLevels returns the selectable effort levels in ascending order.
// A fresh copy, so a caller offering them as choices cannot reorder the source.
func ClaudeEffortLevels() []string {
	out := make([]string, len(eruncommon.ClaudeEffortLevels))
	copy(out, eruncommon.ClaudeEffortLevels)
	return out
}

// DefaultClaudeEffortLevel is the effort a session launches with when nothing
// chose one.
const DefaultClaudeEffortLevel = eruncommon.DefaultClaudeEffort

// ClaudeBackgroundEffort splits a resolved effort level between the two places
// a Claude background session carries it: settings keys, which the session
// re-reads on every restart, and launch flags, which only a recreated session
// picks up. The effortLevel setting rejects "max" (and ignores the effort
// environment variable), so that one level stays a flag.
func ClaudeBackgroundEffort(effort string) (map[string]any, []string) {
	switch {
	case effort == eruncommon.ClaudeEffortUltracode:
		return map[string]any{"ultracode": true}, nil
	case effort == "max":
		return map[string]any{}, []string{"--effort", "max"}
	case eruncommon.ValidClaudeEffort(effort):
		return map[string]any{"effortLevel": effort}, nil
	}
	return map[string]any{}, nil
}
