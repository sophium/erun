package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/ai"
	"github.com/sophium/erun/erun-common/command/orchestrator"
	"github.com/sophium/erun/erun-ui/internal/terminal"
)

// orchestratorLaunchPlan is a launch resolved at start. env may carry the gateway
// credential, so it reaches the PTY environment and nothing else.
type orchestratorLaunchPlan struct {
	launch   orchestrator.OrchestratorLaunch
	settings orchestratorLaunchInput
	env      []string
}

// orchestratorResolvedLaunch is compared instead of recorded settings: a catalog
// default can move, and naming the default changes nothing.
type orchestratorResolvedLaunch struct {
	Model    string
	Effort   string
	Endpoint string
}

func resolvedLaunchOf(launch orchestrator.OrchestratorLaunch) orchestratorResolvedLaunch {
	return orchestratorResolvedLaunch{Model: launch.Model, Effort: launch.Effort, Endpoint: launch.Gateway.Endpoint()}
}

// orchestratorLaunchDiffers treats an unresolvable definition as differing.
func orchestratorLaunchDiffers(def config.OrchestratorConfig, catalog *config.OpenRouterConfig, launched orchestratorResolvedLaunch) bool {
	launch, err := orchestrator.ResolveOrchestratorLaunch(def, catalog)
	return err != nil || resolvedLaunchOf(launch) != launched
}

const orchestratorGatewayTokenEnvVar = "ANTHROPIC_AUTH_TOKEN"

// defaultOrchestratorLaunch cannot fail: a zero OrchestratorConfig always resolves.
func defaultOrchestratorLaunch() orchestrator.OrchestratorLaunch {
	launch, _ := orchestrator.ResolveOrchestratorLaunch(config.OrchestratorConfig{}, nil)
	return launch
}

func defaultOrchestratorLaunchPlan() orchestratorLaunchPlan {
	launch := defaultOrchestratorLaunch()
	return orchestratorLaunchPlan{launch: launch, env: append([]string(nil), launch.Env...)}
}

func orchestratorLaunchSettingsOf(def config.OrchestratorConfig) orchestratorLaunchInput {
	return orchestratorLaunchInput{
		UseGateway: def.UseGateway,
		Model:      strings.TrimSpace(def.Model),
		Effort:     strings.TrimSpace(def.Effort),
	}
}

func withOrchestratorLaunchSettings(def config.OrchestratorConfig, settings orchestratorLaunchInput) config.OrchestratorConfig {
	def.UseGateway = settings.UseGateway
	def.Model = settings.Model
	def.Effort = settings.Effort
	return def
}

func orchestratorInfoWithLaunchSettings(info orchestratorInfo, settings orchestratorLaunchInput, changed bool) orchestratorInfo {
	info.UseGateway = settings.UseGateway
	info.Model = settings.Model
	info.Effort = settings.Effort
	info.LaunchSettingsChanged = changed
	return info
}

// orchestratorLaunchSettingsRemedy adds the remedy that validation reasons deliberately omit.
func orchestratorLaunchSettingsRemedy(settings orchestratorLaunchInput, catalog *config.OpenRouterConfig) string {
	if settings.UseGateway && !catalog.Configured() {
		return "configure the gateway catalog in the desktop's ERun settings, or turn this orchestrator's gateway off"
	}
	return "choose another model or effort for this orchestrator"
}

func (a *App) validateOrchestratorSettings(alias string, launch orchestratorLaunchInput) (string, orchestratorLaunchInput, *config.OpenRouterConfig, error) {
	alias = strings.TrimSpace(alias)
	if err := orchestrator.ValidateOrchestratorAlias(a.deps.store, alias); err != nil {
		return "", orchestratorLaunchInput{}, nil, fmt.Errorf("orchestrator alias: %w", err)
	}
	launch.Model, launch.Effort = clearedLaunchSetting(launch.Model), clearedLaunchSetting(launch.Effort)
	catalog := a.settings.ResolveOpenRouterConfig()
	if err := orchestrator.ValidateOrchestratorLaunchSettings(withOrchestratorLaunchSettings(config.OrchestratorConfig{}, launch), catalog); err != nil {
		return "", orchestratorLaunchInput{}, nil, fmt.Errorf("orchestrator launch settings: %w -- %s", err, orchestratorLaunchSettingsRemedy(launch, catalog))
	}
	return alias, launch, catalog, nil
}

// clearedLaunchSetting maps the CLI's clearing token to "", so it is never stored.
func clearedLaunchSetting(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, orchestrator.OrchestratorLaunchSettingDefault) {
		return ""
	}
	return value
}

// resolvePersistedOrchestratorLaunch runs before a running session is torn down;
// unhonourable settings refuse the start.
func (a *App) resolvePersistedOrchestratorLaunch(def config.OrchestratorConfig) (orchestratorLaunchPlan, error) {
	settings := orchestratorLaunchSettingsOf(def)
	catalog, err := config.ResolveOpenRouterConfig(a.deps.store)
	if err != nil && settings.UseGateway && !errors.Is(err, config.ErrNotInitialized) {
		return orchestratorLaunchPlan{}, fmt.Errorf("start orchestrator %s: cannot read the gateway catalog: %w", def.ID, err)
	}
	launch, err := orchestrator.ResolveOrchestratorLaunch(def, catalog)
	if err != nil {
		return orchestratorLaunchPlan{}, fmt.Errorf("start orchestrator %s: %w -- %s", def.ID, err, orchestratorLaunchSettingsRemedy(settings, catalog))
	}
	env, err := a.orchestratorLaunchEnv(launch)
	if err != nil {
		return orchestratorLaunchPlan{}, fmt.Errorf("start orchestrator %s through the gateway: %w -- save the gateway key in the desktop's ERun settings", def.ID, err)
	}
	return orchestratorLaunchPlan{launch: launch, settings: settings, env: env}, nil
}

func (a *App) orchestratorLaunchEnv(launch orchestrator.OrchestratorLaunch) ([]string, error) {
	env := append([]string(nil), launch.Env...)
	if launch.Gateway == nil {
		return env, nil
	}
	token, err := a.deps.resolveGatewayCredential(launch.Gateway)
	if err != nil {
		return nil, err
	}
	return append(env, orchestratorGatewayTokenEnvVar+"="+token), nil
}

func (a *App) orchestratorLaunchSettingsChanged(def config.OrchestratorConfig, catalog *config.OpenRouterConfig) bool {
	changed := false
	a.terminal.WithLock(func(l *terminal.Locked) {
		session := a.orchestrators[def.ID]
		managed := l.Session(terminal.OrchestratorSessionKey(def.ID))
		if session == nil || managed == nil || managed.Closed {
			return
		}
		changed = orchestratorLaunchDiffers(def, catalog, session.launched)
	})
	return changed
}

func (a *App) withConfiguredLaunchSettings(info orchestratorInfo) orchestratorInfo {
	configs, catalog, err := a.loadOrchestratorConfigsWithCatalog()
	if err != nil {
		return info
	}
	for _, def := range configs {
		if def.ID == info.ID {
			return orchestratorInfoWithLaunchSettings(info, orchestratorLaunchSettingsOf(def), a.orchestratorLaunchSettingsChanged(def, catalog))
		}
	}
	return info
}

func orchestratorLaunchChoicesFor(catalog *config.OpenRouterConfig) orchestratorLaunchChoices {
	models := catalog.ModelIDs()
	if models == nil {
		models = []string{}
	}
	return orchestratorLaunchChoices{
		GatewayConfigured:        catalog.Configured(),
		GatewayModels:            models,
		GatewayUndriveableModels: undriveableGatewayModels(catalog),
		GatewayDefaultModel:      catalog.ResolveDefaultModel(),
		DefaultModel:             orchestrator.DefaultOrchestratorModel,
		EffortLevels:             ai.ClaudeEffortLevels(),
		DefaultEffort:            ai.DefaultClaudeEffortLevel,
	}
}

// undriveableGatewayModels never returns nil.
func undriveableGatewayModels(catalog *config.OpenRouterConfig) []string {
	out := []string{}
	if catalog == nil {
		return out
	}
	seen := map[string]struct{}{}
	for _, m := range catalog.Models {
		id := strings.TrimSpace(m.ID)
		if _, dup := seen[id]; dup || id == "" || !catalog.RequiresReasoningEcho(id) {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
