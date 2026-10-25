package orchestrator

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/ai"
	orchcmd "github.com/sophium/erun/erun-common/command/orchestrator"
)

// orchestratorLaunchPlan is a launch resolved at start. env may carry the gateway
// credential, so it reaches the PTY environment and nothing else.
type orchestratorLaunchPlan struct {
	launch   orchcmd.OrchestratorLaunch
	settings LaunchInput
	env      []string
}

// orchestratorResolvedLaunch is compared instead of recorded settings: a catalog
// default can move, and naming the default changes nothing.
type orchestratorResolvedLaunch struct {
	Model    string
	Effort   string
	Endpoint string
}

func resolvedLaunchOf(launch orchcmd.OrchestratorLaunch) orchestratorResolvedLaunch {
	return orchestratorResolvedLaunch{Model: launch.Model, Effort: launch.Effort, Endpoint: launch.Gateway.Endpoint()}
}

// orchestratorLaunchDiffers treats an unresolvable definition as differing.
func orchestratorLaunchDiffers(def config.OrchestratorConfig, catalog *config.OpenRouterConfig, launched orchestratorResolvedLaunch) bool {
	launch, err := orchcmd.ResolveOrchestratorLaunch(def, catalog)
	return err != nil || resolvedLaunchOf(launch) != launched
}

const orchestratorGatewayTokenEnvVar = "ANTHROPIC_AUTH_TOKEN"

// defaultOrchestratorLaunch cannot fail: a zero OrchestratorConfig always resolves.
func defaultOrchestratorLaunch() orchcmd.OrchestratorLaunch {
	launch, _ := orchcmd.ResolveOrchestratorLaunch(config.OrchestratorConfig{}, nil)
	return launch
}

func defaultOrchestratorLaunchPlan() orchestratorLaunchPlan {
	launch := defaultOrchestratorLaunch()
	return orchestratorLaunchPlan{launch: launch, env: append([]string(nil), launch.Env...)}
}

func orchestratorLaunchSettingsOf(def config.OrchestratorConfig) LaunchInput {
	return LaunchInput{
		UseGateway: def.UseGateway,
		Model:      strings.TrimSpace(def.Model),
		Effort:     strings.TrimSpace(def.Effort),
	}
}

func withOrchestratorLaunchSettings(def config.OrchestratorConfig, settings LaunchInput) config.OrchestratorConfig {
	def.UseGateway = settings.UseGateway
	def.Model = settings.Model
	def.Effort = settings.Effort
	return def
}

func orchestratorInfoWithLaunchSettings(info Info, settings LaunchInput, changed bool) Info {
	info.UseGateway = settings.UseGateway
	info.Model = settings.Model
	info.Effort = settings.Effort
	info.LaunchSettingsChanged = changed
	return info
}

// orchestratorLaunchSettingsRemedy adds the remedy that validation reasons deliberately omit.
func orchestratorLaunchSettingsRemedy(settings LaunchInput, catalog *config.OpenRouterConfig) string {
	if settings.UseGateway && !catalog.Configured() {
		return "configure the gateway catalog in the desktop's ERun settings, or turn this orchestrator's gateway off"
	}
	return "choose another model or effort for this orchestrator"
}

func (s *Service) validateOrchestratorSettings(alias string, launch LaunchInput) (string, LaunchInput, *config.OpenRouterConfig, error) {
	alias = strings.TrimSpace(alias)
	if err := orchcmd.ValidateOrchestratorAlias(s.d.Store, alias); err != nil {
		return "", LaunchInput{}, nil, fmt.Errorf("orchestrator alias: %w", err)
	}
	launch.Model, launch.Effort = clearedLaunchSetting(launch.Model), clearedLaunchSetting(launch.Effort)
	catalog := s.d.ResolveOpenRouterConfig()
	if err := orchcmd.ValidateOrchestratorLaunchSettings(withOrchestratorLaunchSettings(config.OrchestratorConfig{}, launch), catalog); err != nil {
		return "", LaunchInput{}, nil, fmt.Errorf("orchestrator launch settings: %w -- %s", err, orchestratorLaunchSettingsRemedy(launch, catalog))
	}
	return alias, launch, catalog, nil
}

// clearedLaunchSetting maps the CLI's clearing token to "", so it is never stored.
func clearedLaunchSetting(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, orchcmd.OrchestratorLaunchSettingDefault) {
		return ""
	}
	return value
}

// resolvePersistedOrchestratorLaunch runs before a running session is torn down;
// unhonourable settings refuse the start.
func (s *Service) resolvePersistedOrchestratorLaunch(def config.OrchestratorConfig) (orchestratorLaunchPlan, error) {
	settings := orchestratorLaunchSettingsOf(def)
	catalog, err := config.ResolveOpenRouterConfig(s.d.Store)
	if err != nil && settings.UseGateway && !errors.Is(err, config.ErrNotInitialized) {
		return orchestratorLaunchPlan{}, fmt.Errorf("start orchestrator %s: cannot read the gateway catalog: %w", def.ID, err)
	}
	launch, err := orchcmd.ResolveOrchestratorLaunch(def, catalog)
	if err != nil {
		return orchestratorLaunchPlan{}, fmt.Errorf("start orchestrator %s: %w -- %s", def.ID, err, orchestratorLaunchSettingsRemedy(settings, catalog))
	}
	env, err := s.orchestratorLaunchEnv(launch)
	if err != nil {
		return orchestratorLaunchPlan{}, fmt.Errorf("start orchestrator %s through the gateway: %w -- save the gateway key in the desktop's ERun settings", def.ID, err)
	}
	return orchestratorLaunchPlan{launch: launch, settings: settings, env: env}, nil
}

func (s *Service) orchestratorLaunchEnv(launch orchcmd.OrchestratorLaunch) ([]string, error) {
	env := append([]string(nil), launch.Env...)
	if launch.Gateway == nil {
		return env, nil
	}
	token, err := s.d.ResolveGatewayCredential(launch.Gateway)
	if err != nil {
		return nil, err
	}
	return append(env, orchestratorGatewayTokenEnvVar+"="+token), nil
}

func (s *Service) orchestratorLaunchSettingsChanged(def config.OrchestratorConfig, catalog *config.OpenRouterConfig) bool {
	changed := false
	s.withSessions(func(l Registry) {
		session := s.orchestrators[def.ID]
		if _, live := l.Live(def.ID); session == nil || !live {
			return
		}
		changed = orchestratorLaunchDiffers(def, catalog, session.launched)
	})
	return changed
}

func (s *Service) withConfiguredLaunchSettings(info Info) Info {
	configs, catalog, err := s.loadOrchestratorConfigsWithCatalog()
	if err != nil {
		return info
	}
	for _, def := range configs {
		if def.ID == info.ID {
			return orchestratorInfoWithLaunchSettings(info, orchestratorLaunchSettingsOf(def), s.orchestratorLaunchSettingsChanged(def, catalog))
		}
	}
	return info
}

func orchestratorLaunchChoicesFor(catalog *config.OpenRouterConfig) LaunchChoices {
	models := catalog.ModelIDs()
	if models == nil {
		models = []string{}
	}
	return LaunchChoices{
		GatewayConfigured:        catalog.Configured(),
		GatewayModels:            models,
		GatewayUndriveableModels: undriveableGatewayModels(catalog),
		GatewayDefaultModel:      catalog.ResolveDefaultModel(),
		DefaultModel:             orchcmd.DefaultOrchestratorModel,
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
