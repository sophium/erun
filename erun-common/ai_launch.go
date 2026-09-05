package eruncommon

import (
	"regexp"
	"strings"
)

const (
	defaultAITool       = "claude"
	defaultClaudeEffort = "ultracode"
	// claudeEffortUltracode is not a `claude --effort` value (Claude Code
	// rejects it) but a settings key meaning "everything on" — xhigh thinking
	// plus standing multi-agent workflow orchestration.
	claudeEffortUltracode = "ultracode"
)

// claudeEffortLevels lists the selectable effort levels; all but ultracode are
// real `claude --effort` values (ultracode launches through --settings).
var claudeEffortLevels = []string{"low", "medium", "high", "xhigh", "max", claudeEffortUltracode}

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

// resolveClaudeLaunchModel picks the model the managed AI session starts on,
// never the agent's own default: the environment may not be able to serve it.
func resolveClaudeLaunchModel(config EnvironmentClaudeConfig) string {
	available := config.NormalizedModels()
	if len(available) == 0 {
		available = DefaultClaudeAvailableModels()
	}
	if config.DefaultModel != nil {
		if model := strings.TrimSpace(*config.DefaultModel); claudeModelTokenPattern.MatchString(model) {
			for _, candidate := range available {
				if candidate == model {
					return model
				}
			}
		}
	}
	for _, candidate := range available {
		if claudeModelTokenPattern.MatchString(candidate) {
			return candidate
		}
	}
	return ""
}

// claudeModelTokenPattern guards a persisted model name so it can only reach the
// launch script as a plain argv token, never shell or flag syntax.
var claudeModelTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._:/-]+$`)

// AISessionLaunchCommand returns the shell command the AI tab's persistent
// remote session runs as its program — for claude, a guard that resumes the
// cwd's existing session or starts fresh. It runs once when the dtach session
// is created; reattaches never re-run it. Centralised so `erun open --ai` can
// launch it pod-side and survive a disconnect.
//
// Never add --fork-session here: it mints a new session id on every resume,
// which reintroduces the conversation-id drift this launch command exists to
// avoid (an orchestrator resuming the wrong, empty conversation after a pod
// restart). If a future resume bug looks like it needs a fresh id, that is a
// sign the underlying id tracking is wrong, not that this flag is missing.
func AISessionLaunchCommand(aiTool string, claude EnvironmentClaudeConfig, tenant, environment string) string {
	if tool := strings.TrimSpace(aiTool); tool != "" && tool != defaultAITool {
		return tool
	}
	prefix := claudeLaunchEnvPrefix(claude)
	flags := claudeLaunchFlags(claude, tenant, environment)
	return `if [ -d "$HOME/.claude/projects/$(pwd | tr / -)" ]; then ` + prefix + `claude --continue` + flags + `; else ` + prefix + `claude` + flags + `; fi`
}

// AISessionLaunchLines returns the dtach session's AI program as script lines:
// the launch plus an exit wrapper. Without it a killed or exited Claude
// silently falls through to the trailing interactive shell — a tab labelled
// "AI" showing a bare bash prompt — so the wrapper makes the exit state
// explicit and puts the resume command one paste away.
func AISessionLaunchLines(aiTool string, claude EnvironmentClaudeConfig, tenant, environment string) []string {
	launch := AISessionLaunchCommand(aiTool, claude, tenant, environment)
	label := "Claude"
	resume := claudeLaunchEnvPrefix(claude) + "claude --continue" + claudeLaunchFlags(claude, tenant, environment)
	if tool := strings.TrimSpace(aiTool); tool != "" && tool != defaultAITool {
		label = "The AI tool"
		resume = tool
	}
	return []string{
		"ai_status=0",
		launch + " || ai_status=$?",
		`if [ "$ai_status" = 137 ]; then printf '\n\033[2;33m── ` + label + ` was killed (exit 137) — likely out of memory; consider raising Memory in the environment Runtime settings ──\033[0m\n'; elif [ "$ai_status" != 0 ]; then printf '\n\033[2;33m── ` + label + ` exited (exit %s) ──\033[0m\n' "$ai_status"; else printf '\n\033[2;33m── ` + label + ` session ended ──\033[0m\n'; fi`,
		// shellQuote, not inlining: the resume command can carry single quotes
		// (the ultracode --settings JSON) that would break the printf format.
		`printf '\033[2;33m── resume with: %s — or use this shell ──\033[0m\n' ` + shellQuote(resume),
	}
}

func claudeLaunchFlags(claude EnvironmentClaudeConfig, tenant, environment string) string {
	flags := claudeEffortFlags(resolveClaudeEffort(claude))
	if model := resolveClaudeLaunchModel(claude); model != "" {
		flags += " --model " + model
	}
	if claude.VerboseDebug {
		flags += " --verbose --debug"
	}
	flags += claudeRemoteControlFlag(claude, tenant, environment)
	return flags
}

// claudeRemoteControlFlag enables Claude Code Remote Control by default so the
// operator can drive the managed AI session from the Claude iOS app, naming it
// <tenant>/<env> to keep each environment distinct. Gateway (Bedrock/Mantle)
// auth disables it: Remote Control pairs through the claude.ai account relay,
// which those auth modes cannot satisfy, so enabling it would fail to pair.
func claudeRemoteControlFlag(claude EnvironmentClaudeConfig, tenant, environment string) string {
	if claudeUsesGatewayAuth(claude) {
		return ""
	}
	if name := claudeRemoteControlSessionName(tenant, environment); name != "" {
		return " --remote-control " + name
	}
	return " --remote-control"
}

func claudeUsesGatewayAuth(claude EnvironmentClaudeConfig) bool {
	return (claude.UseBedrock != nil && *claude.UseBedrock) ||
		(claude.UseMantle != nil && *claude.UseMantle)
}

// claudeRemoteControlSessionName builds the <tenant>/<env> session name, or ""
// when either identifier is not shell-safe for the unquoted launch one-liner.
func claudeRemoteControlSessionName(tenant, environment string) string {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if !claudeSessionNameTokenPattern.MatchString(tenant) ||
		!claudeSessionNameTokenPattern.MatchString(environment) {
		return ""
	}
	return tenant + "/" + environment
}

// claudeSessionNameTokenPattern keeps the composed <tenant>/<env> name shell-safe
// so the launch one-liner needs no quoting. It must start with an alphanumeric:
// a leading '-' is shell-safe but would look like an option flag to Claude
// Code's own parser, not the --remote-control value.
var claudeSessionNameTokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// claudeLaunchEnvPrefix mirrors the resolved default model into
// CLAUDE_CODE_SUBAGENT_MODEL so subagents run on the same model the session
// launches with. It must be an in-string command prefix, not a PTY env entry:
// for remote-agent envs the guard runs `claude` in the pod via kubectl exec,
// and only an in-string assignment crosses into the pod.
func claudeLaunchEnvPrefix(claude EnvironmentClaudeConfig) string {
	if model := resolveClaudeLaunchModel(claude); model != "" {
		return "CLAUDE_CODE_SUBAGENT_MODEL=" + model + " "
	}
	return ""
}

// claudeEffortFlags maps a resolved effort level to its launch flags. ultracode
// launches as `--settings '{"ultracode":true}'`, single-quoted because the
// guard is a sh one-liner in which nothing may interpolate.
func claudeEffortFlags(effort string) string {
	switch {
	case effort == claudeEffortUltracode:
		return ` --settings '{"ultracode":true}'`
	case validClaudeEffort(effort):
		return " --effort " + effort
	}
	return ""
}
