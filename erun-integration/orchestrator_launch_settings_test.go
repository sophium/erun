package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
)

// testOrchestratorLaunchSettings covers the launch-setting writers --
// `erun orchestrator set-gateway`, `set-model` and `set-effort`, the CLI
// counterparts of the desktop dialog's launch controls for
// OrchestratorConfig.UseGateway, .Model and .Effort. Every refusal here is
// decided by orchestrator.ValidateOrchestratorLaunchSettings against the
// orchestrator as it would be after the write, so the gateway and model
// scenarios cross each other: a model refused only because the gateway is on,
// and a gateway refused only because of the model already recorded.
func testOrchestratorLaunchSettings(t *testing.T) {
	t.Run("set_gateway_help", testOrchestratorSetGatewayHelp)
	t.Run("set_model_help", testOrchestratorSetModelHelp)
	t.Run("set_effort_help", testOrchestratorSetEffortHelp)
	t.Run("set_gateway_dry_run_traces_write", testOrchestratorSetGatewayDryRunTracesWrite)
	t.Run("set_model_dry_run_traces_write", testOrchestratorSetModelDryRunTracesWrite)
	t.Run("set_effort_dry_run_traces_write", testOrchestratorSetEffortDryRunTracesWrite)
	t.Run("set_gateway_real_run_persists_and_is_visible_in_list", testOrchestratorSetGatewayRealRunPersistsAndIsVisibleInList)
	t.Run("set_model_real_run_persists_a_catalog_model_through_the_gateway", testOrchestratorSetModelRealRunPersistsACatalogModelThroughTheGateway)
	t.Run("set_effort_real_run_persists_and_is_visible_in_list", testOrchestratorSetEffortRealRunPersistsAndIsVisibleInList)
	t.Run("set_gateway_real_run_can_turn_it_off", testOrchestratorSetGatewayRealRunCanTurnItOff)
	t.Run("set_model_real_run_can_clear_back_to_default", testOrchestratorSetModelRealRunCanClearBackToDefault)
	t.Run("set_effort_real_run_can_clear_back_to_default", testOrchestratorSetEffortRealRunCanClearBackToDefault)
	t.Run("set_gateway_missing_gateway_flag_fails", testOrchestratorSetGatewayMissingGatewayFlagFails)
	t.Run("set_model_missing_model_flag_fails", testOrchestratorSetModelMissingModelFlagFails)
	t.Run("set_effort_missing_effort_flag_fails", testOrchestratorSetEffortMissingEffortFlagFails)
	t.Run("set_gateway_unknown_orchestrator_fails", testOrchestratorSetGatewayUnknownOrchestratorFails)
	t.Run("set_model_unknown_orchestrator_fails", testOrchestratorSetModelUnknownOrchestratorFails)
	t.Run("set_effort_unknown_orchestrator_fails", testOrchestratorSetEffortUnknownOrchestratorFails)
	t.Run("set_model_blank_orchestrator_id_fails", testOrchestratorSetModelBlankOrchestratorIdFails)
	t.Run("set_gateway_invalid_value_fails", testOrchestratorSetGatewayInvalidValueFails)
	t.Run("set_model_empty_value_fails", testOrchestratorSetModelEmptyValueFails)
	t.Run("set_effort_refuses_an_unknown_level", testOrchestratorSetEffortRefusesAnUnknownLevel)
	t.Run("set_gateway_on_refused_without_a_catalog", testOrchestratorSetGatewayOnRefusedWithoutACatalog)
	t.Run("set_model_refuses_a_leading_dash", testOrchestratorSetModelRefusesALeadingDash)
	t.Run("set_model_refuses_a_reasoning_echo_model_when_the_gateway_is_on", testOrchestratorSetModelRefusesAReasoningEchoModelWhenTheGatewayIsOn)
	t.Run("set_gateway_on_refused_when_the_recorded_model_needs_reasoning_echo", testOrchestratorSetGatewayOnRefusedWhenTheRecordedModelNeedsReasoningEcho)
}

func testOrchestratorSetGatewayHelp(t *testing.T) {
	setup := env.New(t)
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-gateway", "--help"}, true, "set_gateway_help")
}

func testOrchestratorSetModelHelp(t *testing.T) {
	setup := env.New(t)
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", "--help"}, true, "set_model_help")
}

func testOrchestratorSetEffortHelp(t *testing.T) {
	setup := env.New(t)
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-effort", "--help"}, true, "set_effort_help")
}

// The dry runs validate exactly as a real run does (the catalog is seeded so
// the gateway write is accepted) and trace the write without making it.
func testOrchestratorSetGatewayDryRunTracesWrite(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
	seedGatewayCatalog(t, setup, defaultGatewayCatalogSeed())
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-gateway", "eng-1", "--gateway", "on", "--dry-run"}, true, "set_gateway_dry_run_traces_write")
	requireOrchestratorsBlockLacks(t, setup, "usegateway:")
}

func testOrchestratorSetModelDryRunTracesWrite(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", "eng-1", "--model", "sonnet", "--dry-run"}, true, "set_model_dry_run_traces_write")
	requireOrchestratorsBlockLacks(t, setup, "    model:")
}

func testOrchestratorSetEffortDryRunTracesWrite(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-effort", "eng-1", "--effort", "high", "--dry-run"}, true, "set_effort_dry_run_traces_write")
	requireOrchestratorsBlockLacks(t, setup, "effort:")
}

// The real runs persist the setting and `erun list` reads it back, which is
// what makes each command a writer with a reader rather than a field written
// into a file nothing shows.
func testOrchestratorSetGatewayRealRunPersistsAndIsVisibleInList(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
	seedGatewayCatalog(t, setup, defaultGatewayCatalogSeed())
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-gateway", "eng-1", "--gateway", "on"}, true, "set_gateway_real_run_persists_and_is_visible_in_list")
	requireOrchestratorsBlockHas(t, setup, "usegateway: true")
	runOrchestratorScenario(t, setup, []string{"list"}, true, "set_gateway_real_run_persists_and_is_visible_in_list_erun_list")
}

// set_model_real_run_persists_a_catalog_model_through_the_gateway records a
// model the gateway can drive on an orchestrator already routed through it,
// the accepted side of the reasoning-echo refusal below.
func testOrchestratorSetModelRealRunPersistsACatalogModelThroughTheGateway(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One", useGateway: true}})
	seedGatewayCatalog(t, setup, defaultGatewayCatalogSeed())
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", "eng-1", "--model", "deepseek/deepseek-v4-pro-0813"}, true, "set_model_real_run_persists_a_catalog_model_through_the_gateway")
	requireOrchestratorsBlockHas(t, setup, "model: deepseek/deepseek-v4-pro-0813")
	runOrchestratorScenario(t, setup, []string{"list"}, true, "set_model_real_run_persists_a_catalog_model_through_the_gateway_erun_list")
}

func testOrchestratorSetEffortRealRunPersistsAndIsVisibleInList(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-effort", "eng-1", "--effort", "high"}, true, "set_effort_real_run_persists_and_is_visible_in_list")
	requireOrchestratorsBlockHas(t, setup, "effort: high")
	runOrchestratorScenario(t, setup, []string{"list"}, true, "set_effort_real_run_persists_and_is_visible_in_list_erun_list")
}

// Clearing writes the zero value, which omitempty drops: the key is gone
// rather than recorded as an explicit default. Turning the gateway off needs
// no catalog, so none is seeded.
func testOrchestratorSetGatewayRealRunCanTurnItOff(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One", useGateway: true}})
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-gateway", "eng-1", "--gateway", "off"}, true, "set_gateway_real_run_can_turn_it_off")
	requireOrchestratorsBlockLacks(t, setup, "usegateway:")
}

func testOrchestratorSetModelRealRunCanClearBackToDefault(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One", model: "sonnet"}})
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", "eng-1", "--model", "default"}, true, "set_model_real_run_can_clear_back_to_default")
	requireOrchestratorsBlockLacks(t, setup, "    model:")
}

func testOrchestratorSetEffortRealRunCanClearBackToDefault(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One", effort: "high"}})
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-effort", "eng-1", "--effort", "default"}, true, "set_effort_real_run_can_clear_back_to_default")
	requireOrchestratorsBlockLacks(t, setup, "effort:")
}

func testOrchestratorSetGatewayMissingGatewayFlagFails(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-gateway", "eng-1"}, false, "set_gateway_missing_gateway_flag_fails")
}

func testOrchestratorSetModelMissingModelFlagFails(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", "eng-1"}, false, "set_model_missing_model_flag_fails")
}

func testOrchestratorSetEffortMissingEffortFlagFails(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-effort", "eng-1"}, false, "set_effort_missing_effort_flag_fails")
}

func testOrchestratorSetGatewayUnknownOrchestratorFails(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-gateway", "nope", "--gateway", "off"}, false, "set_gateway_unknown_orchestrator_fails")
}

func testOrchestratorSetModelUnknownOrchestratorFails(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", "nope", "--model", "sonnet"}, false, "set_model_unknown_orchestrator_fails")
}

func testOrchestratorSetEffortUnknownOrchestratorFails(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-effort", "nope", "--effort", "high"}, false, "set_effort_unknown_orchestrator_fails")
}

// A blank id reaches the shared writer (cobra counts it as the one
// argument), whose refusal names the command to rerun.
func testOrchestratorSetModelBlankOrchestratorIdFails(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", " ", "--model", "sonnet"}, false, "set_model_blank_orchestrator_id_fails")
}

func testOrchestratorSetGatewayInvalidValueFails(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-gateway", "eng-1", "--gateway", "maybe"}, false, "set_gateway_invalid_value_fails")
}

// An empty --model is neither a model nor the clearing token; it is refused
// at parse time rather than read as "clear".
func testOrchestratorSetModelEmptyValueFails(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One", model: "sonnet"}})
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", "eng-1", "--model", ""}, false, "set_model_empty_value_fails")
	requireOrchestratorsBlockHas(t, setup, "model: sonnet")
}

func testOrchestratorSetEffortRefusesAnUnknownLevel(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-effort", "eng-1", "--effort", "extreme"}, false, "set_effort_refuses_an_unknown_level")
	requireOrchestratorsBlockLacks(t, setup, "effort:")
}

// set_gateway_on_refused_without_a_catalog: with nothing to route through,
// the orchestrator would launch on this machine's own sign-in while reading
// as routed. The refusal names where the catalog is configured.
func testOrchestratorSetGatewayOnRefusedWithoutACatalog(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-gateway", "eng-1", "--gateway", "on"}, false, "set_gateway_on_refused_without_a_catalog")
	requireOrchestratorsBlockLacks(t, setup, "usegateway:")
}

// A model is passed to `claude --model` on the launch line, so a value that
// starts with '-' would be read as a flag. Passed with '=' so cobra hands it
// to --model rather than parsing it as a flag of its own.
func testOrchestratorSetModelRefusesALeadingDash(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", "eng-1", "--model=--dangerously-skip-permissions"}, false, "set_model_refuses_a_leading_dash")
	requireOrchestratorsBlockLacks(t, setup, "    model:")
}

// The reasoning-echo pair. The model is refused because the gateway is on;
// the gateway is refused because of the model already recorded. Either way
// the combination the gateway cannot drive is never written.
func testOrchestratorSetModelRefusesAReasoningEchoModelWhenTheGatewayIsOn(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One", useGateway: true}})
	seedGatewayCatalog(t, setup, defaultGatewayCatalogSeed())
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", "eng-1", "--model", "deepseek/deepseek-v4.1-flash"}, false, "set_model_refuses_a_reasoning_echo_model_when_the_gateway_is_on")
	requireOrchestratorsBlockLacks(t, setup, "    model:")
}

func testOrchestratorSetGatewayOnRefusedWhenTheRecordedModelNeedsReasoningEcho(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One", model: "deepseek/deepseek-v4.1-flash"}})
	seedGatewayCatalog(t, setup, defaultGatewayCatalogSeed())
	runOrchestratorScenario(t, setup, []string{"orchestrator", "set-gateway", "eng-1", "--gateway", "on"}, false, "set_gateway_on_refused_when_the_recorded_model_needs_reasoning_echo")
	requireOrchestratorsBlockLacks(t, setup, "usegateway:")
}
