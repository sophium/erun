package orchestrator

import (
	"errors"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/open"
)

// orchestrator_launch_settings.go holds the CLI's writers for an orchestrator's
// launch settings -- OrchestratorConfig.UseGateway, .Model and .Effort -- the
// counterparts of the desktop's Edit orchestrator dialog. `erun list` reads
// them back.

// OrchestratorLaunchSettingDefault is the CLI-facing token that clears a model
// or effort back to its default. The fields' own zero value ("") is that
// default, and cannot double as a flag value that also means "no flag given".
const OrchestratorLaunchSettingDefault = "default"

// ParseOrchestratorGatewayFlag resolves `on` or `off` into
// OrchestratorConfig.UseGateway.
func ParseOrchestratorGatewayFlag(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on":
		return true, nil
	case "off":
		return false, nil
	}
	return false, fmt.Errorf("invalid gateway %q: must be \"on\" or \"off\"", value)
}

// ParseOrchestratorModelFlag resolves a CLI-facing model token into the value
// OrchestratorConfig.Model stores. It checks only that one was given; whether
// the model can be launched is decided against the catalog by
// SetOrchestratorModel, so a dry run reports the same answer a real run would.
func ParseOrchestratorModelFlag(value string) (string, error) {
	return parseOrchestratorLaunchSettingFlag("model", "a model id", value)
}

// ParseOrchestratorEffortFlag resolves a CLI-facing effort token into the value
// OrchestratorConfig.Effort stores, validated by SetOrchestratorEffort.
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

// SetOrchestratorGatewayParams identifies the orchestrator to write and whether
// it routes through the gateway catalog.
type SetOrchestratorGatewayParams struct {
	OrchestratorID string
	UseGateway     bool
}

// SetOrchestratorModelParams identifies the orchestrator to write and the model
// to record on it. Model is already parsed: "" clears it to the default.
type SetOrchestratorModelParams struct {
	OrchestratorID string
	Model          string
}

// SetOrchestratorEffortParams identifies the orchestrator to write and the
// effort to record on it. Effort is already parsed: "" clears it to the default.
type SetOrchestratorEffortParams struct {
	OrchestratorID string
	Effort         string
}

// SetOrchestratorGateway turns an orchestrator's use of the gateway catalog on
// or off. Turning it on with no catalog configured is refused: the orchestrator
// would launch on the host's own sign-in while reading as routed.
func SetOrchestratorGateway(ctx core.Context, store OrchestratorAliasStore, params SetOrchestratorGatewayParams) (configpkg.OrchestratorConfig, error) {
	return setOrchestratorLaunchSetting(ctx, store, params.OrchestratorID, orchestratorLaunchSettingWrite{
		setting: "gateway",
		usage:   "--gateway <on|off>",
		apply:   func(o *configpkg.OrchestratorConfig) { o.UseGateway = params.UseGateway },
	})
}

// SetOrchestratorModel sets the model an orchestrator launches on. A model the
// gateway cannot drive is refused when the orchestrator uses the gateway.
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

// orchestratorLaunchSettingRemedy names the command that clears the check that
// failed, which need not be the setting being written.
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

// setOrchestratorLaunchSetting validates the orchestrator as it would be after
// the write, not only the one field written: turning the gateway on can make an
// already recorded model undriveable, and that combination must be refused by
// whichever writer would create it.
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
	// Validated in dry-run too: a local config read, and a dry run that
	// accepted a value a real run refuses would report a plan it cannot carry out.
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
