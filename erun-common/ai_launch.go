package eruncommon

import "strings"

const (
	defaultAITool       = "claude"
	defaultClaudeEffort = "max"
)

// claudeEffortLevels enumerates the valid `claude --effort` startup levels in
// ascending order; max is the default.
var claudeEffortLevels = []string{"low", "medium", "high", "xhigh", "max"}

func validClaudeEffort(level string) bool {
	level = strings.TrimSpace(level)
	for _, candidate := range claudeEffortLevels {
		if candidate == level {
			return true
		}
	}
	return false
}

func resolveClaudeEffort(config EnvironmentClaudeConfig) string {
	if config.Effort != nil {
		if level := strings.TrimSpace(*config.Effort); validClaudeEffort(level) {
			return level
		}
	}
	return defaultClaudeEffort
}

// AISessionLaunchCommand returns the shell command the AI tab's persistent
// remote session runs as its program: the configured AI tool, or — for claude —
// a guard that resumes the cwd's existing Claude Code session when one exists
// (else starts fresh), at the env's effort level. It runs once when the dtach
// session is created; reattaches never re-run it. Mirrors the command the
// desktop used to type into the shell (issues #451/#469); centralised here so
// `erun open --ai` can launch it pod-side and survive a disconnect. See #478.
func AISessionLaunchCommand(aiTool string, claude EnvironmentClaudeConfig) string {
	if tool := strings.TrimSpace(aiTool); tool != "" && tool != defaultAITool {
		return tool
	}
	return claudeLaunchGuard(resolveClaudeEffort(claude))
}

func claudeLaunchGuard(effort string) string {
	flag := ""
	if validClaudeEffort(effort) {
		flag = " --effort " + effort
	}
	return `if [ -d "$HOME/.claude/projects/$(pwd | tr / -)" ]; then claude --continue` + flag + `; else claude` + flag + `; fi`
}
