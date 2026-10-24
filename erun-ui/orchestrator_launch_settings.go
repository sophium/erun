package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/ai"
	"github.com/sophium/erun/erun-common/command/orchestrator"
)

// orchestratorLaunchPlan is one persisted orchestrator's launch, resolved at
// the moment it starts: the model and effort it launches on, the settings it
// was resolved from (what its row reports), and the full process environment
// it needs.
//
// env carries the gateway credential when the launch routes through one, so it
// is a secret: it reaches the PTY's environment and nothing else.
type orchestratorLaunchPlan struct {
	launch   orchestrator.OrchestratorLaunch
	settings orchestratorLaunchInput
	env      []string
}

// orchestratorResolvedLaunch is the non-secret part of a resolved launch that
// decides what a session runs on. It is compared rather than the recorded
// settings, which disagree with it both ways: a catalog's default model can
// move under an orchestrator that names none, and naming the default
// explicitly changes the record without changing the launch.
type orchestratorResolvedLaunch struct {
	Model    string
	Effort   string
	Endpoint string
}

func resolvedLaunchOf(launch orchestrator.OrchestratorLaunch) orchestratorResolvedLaunch {
	return orchestratorResolvedLaunch{Model: launch.Model, Effort: launch.Effort, Endpoint: launch.Gateway.Endpoint()}
}

// orchestratorLaunchDiffers reports whether def, resolved against catalog now,
// would launch on something other than launched. A definition that no longer
// resolves cannot be what is running, so it differs.
func orchestratorLaunchDiffers(def config.OrchestratorConfig, catalog *config.OpenRouterConfig, launched orchestratorResolvedLaunch) bool {
	launch, err := orchestrator.ResolveOrchestratorLaunch(def, catalog)
	return err != nil || resolvedLaunchOf(launch) != launched
}

// orchestratorGatewayTokenEnvVar carries the gateway credential into a session
// launched through the gateway, as a pod's runtime receives it.
const orchestratorGatewayTokenEnvVar = "ANTHROPIC_AUTH_TOKEN"

// defaultOrchestratorLaunch is how an orchestrator with no settings launches.
// A zero OrchestratorConfig always resolves, so the error cannot occur.
func defaultOrchestratorLaunch() orchestrator.OrchestratorLaunch {
	launch, _ := orchestrator.ResolveOrchestratorLaunch(config.OrchestratorConfig{}, nil)
	return launch
}

func defaultOrchestratorLaunchPlan() orchestratorLaunchPlan {
	launch := defaultOrchestratorLaunch()
	return orchestratorLaunchPlan{launch: launch, env: append([]string(nil), launch.Env...)}
}

// orchestratorLaunchSettingsOf reads an orchestrator's launch settings as
// recorded, trimmed the way the writers store them.
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

// orchestratorLaunchSettingsRemedy names what the operator can do about
// settings orchestrator.ValidateOrchestratorLaunchSettings refused, whose reasons
// deliberately name no surface.
func orchestratorLaunchSettingsRemedy(settings orchestratorLaunchInput, catalog *config.OpenRouterConfig) string {
	if settings.UseGateway && !catalog.Configured() {
		return "configure the gateway catalog in the desktop's ERun settings, or turn this orchestrator's gateway off"
	}
	return "choose another model or effort for this orchestrator"
}

// validateOrchestratorSettings validates the dialog's alias and launch settings
// against what this host has configured, returning them normalized together
// with the catalog they were validated against.
func (a *App) validateOrchestratorSettings(alias string, launch orchestratorLaunchInput) (string, orchestratorLaunchInput, *config.OpenRouterConfig, error) {
	alias = strings.TrimSpace(alias)
	if err := orchestrator.ValidateOrchestratorAlias(a.deps.store, alias); err != nil {
		return "", orchestratorLaunchInput{}, nil, fmt.Errorf("orchestrator alias: %w", err)
	}
	launch.Model, launch.Effort = clearedLaunchSetting(launch.Model), clearedLaunchSetting(launch.Effort)
	catalog := a.resolveOpenRouterConfig()
	if err := orchestrator.ValidateOrchestratorLaunchSettings(withOrchestratorLaunchSettings(config.OrchestratorConfig{}, launch), catalog); err != nil {
		return "", orchestratorLaunchInput{}, nil, fmt.Errorf("orchestrator launch settings: %w -- %s", err, orchestratorLaunchSettingsRemedy(launch, catalog))
	}
	return alias, launch, catalog, nil
}

// clearedLaunchSetting trims a model or effort and maps the CLI's clearing
// token to the empty default, as the CLI's own writers do, so the token is
// never stored.
func clearedLaunchSetting(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, orchestrator.OrchestratorLaunchSettingDefault) {
		return ""
	}
	return value
}

// resolvePersistedOrchestratorLaunch resolves def's launch, credential
// included, before anything about a running session is torn down. Settings
// that cannot be honoured refuse the start rather than launching something else.
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

// orchestratorLaunchEnv is launch's environment plus, through a gateway, the
// credential it authenticates with.
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

// orchestratorLaunchSettingsChanged reports whether def's live session would
// launch differently if restarted against catalog; false when nothing is
// running for it.
func (a *App) orchestratorLaunchSettingsChanged(def config.OrchestratorConfig, catalog *config.OpenRouterConfig) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	session := a.orchestrators[def.ID]
	managed := a.sessions[orchestratorSessionKey(def.ID)]
	if session == nil || managed == nil || managed.closed {
		return false
	}
	return orchestratorLaunchDiffers(def, catalog, session.launched)
}

// withConfiguredLaunchSettings fills a running row built from its session with
// the configured launch settings. A row without a definition (a transient
// session) carries none.
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

// undriveableGatewayModels lists the catalog's entries the gateway cannot
// drive, in configured order without blanks or duplicates; never nil.
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
