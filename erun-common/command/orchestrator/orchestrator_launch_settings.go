package orchestrator

import (
	"errors"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/open"
)

// OrchestratorLaunchSettingDefault is the CLI token that clears a model or effort, since ""
// means "no flag given".
const OrchestratorLaunchSettingDefault = "default"

func ParseOrchestratorGatewayFlag(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on":
		return true, nil
	case "off":
		return false, nil
	}
	return false, fmt.Errorf("invalid gateway %q: must be \"on\" or \"off\"", value)
}

// ParseOrchestratorModelFlag checks only that a model was given; SetOrchestratorModel validates it.
func ParseOrchestratorModelFlag(value string) (string, error) {
	return parseOrchestratorLaunchSettingFlag("model", "a model id", value)
}

func ParseOrchestratorEffortFlag(value string) (string, error) {
	return parseOrchestratorLaunchSettingFlag("effort", "one of "+strings.Join(open.ClaudeEffortLevels, ", "), value)
}

func parseOrchestratorLaunchSettingFlag(setting, expected, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, OrchestratorLaunchSettingDefault) {
		return "", nil
	}
	if trimmed == "" {
		return "", fmt.Errorf("invalid %s %q: must be %s, or %q to clear it",
			setting, value, expected, OrchestratorLaunchSettingDefault)
	}
	return trimmed, nil
}

type SetOrchestratorGatewayParams struct {
	OrchestratorID string
	UseGateway     bool
}

// SetOrchestratorModelParams carries an already-parsed Model; "" clears it.
type SetOrchestratorModelParams struct {
	OrchestratorID string
	Model          string
}

// SetOrchestratorEffortParams carries an already-parsed Effort; "" clears it.
type SetOrchestratorEffortParams struct {
	OrchestratorID string
	Effort         string
}

// SetOrchestratorGateway toggles gateway use; enabling it with no catalog is refused.
func SetOrchestratorGateway(ctx core.Context, store OrchestratorAliasStore, params SetOrchestratorGatewayParams) (configpkg.OrchestratorConfig, error) {
	return setOrchestratorLaunchSetting(ctx, store, params.OrchestratorID, orchestratorLaunchSettingWrite{
		setting: "gateway",
		usage:   "--gateway <on|off>",
		apply:   func(o *configpkg.OrchestratorConfig) { o.UseGateway = params.UseGateway },
	})
}

// SetOrchestratorModel sets the launch model, refusing one the gateway cannot drive.
func SetOrchestratorModel(ctx core.Context, store OrchestratorAliasStore, params SetOrchestratorModelParams) (configpkg.OrchestratorConfig, error) {
	model := strings.TrimSpace(params.Model)
	return setOrchestratorLaunchSetting(ctx, store, params.OrchestratorID, orchestratorLaunchSettingWrite{
		setting: "model",
		usage:   "--model <model|default>",
		apply:   func(o *configpkg.OrchestratorConfig) { o.Model = model },
	})
}

// SetOrchestratorEffort sets the effort an orchestrator launches at.
func SetOrchestratorEffort(ctx core.Context, store OrchestratorAliasStore, params SetOrchestratorEffortParams) (configpkg.OrchestratorConfig, error) {
	effort := strings.TrimSpace(params.Effort)
	return setOrchestratorLaunchSetting(ctx, store, params.OrchestratorID, orchestratorLaunchSettingWrite{
		setting: "effort",
		usage:   "--effort <level|default>",
		apply:   func(o *configpkg.OrchestratorConfig) { o.Effort = effort },
	})
}

type orchestratorLaunchSettingWrite struct {
	setting string
	usage   string
	apply   func(*configpkg.OrchestratorConfig)
}

// orchestratorLaunchSettingRemedy names the command for the failed check, not the setting written.
func orchestratorLaunchSettingRemedy(err error, orchestratorID string) string {
	var settingErr orchestratorLaunchSettingError
	if !errors.As(err, &settingErr) {
		return ""
	}
	switch settingErr.kind {
	case orchestratorLaunchSettingNoCatalog:
		return fmt.Sprintf("configure the gateway catalog in the desktop's ERun settings, or turn the gateway off (`erun orchestrator set-gateway %s --gateway off`)", orchestratorID)
	case orchestratorLaunchSettingBadEffort:
		return fmt.Sprintf("name a known level or clear it (`erun orchestrator set-effort %s --effort %s`)", orchestratorID, OrchestratorLaunchSettingDefault)
	case orchestratorLaunchSettingBadModel:
		return fmt.Sprintf("name another model or clear it (`erun orchestrator set-model %s --model %s`)", orchestratorID, OrchestratorLaunchSettingDefault)
	case orchestratorLaunchSettingUndriveableModel:
		return fmt.Sprintf("choose another model (`erun orchestrator set-model %s --model <model>`), or keep the gateway off", orchestratorID)
	}
	return ""
}

// setOrchestratorLaunchSetting validates the whole resulting orchestrator, since one
// setting can invalidate another.
func setOrchestratorLaunchSetting(ctx core.Context, store OrchestratorAliasStore, rawID string, write orchestratorLaunchSettingWrite) (configpkg.OrchestratorConfig, error) {
	if store == nil {
		return configpkg.OrchestratorConfig{}, fmt.Errorf("store is required")
	}
	orchestratorID := strings.TrimSpace(rawID)
	if orchestratorID == "" {
		return configpkg.OrchestratorConfig{}, fmt.Errorf("set orchestrator %s: no orchestrator id given — pass one explicitly (`erun orchestrator set-%s <id> %s`)",
			write.setting, write.setting, write.usage)
	}

	config, _, err := store.LoadERunConfig()
	if err != nil {
		return configpkg.OrchestratorConfig{}, err
	}
	orchestratorIndex := findOrchestratorIndex(config, orchestratorID)
	if orchestratorIndex < 0 {
		return configpkg.OrchestratorConfig{}, fmt.Errorf("orchestrator %q not found", orchestratorID)
	}

	orchestrator := config.Orchestrators[orchestratorIndex]
	write.apply(&orchestrator)
	// Validated in dry-run too, so a dry run refuses what a real run refuses.
	if err := ValidateOrchestratorLaunchSettings(orchestrator, config.OpenRouter); err != nil {
		if remedy := orchestratorLaunchSettingRemedy(err, orchestratorID); remedy != "" {
			return configpkg.OrchestratorConfig{}, fmt.Errorf("orchestrator %s: %w -- %s", orchestratorID, err, remedy)
		}
		return configpkg.OrchestratorConfig{}, fmt.Errorf("orchestrator %s: %w", orchestratorID, err)
	}
	config.Orchestrators[orchestratorIndex] = orchestrator

	if ctx.DryRun {
		ctx.Trace(fmt.Sprintf("write orchestrator %s %s", orchestratorID, write.setting))
		return orchestrator, nil
	}
	if err := store.SaveERunConfig(config); err != nil {
		return configpkg.OrchestratorConfig{}, err
	}
	return orchestrator, nil
}
