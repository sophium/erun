package eruncommon

import (
	"regexp"
	"strings"
)

const (
	defaultAITool       = "claude"
	defaultClaudeEffort = "ultracode"
	// claudeEffortUltracode is not a `claude --effort` value (Claude Code
	// rejects it): it is the settings key for "everything on" — xhigh
	// thinking effort plus standing multi-agent workflow orchestration —
	// enabled via `--settings '{"ultracode":true}'`.
	claudeEffortUltracode = "ultracode"
)

// claudeEffortLevels enumerates the selectable Claude effort levels in
// ascending order. The first five are `claude --effort` values (mirroring
// `claude --help`); ultracode sits above max as the desktop default and
// launches through --settings instead (see claudeEffortFlags).
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

// resolveClaudeLaunchModel picks the model the managed AI session starts on:
// the env's chosen default while it is one of the env's available models, else
// the first available model. A session never falls back to the agent's own
// default, which the environment may not be able to serve; an unusable chosen
// or available value is ignored rather than started.
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

// claudeModelTokenPattern keeps a persisted model name from reaching the
// launch script as anything but a plain argv token: alphanumerics plus the
// separators real Claude/Bedrock model ids use (. _ : / -).
var claudeModelTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._:/-]+$`)

// AISessionLaunchCommand returns the shell command the AI tab's persistent
// remote session runs as its program: the configured AI tool, or — for claude —
// a guard that resumes the cwd's existing Claude Code session when one exists
// (else starts fresh), at the env's effort level, on the env's default model
// when one is set, with --verbose --debug when the env opts in, and with Remote
// Control enabled by default so the operator can drive the session from the
// Claude iOS app. It runs once when the dtach session is created; reattaches
// never re-run it. Mirrors the command the desktop used to type into the shell;
// centralised here so `erun open --ai` can launch it pod-side and survive a
// disconnect.
func AISessionLaunchCommand(aiTool string, claude EnvironmentClaudeConfig, tenant, environment string) string {
	if tool := strings.TrimSpace(aiTool); tool != "" && tool != defaultAITool {
		return tool
	}
	prefix := claudeLaunchEnvPrefix(claude)
	flags := claudeLaunchFlags(claude, tenant, environment)
	return `if [ -d "$HOME/.claude/projects/$(pwd | tr / -)" ]; then ` + prefix + `claude --continue` + flags + `; else ` + prefix + `claude` + flags + `; fi`
}

// AISessionLaunchLines returns the dtach session's AI program as script
// lines: the launch plus the exit wrapper that keeps the tab honest when the
// tool ends. The launch used to be a bare line in the launcher
// body, so a killed Claude (OOM, crash, or a clean exit) silently fell
// through to the trailing interactive shell — a tab labelled "AI" showing a
// bash prompt with no explanation. The wrapper captures the exit status,
// names it (137 = SIGKILL, almost always the container's OOM killer, with
// the Runtime-settings memory hint), and prints the exact resume command
// before the shell takes over, so the state is explicit and recovery is one
// paste away.
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
		// The resume command goes through %s + shellQuote: it can carry
		// single quotes itself (the ultracode --settings JSON), which would
		// break the printf format's own quoting if inlined.
		`printf '\033[2;33m── resume with: %s — or use this shell ──\033[0m\n' ` + shellQuote(resume),
	}
}

// claudeLaunchFlags composes the managed Claude launch flags: the effort
// mechanism (--effort or the ultracode --settings), the env's default model,
// --verbose --debug, and Remote Control.
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

// claudeRemoteControlFlag enables Claude Code Remote Control on the managed
// AI-tab session by default so the operator can view and drive it from the
// Claude iOS app, naming the session <tenant>/<env> so each
// environment is distinct in the app. It is empty when the env authenticates
// Claude through the Bedrock or Mantle gateways: Remote Control pairs through
// the claude.ai account relay, which those gateway auth modes cannot satisfy,
// so enabling it there would fail to pair rather than help. It is also empty
// for a non-claude AI tool, which launches verbatim before this composes in.
// An unsafe tenant/env token falls back to the unnamed flag, leaving Claude
// Code to name the session from the pod hostname.
func claudeRemoteControlFlag(claude EnvironmentClaudeConfig, tenant, environment string) string {
	if claudeUsesGatewayAuth(claude) {
		return ""
	}
	if name := claudeRemoteControlSessionName(tenant, environment); name != "" {
		return " --remote-control " + name
	}
	return " --remote-control"
}

// claudeUsesGatewayAuth reports whether the env routes Claude through the
// Bedrock or Mantle inference gateways rather than a claude.ai account.
func claudeUsesGatewayAuth(claude EnvironmentClaudeConfig) bool {
	return (claude.UseBedrock != nil && *claude.UseBedrock) ||
		(claude.UseMantle != nil && *claude.UseMantle)
}

// claudeRemoteControlSessionName builds the <tenant>/<env> Remote Control
// session name, or "" when either identifier carries anything but a plain
// token safe to drop into the sh launch one-liner unquoted (the "/" separator
// itself is shell-safe).
func claudeRemoteControlSessionName(tenant, environment string) string {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if !claudeSessionNameTokenPattern.MatchString(tenant) ||
		!claudeSessionNameTokenPattern.MatchString(environment) {
		return ""
	}
	return tenant + "/" + environment
}

// claudeSessionNameTokenPattern constrains a tenant/env name to alphanumerics
// plus the separators erun identifiers use, so the composed <tenant>/<env>
// session name needs no shell quoting in the launch one-liner. It must also
// start with an alphanumeric: a leading '-' is shell-safe but would reach
// Claude Code's own argument parser looking like an option flag rather than
// the --remote-control value, so such names fall back to the unnamed flag.
var claudeSessionNameTokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// claudeLaunchEnvPrefix returns the process-scoped env assignment that mirrors
// the env's resolved default model into CLAUDE_CODE_SUBAGENT_MODEL, so
// subagents spawned in the managed Claude session run on the same model the
// session launches with via --model. It is a command-string
// prefix — not a launch flag and not a desktop PTY env entry — because for
// remote-agent envs the guard executes `claude` inside the pod via kubectl
// exec; only an in-string assignment crosses into the pod, exactly like
// --effort/--model. The token is already constrained to
// claudeModelTokenPattern by resolveClaudeLaunchModel, so it needs no quoting
// and composes with the single-quoted ultracode --settings JSON. Empty only
// when no available model is a safe token, leaving Claude Code's own subagent
// default in place.
func claudeLaunchEnvPrefix(claude EnvironmentClaudeConfig) string {
	if model := resolveClaudeLaunchModel(claude); model != "" {
		return "CLAUDE_CODE_SUBAGENT_MODEL=" + model + " "
	}
	return ""
}

// claudeEffortFlags maps a resolved effort level to its launch flags.
// ultracode is enabled through Claude Code's settings mechanism, not
// --effort (which rejects it), so it launches as `--settings
// '{"ultracode":true}'` — the JSON stays single-quoted because the guard is
// a sh one-liner and nothing inside it may interpolate. The five --effort
// levels launch as before.
func claudeEffortFlags(effort string) string {
	switch {
	case effort == claudeEffortUltracode:
		return ` --settings '{"ultracode":true}'`
	case validClaudeEffort(effort):
		return " --effort " + effort
	}
	return ""
}
