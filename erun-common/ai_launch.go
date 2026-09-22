package eruncommon

import (
	"regexp"
	"strconv"
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
//
// With a gateway configured the catalog supplies the selectable set, but an
// environment's own choice is honoured even when the catalog does not list it —
// typing an id the operator has not curated is a supported act, and refusing it
// here would silently launch a different model than the tab shows. Without a
// gateway the previous rule stands: the choice must be one of the environment's
// available models.
func resolveClaudeLaunchModel(config EnvironmentClaudeConfig, gateway *OpenRouterConfig) string {
	// Resolved through the environment's own opt-out, so an environment that
	// stays on its own Claude sign-in is never handed a catalog model.
	if effective := EffectiveGateway(config, gateway); effective.Configured() {
		return resolveGatewayLaunchModel(config, effective)
	}
	return resolveAvailableClaudeModel(config)
}

// claudeConfiguredModel returns the environment's own model choice when it is a
// token the launch can pass safely, and "" otherwise. A choice that is not a
// safe token is ignored rather than passed through.
func claudeConfiguredModel(config EnvironmentClaudeConfig) string {
	if config.DefaultModel == nil {
		return ""
	}
	model := strings.TrimSpace(*config.DefaultModel)
	if !claudeModelTokenPattern.MatchString(model) {
		return ""
	}
	return model
}

// resolveGatewayLaunchModel resolves against a gateway: the environment's choice
// wins even when the catalog does not list it, because typing an uncurated id is
// a supported act and silently substituting another model would launch
// something other than what the tab shows.
//
// One choice is refused rather than substituted: an id the catalog itself lists
// as requiring reasoning echo. That listing is the operator stating this model
// cannot be driven, and nothing about the environment's own record makes it
// driveable again, so launching it would substitute a mid-run provider refusal
// for a launch that never happens. It is not a silent substitution of the kind
// the rule above guards against — the id cannot be launched at all, and
// ModelIDs no longer offers it, so the resolved default is the only model the
// tab can present as selectable.
func resolveGatewayLaunchModel(config EnvironmentClaudeConfig, gateway *OpenRouterConfig) string {
	if model := claudeConfiguredModel(config); model != "" && !gateway.RequiresReasoningEcho(model) {
		return model
	}
	return gateway.ResolveDefaultModel()
}

// resolveAvailableClaudeModel resolves against an environment's own available
// set: the choice must be one of them, so a model the environment cannot serve
// is never launched.
func resolveAvailableClaudeModel(config EnvironmentClaudeConfig) string {
	available := config.NormalizedModels()
	if len(available) == 0 {
		available = DefaultClaudeAvailableModels()
	}
	if model := claudeConfiguredModel(config); model != "" {
		for _, candidate := range available {
			if candidate == model {
				return model
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
func AISessionLaunchCommand(aiTool string, claude EnvironmentClaudeConfig, gateway *OpenRouterConfig, tenant, environment string) string {
	if tool := strings.TrimSpace(aiTool); tool != "" && tool != defaultAITool {
		return tool
	}
	prefix := claudeLaunchEnvPrefix(claude, gateway)
	flags := claudeLaunchFlags(claude, gateway, tenant, environment)
	return `if [ -d "$HOME/.claude/projects/$(pwd | tr / -)" ]; then ` + prefix + `claude --continue` + flags + `; else ` + prefix + `claude` + flags + `; fi`
}

// AISessionLaunchLines returns the dtach session's AI program as script lines:
// the launch plus an exit wrapper. Without it a killed or exited Claude
// silently falls through to the trailing interactive shell — a tab labelled
// "AI" showing a bare bash prompt — so the wrapper makes the exit state
// explicit and puts the resume command one paste away.
func AISessionLaunchLines(aiTool string, claude EnvironmentClaudeConfig, gateway *OpenRouterConfig, tenant, environment string) []string {
	launch := AISessionLaunchCommand(aiTool, claude, gateway, tenant, environment)
	label := "Claude"
	resume := claudeLaunchEnvPrefix(claude, gateway) + "claude --continue" + claudeLaunchFlags(claude, gateway, tenant, environment)
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

func claudeLaunchFlags(claude EnvironmentClaudeConfig, gateway *OpenRouterConfig, tenant, environment string) string {
	flags := claudeEffortFlags(resolveClaudeEffort(claude))
	if model := resolveClaudeLaunchModel(claude, gateway); model != "" {
		flags += " --model " + model
	}
	if claude.VerboseDebug {
		flags += " --verbose --debug"
	}
	flags += claudeRemoteControlFlag(claude, gateway, tenant, environment)
	return flags
}

// claudeRemoteControlFlag enables Claude Code Remote Control by default so the
// operator can drive the managed AI session from the Claude iOS app, naming it
// <tenant>/<env> to keep each environment distinct. Gateway auth disables it:
// Remote Control pairs through the claude.ai account relay, which gateway
// credentials cannot satisfy, so enabling it would fail to pair.
func claudeRemoteControlFlag(claude EnvironmentClaudeConfig, gateway *OpenRouterConfig, tenant, environment string) string {
	if claudeUsesGatewayAuth(claude, gateway) {
		return ""
	}
	if name := claudeRemoteControlSessionName(tenant, environment); name != "" {
		return " --remote-control " + name
	}
	return " --remote-control"
}

// claudeUsesGatewayAuth reports whether the session authenticates through a
// gateway rather than a claude.ai account, which is what decides Remote
// Control eligibility.
func claudeUsesGatewayAuth(claude EnvironmentClaudeConfig, gateway *OpenRouterConfig) bool {
	// Through the opt-out, so an environment that left the gateway keeps Remote
	// Control, which a gateway credential could not pair.
	if EffectiveGateway(claude, gateway).Configured() {
		return true
	}
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
// launches with, and declares that model's context window so Claude Code does
// not have to assume one for an id it does not recognise.
//
// It must be an in-string command prefix, not a PTY env entry: for remote-agent
// envs the guard runs `claude` in the pod via kubectl exec, and only an
// in-string assignment crosses into the pod.
//
// The gateway credential is deliberately absent here. A launch command's argv
// is visible to anything that can list processes, so the token reaches the pod
// through Claude Code's settings env block instead — see OpenRouterConfig.
// AuthTokenRef. Only non-secret routing values belong in this prefix.
func claudeLaunchEnvPrefix(claude EnvironmentClaudeConfig, gateway *OpenRouterConfig) string {
	model := resolveClaudeLaunchModel(claude, gateway)
	if model == "" {
		return ""
	}
	prefix := "CLAUDE_CODE_SUBAGENT_MODEL=" + model + " "
	if context := gateway.ContextFor(model); context > 0 {
		prefix = "CLAUDE_CODE_MAX_CONTEXT_TOKENS=" + strconv.Itoa(context) + " " + prefix
	}
	return prefix
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
