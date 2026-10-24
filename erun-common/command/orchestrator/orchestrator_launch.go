package orchestrator

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/open"
)

// DefaultOrchestratorModel matches an environment agent's default model.
const DefaultOrchestratorModel = "opus"

// OrchestratorLaunch is how an orchestrator's Claude session launches.
type OrchestratorLaunch struct {
	// Model is never empty.
	Model       string
	Effort      string
	EffortFlags string
	// Gateway is nil when the session uses this machine's own configuration. The secret
	// credential is delivered by the caller.
	Gateway *config.OpenRouterConfig
	// Env is the non-secret launch environment, in a fixed order.
	Env []string
}

// ResolveOrchestratorLaunch resolves orchestrator's launch against catalog. Settings that
// cannot be honoured are refused rather than substituted.
func ResolveOrchestratorLaunch(orchestrator config.OrchestratorConfig, catalog *config.OpenRouterConfig) (OrchestratorLaunch, error) {
	if err := ValidateOrchestratorLaunchSettings(orchestrator, catalog); err != nil {
		return OrchestratorLaunch{}, err
	}
	var gateway *config.OpenRouterConfig
	if orchestrator.UseGateway {
		gateway = catalog
	}
	effort := strings.TrimSpace(orchestrator.Effort)
	if effort == "" {
		effort = open.DefaultClaudeEffort
	}
	model := resolveOrchestratorModel(strings.TrimSpace(orchestrator.Model), gateway)
	// The model is spliced unquoted into a shell line, so the resolved value is checked too.
	if err := ValidateClaudeModelID(model); err != nil {
		return OrchestratorLaunch{}, orchestratorLaunchSettingError{kind: orchestratorLaunchSettingBadModel, reason: err.Error()}
	}
	return OrchestratorLaunch{
		Model:       model,
		Effort:      effort,
		EffortFlags: open.ClaudeEffortFlags(effort),
		Gateway:     gateway,
		Env:         orchestratorLaunchEnv(model, gateway),
	}, nil
}

func resolveOrchestratorModel(model string, gateway *config.OpenRouterConfig) string {
	if model != "" {
		return model
	}
	// A catalog with no driveable models still routes; the orchestrator's default passes through.
	if def := gateway.ResolveDefaultModel(); def != "" {
		return def
	}
	return DefaultOrchestratorModel
}

// orchestratorLaunchEnv mirrors a pod's gateway env block.
func orchestratorLaunchEnv(model string, gateway *config.OpenRouterConfig) []string {
	env := []string{"CLAUDE_CODE_SUBAGENT_MODEL=" + model}
	if !gateway.Configured() {
		return env
	}
	env = append(env,
		"ANTHROPIC_BASE_URL="+gateway.Endpoint(),
		// Empty, not unset: an unset key lets Claude Code bypass the gateway.
		"ANTHROPIC_API_KEY=",
		"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1",
	)
	if context := gateway.ContextFor(model); context > 0 {
		env = append(env, "CLAUDE_CODE_MAX_CONTEXT_TOKENS="+strconv.Itoa(context))
	}
	return env
}

// ValidateOrchestratorLaunchSettings is the one check every writer and the launch consult.
// Reasons name no command; each surface adds its own remedy.
func ValidateOrchestratorLaunchSettings(orchestrator config.OrchestratorConfig, catalog *config.OpenRouterConfig) error {
	if orchestrator.UseGateway && !catalog.Configured() {
		return orchestratorLaunchSettingError{
			kind:   orchestratorLaunchSettingNoCatalog,
			reason: "the gateway is turned on but no gateway catalog is configured",
		}
	}
	if effort := strings.TrimSpace(orchestrator.Effort); effort != "" && !open.ValidClaudeEffort(effort) {
		return orchestratorLaunchSettingError{
			kind:   orchestratorLaunchSettingBadEffort,
			reason: fmt.Sprintf("invalid effort %q: must be one of %s", effort, strings.Join(open.ClaudeEffortLevels, ", ")),
		}
	}
	model := strings.TrimSpace(orchestrator.Model)
	if model == "" {
		return nil
	}
	if strings.EqualFold(model, OrchestratorLaunchSettingDefault) {
		return orchestratorLaunchSettingError{
			kind:   orchestratorLaunchSettingBadModel,
			reason: fmt.Sprintf("invalid model %q: it is the token for clearing the model, not a model id", model),
		}
	}
	if err := ValidateClaudeModelID(model); err != nil {
		return orchestratorLaunchSettingError{kind: orchestratorLaunchSettingBadModel, reason: err.Error()}
	}
	if orchestrator.UseGateway && catalog.RequiresReasoningEcho(model) {
		return orchestratorLaunchSettingError{
			kind:   orchestratorLaunchSettingUndriveableModel,
			reason: fmt.Sprintf("model %q cannot be driven through the gateway: its provider requires its own reasoning echoed back", model),
		}
	}
	return nil
}

// orchestratorLaunchSettingError names the failed check, so a writer offers the right remedy.
type orchestratorLaunchSettingError struct {
	kind   orchestratorLaunchSettingFailure
	reason string
}

func (e orchestratorLaunchSettingError) Error() string { return e.reason }

type orchestratorLaunchSettingFailure int

const (
	orchestratorLaunchSettingNoCatalog orchestratorLaunchSettingFailure = iota
	orchestratorLaunchSettingBadEffort
	orchestratorLaunchSettingBadModel
	orchestratorLaunchSettingUndriveableModel
)
