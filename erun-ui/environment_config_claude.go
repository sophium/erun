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
		// The environment's own models; the gateway catalog's set travels separately.
		Models:          configpkg.DefaultClaudeAvailableModels(),
		MaxOutputTokens: configpkg.DefaultClaudeMaxOutputTokens,
		KnownModels:     configpkg.KnownClaudeModels(),
		MinTokens:       minTokens,
		MaxTokens:       maxTokens,
		Effort:          ai.DefaultClaudeEffortLevel,
		EffortLevels:    ai.ClaudeEffortLevels(),
		// Resolved from the catalog, not the effective gateway, so an opted-out
		// environment can opt back in.
		GatewayConfigured:   gateway.Configured(),
		GatewayModels:       gatewayModels,
		GatewayDefaultModel: gateway.ResolveDefaultModel(),
	}
}

// resolveOpenRouterConfig treats an unreadable root config as no catalog;
// gateway actions re-resolve it where it matters.
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
