package ailaunch

import (
	"regexp"
	"strconv"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/claudedefaults"
	"github.com/sophium/erun/erun-common/base/config/openrouter"
	"github.com/sophium/erun/erun-common/base/core"
)

const (
	defaultAITool       = "claude"
	DefaultClaudeEffort = "ultracode"
	// ClaudeEffortUltracode is a settings key, not a `claude --effort` value.
	ClaudeEffortUltracode = "ultracode"
)

// ClaudeEffortLevels lists the selectable effort levels.
var ClaudeEffortLevels = []string{"low", "medium", "high", "xhigh", "max", ClaudeEffortUltracode}

func ValidClaudeEffort(level string) bool {
	level = strings.TrimSpace(level)
	for _, candidate := range ClaudeEffortLevels {
		if candidate == level {
			return true
		}
	}
	return false
}

func ResolveClaudeEffort(config configpkg.EnvironmentClaudeConfig) string {
	if config.Effort != nil {
		if level := strings.TrimSpace(*config.Effort); ValidClaudeEffort(level) {
			return level
		}
	}
	return DefaultClaudeEffort
}

// ResolveClaudeLaunchModel never falls back to the agent's own default, which
// the environment may not be able to serve.
func ResolveClaudeLaunchModel(config configpkg.EnvironmentClaudeConfig, gateway *configpkg.OpenRouterConfig) string {
	if effective := openrouter.EffectiveGateway(config, gateway); effective.Configured() {
		return ResolveGatewayLaunchModel(config, effective)
	}
	return resolveAvailableClaudeModel(config)
}

// ClaudeConfiguredModel returns the environment's model if it is a shell-safe token.
func ClaudeConfiguredModel(config configpkg.EnvironmentClaudeConfig) string {
	if config.DefaultModel == nil {
		return ""
	}
	model := strings.TrimSpace(*config.DefaultModel)
	if !configpkg.ClaudeModelTokenPattern.MatchString(model) {
		return ""
	}
	return model
}

// ResolveGatewayLaunchModel honours an uncurated choice, except one the catalog
// lists as requiring reasoning echo, which cannot be driven at all.
func ResolveGatewayLaunchModel(config configpkg.EnvironmentClaudeConfig, gateway *configpkg.OpenRouterConfig) string {
	if model := ClaudeConfiguredModel(config); model != "" && !gateway.RequiresReasoningEcho(model) {
		return model
	}
	return gateway.ResolveDefaultModel()
}

func resolveAvailableClaudeModel(config configpkg.EnvironmentClaudeConfig) string {
	available := config.NormalizedModels()
	if len(available) == 0 {
		available = claudedefaults.DefaultClaudeAvailableModels()
	}
	if model := ClaudeConfiguredModel(config); model != "" {
		for _, candidate := range available {
			if candidate == model {
				return model
			}
		}
	}
	for _, candidate := range available {
		if configpkg.ClaudeModelTokenPattern.MatchString(candidate) {
			return candidate
		}
	}
	return ""
}

// AISessionLaunchCommand returns the AI tab's dtach program; it runs once per
// session, not on reattach. Never add --fork-session: a new id per resume
// breaks conversation tracking.
func AISessionLaunchCommand(aiTool string, claude configpkg.EnvironmentClaudeConfig, gateway *configpkg.OpenRouterConfig, tenant, environment string) string {
	if tool := strings.TrimSpace(aiTool); tool != "" && tool != defaultAITool {
		return tool
	}
	prefix := claudeLaunchEnvPrefix(claude, gateway)
	flags := claudeLaunchFlags(claude, gateway, tenant, environment)
	// The runtime image links claude to erun, which resumes the cwd's conversation.
	return prefix + `claude` + flags
}

// AISessionLaunchLines wraps the launch so an exit is reported instead of
// silently dropping to the trailing shell.
func AISessionLaunchLines(aiTool string, claude configpkg.EnvironmentClaudeConfig, gateway *configpkg.OpenRouterConfig, tenant, environment string) []string {
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
		// Quoted: the ultracode --settings JSON carries single quotes.
		`printf '\033[2;33m── resume with: %s — or use this shell ──\033[0m\n' ` + core.ShellQuote(resume),
	}
}

func claudeLaunchFlags(claude configpkg.EnvironmentClaudeConfig, gateway *configpkg.OpenRouterConfig, tenant, environment string) string {
	flags := ClaudeEffortFlags(ResolveClaudeEffort(claude))
	if model := ResolveClaudeLaunchModel(claude, gateway); model != "" {
		flags += " --model " + model
	}
	if claude.VerboseDebug {
		flags += " --verbose --debug"
	}
	flags += claudeRemoteControlFlag(claude, gateway, tenant, environment)
	return flags
}

// claudeRemoteControlFlag is off under gateway auth, which cannot pair through
// the claude.ai account relay.
func claudeRemoteControlFlag(claude configpkg.EnvironmentClaudeConfig, gateway *configpkg.OpenRouterConfig, tenant, environment string) string {
	if claudeUsesGatewayAuth(claude, gateway) {
		return ""
	}
	if name := claudeRemoteControlSessionName(tenant, environment); name != "" {
		return " --remote-control " + name
	}
	return " --remote-control"
}

func claudeUsesGatewayAuth(claude configpkg.EnvironmentClaudeConfig, gateway *configpkg.OpenRouterConfig) bool {
	if openrouter.EffectiveGateway(claude, gateway).Configured() {
		return true
	}
	return (claude.UseBedrock != nil && *claude.UseBedrock) ||
		(claude.UseMantle != nil && *claude.UseMantle)
}

func claudeRemoteControlSessionName(tenant, environment string) string {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if !claudeSessionNameTokenPattern.MatchString(tenant) ||
		!claudeSessionNameTokenPattern.MatchString(environment) {
		return ""
	}
	return tenant + "/" + environment
}

// claudeSessionNameTokenPattern is shell-safe and must not start with '-', which
// Claude Code would parse as a flag.
var claudeSessionNameTokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// claudeLaunchEnvPrefix must be an in-string prefix (only that crosses kubectl
// exec) and must never carry the gateway token, since argv is visible.
func claudeLaunchEnvPrefix(claude configpkg.EnvironmentClaudeConfig, gateway *configpkg.OpenRouterConfig) string {
	model := ResolveClaudeLaunchModel(claude, gateway)
	if model == "" {
		return ""
	}
	prefix := "CLAUDE_CODE_SUBAGENT_MODEL=" + model + " "
	if context := openrouter.EffectiveGateway(claude, gateway).ContextFor(model); context > 0 {
		prefix = "CLAUDE_CODE_MAX_CONTEXT_TOKENS=" + strconv.Itoa(context) + " " + prefix
	}
	return prefix
}

// ClaudeEffortFlags single-quotes ultracode's JSON so nothing interpolates.
func ClaudeEffortFlags(effort string) string {
	switch {
	case effort == ClaudeEffortUltracode:
		return ` --settings '{"ultracode":true}'`
	case ValidClaudeEffort(effort):
		return " --effort " + effort
	}
	return ""
}
