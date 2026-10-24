package main

import (
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/ai"
)

func claudeConfigToUI(config configpkg.EnvironmentClaudeConfig) uiClaudeConfig {
	out := uiClaudeConfig{
		UseMantle:       copyBoolPtr(config.UseMantle),
		UseBedrock:      copyBoolPtr(config.UseBedrock),
		UseGateway:      copyBoolPtr(config.UseGateway),
		MaxOutputTokens: copyIntPtr(config.MaxOutputTokens),
		Effort:          copyStringPtr(config.Effort),
		DefaultModel:    copyStringPtr(config.DefaultModel),
		VerboseDebug:    config.VerboseDebug,
	}
	if models := config.NormalizedModels(); len(models) > 0 {
		out.Models = models
	}
	return out
}

func claudeConfigFromUI(config uiClaudeConfig) configpkg.EnvironmentClaudeConfig {
	models := []string(nil)
	if normalized := normalizeUIClaudeModels(config.Models); len(normalized) > 0 {
		models = normalized
	}
	return configpkg.EnvironmentClaudeConfig{
		UseMantle:       copyBoolPtr(config.UseMantle),
		UseBedrock:      copyBoolPtr(config.UseBedrock),
		UseGateway:      copyBoolPtr(config.UseGateway),
		Models:          models,
		MaxOutputTokens: copyIntPtr(config.MaxOutputTokens),
		Effort:          copyStringPtr(config.Effort),
		DefaultModel:    copyStringPtr(config.DefaultModel),
		VerboseDebug:    config.VerboseDebug,
	}
}

func claudeDefaultsForUI(gateway *configpkg.OpenRouterConfig) uiClaudeDefaults {
	minTokens, maxTokens := configpkg.ClaudeMaxOutputTokensRange()
	gatewayModels := gateway.ModelIDs()
	if gatewayModels == nil {
		gatewayModels = []string{}
	}
	return uiClaudeDefaults{
		UseMantle:  configpkg.DefaultClaudeUseMantle,
		UseBedrock: configpkg.DefaultClaudeUseBedrock,
		// The environment's own models, which is what an environment without a
		// gateway (or one that opted out of it) launches from. The catalog's set
		// travels separately below, because the Anthropic aliases are not models
		// a gateway necessarily serves and the catalog's ids are not models an
		// environment's own sign-in can.
		Models:          configpkg.DefaultClaudeAvailableModels(),
		MaxOutputTokens: configpkg.DefaultClaudeMaxOutputTokens,
		KnownModels:     configpkg.KnownClaudeModels(),
		MinTokens:       minTokens,
		MaxTokens:       maxTokens,
		Effort:          ai.DefaultClaudeEffortLevel,
		EffortLevels:    ai.ClaudeEffortLevels(),
		// The per-environment gateway controls override an erun-level catalog, so
		// they are only meaningful when one exists. Resolved from the catalog,
		// not the environment's effective gateway, so an environment that opted
		// out can still opt back in.
		GatewayConfigured:   gateway.Configured(),
		GatewayModels:       gatewayModels,
		GatewayDefaultModel: gateway.ResolveDefaultModel(),
	}
}

// resolveOpenRouterConfig reads the erun-level gateway catalog for the desktop's
// read models. A root config that cannot be read yields no catalog rather than
// failing an environment's config view: the unreadable case is the unconfigured
// one, and every path that acts on a gateway re-resolves it where it matters.
func (a *App) resolveOpenRouterConfig() *configpkg.OpenRouterConfig {
	gateway, err := configpkg.ResolveOpenRouterConfig(a.deps.store)
	if err != nil {
		return nil
	}
	return gateway
}

func normalizeUIClaudeModels(models []string) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		if m = strings.TrimSpace(m); m != "" {
			out = append(out, m)
		}
	}
	return out
}
