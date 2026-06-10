package eruncommon

import (
	"regexp"
	"strings"
)

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

// resolveClaudeDefaultModel returns the env's configured default Claude model
// when it is one of the env's available models (the explicit models list, or
// the default available set when the env declares none) and a safe shell
// token; otherwise "" so the session never launches with a --model the env
// does not expose (issue #482). Models are opaque tokens to erun — resolving
// a name like "fable" to a concrete model is Claude Code's / the env's
// provider-config concern.
func resolveClaudeDefaultModel(config EnvironmentClaudeConfig) string {
	if config.DefaultModel == nil {
		return ""
	}
	model := strings.TrimSpace(*config.DefaultModel)
	if !claudeModelTokenPattern.MatchString(model) {
		return ""
	}
	available := config.NormalizedModels()
	if len(available) == 0 {
		available = DefaultClaudeAvailableModels()
	}
	for _, candidate := range available {
		if candidate == model {
			return model
		}
	}
	return ""
}

// claudeModelTokenPattern keeps a persisted model name from reaching the
// launch script as anything but a plain argv token: alphanumerics plus the
// separators real Claude/Bedrock model ids use (. _ : / -).
var claudeModelTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._:/-]+$`)

// AISessionLaunchCommand returns the shell command the AI tab's persistent
// remote session runs as its program: the configured AI tool, or — for claude —
// a guard that resumes the cwd's existing Claude Code session when one exists
// (else starts fresh), at the env's effort level, on the env's default model
// when one is set (issue #482), and with --verbose --debug when the env opts
// in (issue #477). It runs once when the dtach session is created; reattaches
// never re-run it. Mirrors the command the desktop used to type into the
// shell (issues #451/#469); centralised here so `erun open --ai` can launch
// it pod-side and survive a disconnect. See #478.
func AISessionLaunchCommand(aiTool string, claude EnvironmentClaudeConfig) string {
	if tool := strings.TrimSpace(aiTool); tool != "" && tool != defaultAITool {
		return tool
	}
	return claudeLaunchGuard(resolveClaudeEffort(claude), resolveClaudeDefaultModel(claude), claude.VerboseDebug)
}

func claudeLaunchGuard(effort, model string, verboseDebug bool) string {
	flags := ""
	if validClaudeEffort(effort) {
		flags = " --effort " + effort
	}
	if model != "" {
		flags += " --model " + model
	}
	if verboseDebug {
		flags += " --verbose --debug"
	}
	return `if [ -d "$HOME/.claude/projects/$(pwd | tr / -)" ]; then claude --continue` + flags + `; else claude` + flags + `; fi`
}
