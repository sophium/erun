package orchestrator

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/orchestration"
	"github.com/sophium/erun/erun-common/primitive/open"
)

// DefaultOrchestratorModel is the model an orchestrator launches on when it
// names none and no gateway supplies a default. It matches the model an
// environment's own agent defaults to, so an orchestrator reviewing that work
// runs on the same one.
const DefaultOrchestratorModel = "opus"

// OrchestratorLaunch is how an orchestrator's Claude session launches, resolved
// from its persisted settings and the erun-level gateway catalog.
type OrchestratorLaunch struct {
	// Model is passed as `--model` and mirrored to subagents. Never empty.
	Model string
	// Effort is the resolved effort level and EffortFlags its launch flags.
	Effort      string
	EffortFlags string
	// Gateway is the catalog the session routes through, nil when it stays on
	// this machine's own Claude Code configuration. The credential is not
	// resolved here: it is a secret, and the caller delivers it.
	Gateway *config.OpenRouterConfig
	// Env is the non-secret process environment the launch needs, in a fixed
	// order so a relaunch builds the same environment.
	Env []string
}

// ResolveOrchestratorLaunch resolves orchestrator's launch against catalog.
//
// An orchestrator with no settings resolves to exactly what every orchestrator
// launched on before these settings existed: DefaultOrchestratorModel at the
// default effort, on the host's own configuration, with only the subagent model
// in its environment. A transient session (the Investigate flow) passes a zero
// OrchestratorConfig and so always launches that way.
//
// Settings that cannot be honoured are refused rather than substituted. A model
// or effort that fails validation can only come from a hand-edited config, and
// launching something other than what the operator recorded would hide that;
// a gateway turned on with no catalog behind it would otherwise launch on the
// host's own sign-in while reading as routed.
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
	// The model is spliced unquoted into a host shell line, so the resolved
	// value is checked as well as the recorded one.
	if err := orchestration.ValidateClaudeModelID(model); err != nil {
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
	// A catalog with a base URL and no driveable models still routes the
	// session; it just supplies no default, so the orchestrator's own default
	// is passed through it.
	if def := gateway.ResolveDefaultModel(); def != "" {
		return def
	}
	return DefaultOrchestratorModel
}

// orchestratorLaunchEnv mirrors what a pod's gateway env block renders, so a
// host session and an environment reach the same gateway the same way.
func orchestratorLaunchEnv(model string, gateway *config.OpenRouterConfig) []string {
	env := []string{"CLAUDE_CODE_SUBAGENT_MODEL=" + model}
	if !gateway.Configured() {
		return env
	}
	env = append(env,
		"ANTHROPIC_BASE_URL="+gateway.Endpoint(),
		// Explicitly empty rather than unset: an API key left unset lets Claude
		// Code fall back to a direct provider, bypassing the gateway.
		"ANTHROPIC_API_KEY=",
		"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1",
	)
	if context := gateway.ContextFor(model); context > 0 {
		env = append(env, "CLAUDE_CODE_MAX_CONTEXT_TOKENS="+strconv.Itoa(context))
	}
	return env
}

// ValidateOrchestratorLaunchSettings reports whether orchestrator's gateway,
// model and effort can be launched against catalog. It is the one decision
// every writer and the launch itself consult, so a value one surface accepts is
// never one another refuses.
//
// The reasons name no command: the CLI and the desktop dialog both call this,
// and each adds the remedy its own surface offers.
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
	// "default" is the CLI's token for clearing the model; stored literally it
	// would launch `--model default` while every surface shows the default.
	if strings.EqualFold(model, OrchestratorLaunchSettingDefault) {
		return orchestratorLaunchSettingError{
			kind:   orchestratorLaunchSettingBadModel,
			reason: fmt.Sprintf("invalid model %q: it is the token for clearing the model, not a model id", model),
		}
	}
	if err := orchestration.ValidateClaudeModelID(model); err != nil {
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

// orchestratorLaunchSettingError carries which check failed, so a writer can
// offer the remedy for that check rather than for the setting it was writing:
// turning the gateway on can be refused because of the recorded model.
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
