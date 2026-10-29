package openrouter

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
)

// Fixed names for the gateway credential Secret erun writes into each namespace;
// the creator and the chart both read them from here.
const (
	GatewaySecretName = "erun-claude-gateway"
	GatewaySecretKey  = "token"
)

// EffectiveGateway returns the catalog unless the environment explicitly opted out
// (false); unset and true both inherit.
func EffectiveGateway(claude configpkg.EnvironmentClaudeConfig, catalog *configpkg.OpenRouterConfig) *configpkg.OpenRouterConfig {
	if !catalog.Configured() {
		return nil
	}
	if claude.UseGateway != nil && !*claude.UseGateway {
		return nil
	}
	return catalog
}

type RootConfigStore interface {
	LoadERunConfig() (configpkg.ERunConfig, string, error)
}

// ResolveOpenRouterConfig reads the catalog; a missing root config means no
// gateway rather than an error, so AI launches keep working.
func ResolveOpenRouterConfig(store RootConfigStore) (*configpkg.OpenRouterConfig, error) {
	var config configpkg.ERunConfig
	if store != nil {
		loaded, _, err := store.LoadERunConfig()
		if err != nil {
			return nil, err
		}
		config = loaded
	}
	return config.OpenRouter, nil
}
