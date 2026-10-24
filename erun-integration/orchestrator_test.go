package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// TestOrchestrator locks the orchestrator definition writers (set-role, set-alias, launch
// settings). set-role re-checks the role against the linked env's real type, so scenarios
// that reach that gate seed a real environment config.
func TestOrchestrator(t *testing.T) {
	t.Parallel()
	t.Run("help", testOrchestratorHelp)
	t.Run("set_role_help", testOrchestratorSetRoleHelp)
	t.Run("set_role_dry_run_traces_write", testOrchestratorSetRoleDryRunTracesWrite)
	t.Run("set_role_real_run_persists_and_is_visible_in_list", testOrchestratorSetRoleRealRunPersistsAndIsVisibleInList)
	t.Run("set_role_real_run_accepts_the_runtime_role", testOrchestratorSetRoleRealRunAcceptsTheRuntimeRole)
	t.Run("set_role_real_run_can_clear_back_to_undeclared", testOrchestratorSetRoleRealRunCanClearBackToUndeclared)
	t.Run("set_role_invalid_value_fails", testOrchestratorSetRoleInvalidValueFails)
	t.Run("set_role_missing_orchestrator_fails", testOrchestratorSetRoleMissingOrchestratorFails)
	t.Run("set_role_unlinked_environment_fails", testOrchestratorSetRoleUnlinkedEnvironmentFails)
	t.Run("set_role_refuses_role_not_allowed_for_the_linked_environments_type", testOrchestratorSetRoleRefusesRoleNotAllowedForTheLinkedEnvironmentsType)
	t.Run("set_role_refuses_clearing_role_on_a_runtime_type_environment", testOrchestratorSetRoleRefusesClearingRoleOnARuntimeTypeEnvironment)
	t.Run("set_role_real_run_accepts_the_runtime_role_on_a_runtime_type_environment", testOrchestratorSetRoleRealRunAcceptsTheRuntimeRoleOnARuntimeTypeEnvironment)
	t.Run("set_role_refuses_the_runtime_role_on_a_host_environment", testOrchestratorSetRoleRefusesTheRuntimeRoleOnAHostEnvironment)
	t.Run("set_role_real_run_accepts_a_non_runtime_role_on_a_host_environment", testOrchestratorSetRoleRealRunAcceptsANonRuntimeRoleOnAHostEnvironment)
	t.Run("set_role_recovers_a_preexisting_invalid_pairing", testOrchestratorSetRoleRecoversAPreexistingInvalidPairing)
	t.Run("set_alias_help", testOrchestratorSetAliasHelp)
	t.Run("set_alias_dry_run_traces_write", testOrchestratorSetAliasDryRunTracesWrite)
	t.Run("set_alias_real_run_persists_and_is_visible_in_list", testOrchestratorSetAliasRealRunPersistsAndIsVisibleInList)
	t.Run("set_alias_real_run_can_clear_back_to_none", testOrchestratorSetAliasRealRunCanClearBackToNone)
	t.Run("set_alias_refuses_an_alias_this_host_has_not_configured", testOrchestratorSetAliasRefusesAnAliasThisHostHasNotConfigured)
	t.Run("set_alias_refuses_a_non_erun_alias", testOrchestratorSetAliasRefusesANonErunAlias)
	t.Run("set_alias_missing_alias_flag_fails", testOrchestratorSetAliasMissingAliasFlagFails)
	t.Run("set_alias_unknown_orchestrator_fails", testOrchestratorSetAliasUnknownOrchestratorFails)
	t.Run("set_alias_refuses_when_this_host_has_no_erun_alias", testOrchestratorSetAliasRefusesWhenThisHostHasNoErunAlias)
	testOrchestratorLaunchSettings(t)
}

func testOrchestratorHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"orchestrator", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "orchestrator/help", normalize.Apply(result.Combined))
}

func testOrchestratorSetRoleHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"orchestrator", "set-role", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "orchestrator/set_role_help", normalize.Apply(result.Combined))
}

func testOrchestratorSetRoleDryRunTracesWrite(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
		{
			id:   "eng-1",
			name: "Eng One",
			environments: []orchestratorEnvSeed{
				{tenant: "team", environment: "dev", directory: "/repo/team-dev"},
			},
		},
	})
	result := erun.Run(t, []string{"orchestrator", "set-role", "eng-1", "team", "dev", "--role", "build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "orchestrator/set_role_dry_run_traces_write", normalize.Apply(result.Combined))

	raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
	if err != nil {
		t.Fatalf("read root config: %v", err)
	}
	if strings.Contains(string(raw), "role: build") {
		t.Fatalf("dry-run must not persist the role write:\n%s", raw)
	}
}

func testOrchestratorSetRoleRealRunPersistsAndIsVisibleInList(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
		{
			id:   "eng-1",
			name: "Eng One",
			environments: []orchestratorEnvSeed{
				{tenant: "team", environment: "dev", directory: "/repo/team-dev"},
				{tenant: "team", environment: "staging", directory: "/repo/team-staging"},
			},
		},
	})
	result := erun.Run(t, []string{"orchestrator", "set-role", "eng-1", "team", "dev", "--role", "build"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "orchestrator/set_role_real_run_persists_and_is_visible_in_list", normalize.Apply(result.Combined))

	raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
	if err != nil {
		t.Fatalf("read root config: %v", err)
	}
	if !strings.Contains(string(raw), "role: build") {
		t.Fatalf("expected persisted role: build, got:\n%s", raw)
	}

	listResult := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if listResult.ExitCode != 0 {
		t.Fatalf("exit %d: %s", listResult.ExitCode, listResult.Combined)
	}
	golden.Equal(t, "orchestrator/set_role_real_run_persists_and_is_visible_in_list_erun_list", normalize.Apply(listResult.Combined))
}

// team/dev is a local-agent env, which allows any role including runtime.
func testOrchestratorSetRoleRealRunAcceptsTheRuntimeRole(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
		{
			id:   "eng-1",
			name: "Eng One",
			environments: []orchestratorEnvSeed{
				{tenant: "team", environment: "dev", directory: "/repo/team-dev"},
			},
		},
	})
	result := erun.Run(t, []string{"orchestrator", "set-role", "eng-1", "team", "dev", "--role", "runtime"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "orchestrator/set_role_real_run_accepts_the_runtime_role", normalize.Apply(result.Combined))

	raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
	if err != nil {
		t.Fatalf("read root config: %v", err)
	}
	if !strings.Contains(string(raw), "role: runtime") {
		t.Fatalf("expected persisted role: runtime, got:\n%s", raw)
	}
}

func testOrchestratorSetRoleRealRunCanClearBackToUndeclared(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
		{
			id:   "eng-1",
			name: "Eng One",
			environments: []orchestratorEnvSeed{
				{tenant: "team", environment: "dev", directory: "/repo/team-dev", role: "code"},
			},
		},
	})
	result := erun.Run(t, []string{"orchestrator", "set-role", "eng-1", "team", "dev", "--role", "none"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "orchestrator/set_role_real_run_can_clear_back_to_undeclared", normalize.Apply(result.Combined))

	raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
	if err != nil {
		t.Fatalf("read root config: %v", err)
	}
	if strings.Contains(string(raw), "role:") {
		t.Fatalf("expected the role key omitted once cleared to undeclared, got:\n%s", raw)
	}
}

func testOrchestratorSetRoleInvalidValueFails(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
		{
			id:           "eng-1",
			name:         "Eng One",
			environments: []orchestratorEnvSeed{{tenant: "team", environment: "dev", directory: "/repo/team-dev"}},
		},
	})
	result := erun.Run(t, []string{"orchestrator", "set-role", "eng-1", "team", "dev", "--role", "bogus"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an invalid role, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "orchestrator/set_role_invalid_value_fails", normalize.Apply(result.Combined))
}

func testOrchestratorSetRoleMissingOrchestratorFails(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
		{
			id:           "eng-1",
			name:         "Eng One",
			environments: []orchestratorEnvSeed{{tenant: "team", environment: "dev", directory: "/repo/team-dev"}},
		},
	})
	result := erun.Run(t, []string{"orchestrator", "set-role", "ghost", "team", "dev", "--role", "code"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a missing orchestrator, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "orchestrator/set_role_missing_orchestrator_fails", normalize.Apply(result.Combined))
}

func testOrchestratorSetRoleUnlinkedEnvironmentFails(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
		{
			id:           "eng-1",
			name:         "Eng One",
			environments: []orchestratorEnvSeed{{tenant: "team", environment: "dev", directory: "/repo/team-dev"}},
		},
	})
	result := erun.Run(t, []string{"orchestrator", "set-role", "eng-1", "team", "staging", "--role", "code"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an unlinked environment, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "orchestrator/set_role_unlinked_environment_fails", normalize.Apply(result.Combined))
}

// A runtime-type env accepts only the runtime role; the refusal names the type, the role,
// and the escape hatch.
func testOrchestratorSetRoleRefusesRoleNotAllowedForTheLinkedEnvironmentsType(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnv(t, setup, "frs", "prod")
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
		{
			id:   "ops-1",
			name: "Ops One",
			environments: []orchestratorEnvSeed{
				{tenant: "frs", environment: "prod", role: "runtime"},
			},
		},
	})
	result := erun.Run(t, []string{"orchestrator", "set-role", "ops-1", "frs", "prod", "--role", "code"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an illegal role on a runtime-type environment, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "orchestrator/set_role_refuses_role_not_allowed_for_the_linked_environments_type", normalize.Apply(result.Combined))

	raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
	if err != nil {
		t.Fatalf("read root config: %v", err)
	}
	if !strings.Contains(string(raw), "role: runtime") || strings.Contains(string(raw), "role: code") {
		t.Fatalf("expected the refused write to leave the persisted role untouched, got:\n%s", raw)
	}
}

// A runtime-type env cannot be left undeclared either: "none" is not an escape for it.
func testOrchestratorSetRoleRefusesClearingRoleOnARuntimeTypeEnvironment(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnv(t, setup, "frs", "prod")
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
		{
			id:   "ops-1",
			name: "Ops One",
			environments: []orchestratorEnvSeed{
				{tenant: "frs", environment: "prod", role: "runtime"},
			},
		},
	})
	result := erun.Run(t, []string{"orchestrator", "set-role", "ops-1", "frs", "prod", "--role", "none"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for clearing the role on a runtime-type environment, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "orchestrator/set_role_refuses_clearing_role_on_a_runtime_type_environment", normalize.Apply(result.Combined))
}

func testOrchestratorSetRoleRealRunAcceptsTheRuntimeRoleOnARuntimeTypeEnvironment(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnv(t, setup, "frs", "prod")
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
		{
			id:   "ops-1",
			name: "Ops One",
			environments: []orchestratorEnvSeed{
				{tenant: "frs", environment: "prod", role: "runtime"},
			},
		},
	})
	result := erun.Run(t, []string{"orchestrator", "set-role", "ops-1", "frs", "prod", "--role", "runtime"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "orchestrator/set_role_real_run_accepts_the_runtime_role_on_a_runtime_type_environment", normalize.Apply(result.Combined))

	raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
	if err != nil {
		t.Fatalf("read root config: %v", err)
	}
	if !strings.Contains(string(raw), "role: runtime") {
		t.Fatalf("expected persisted role: runtime, got:\n%s", raw)
	}
}

// A host env has nothing for the runtime role to operate, so it is refused and the
// persisted role is untouched.
func testOrchestratorSetRoleRefusesTheRuntimeRoleOnAHostEnvironment(t *testing.T) {
	setup := env.New(t)
	fixture.SeedHostTenantEnv(t, setup, "frs", "workstation")
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
		{
			id:   "eng-1",
			name: "Eng One",
			environments: []orchestratorEnvSeed{
				{tenant: "frs", environment: "workstation", directory: "/repo/workstation", role: "build"},
			},
		},
	})
	result := erun.Run(t, []string{"orchestrator", "set-role", "eng-1", "frs", "workstation", "--role", "runtime"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for the runtime role on a host environment, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "orchestrator/set_role_refuses_the_runtime_role_on_a_host_environment", normalize.Apply(result.Combined))

	raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
	if err != nil {
		t.Fatalf("read root config: %v", err)
	}
	if !strings.Contains(string(raw), "role: build") || strings.Contains(string(raw), "role: runtime") {
		t.Fatalf("expected the refused write to leave the persisted role untouched, got:\n%s", raw)
	}
}

// Pairs with the refusal above so a gate refusing everything for host would fail.
func testOrchestratorSetRoleRealRunAcceptsANonRuntimeRoleOnAHostEnvironment(t *testing.T) {
	setup := env.New(t)
	fixture.SeedHostTenantEnv(t, setup, "frs", "workstation")
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
		{
			id:   "eng-1",
			name: "Eng One",
			environments: []orchestratorEnvSeed{
				{tenant: "frs", environment: "workstation", directory: "/repo/workstation"},
			},
		},
	})
	result := erun.Run(t, []string{"orchestrator", "set-role", "eng-1", "frs", "workstation", "--role", "code"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "orchestrator/set_role_real_run_accepts_a_non_runtime_role_on_a_host_environment", normalize.Apply(result.Combined))

	raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
	if err != nil {
		t.Fatalf("read root config: %v", err)
	}
	if !strings.Contains(string(raw), "role: code") {
		t.Fatalf("expected persisted role: code, got:\n%s", raw)
	}
}

// A pre-gate config may carry role=code on a runtime env; setting a legal role clears it.
func testOrchestratorSetRoleRecoversAPreexistingInvalidPairing(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnv(t, setup, "frs", "prod")
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
		{
			id:   "ops-1",
			name: "Ops One",
			environments: []orchestratorEnvSeed{
				{tenant: "frs", environment: "prod", role: "code"},
			},
		},
	})
	result := erun.Run(t, []string{"orchestrator", "set-role", "ops-1", "frs", "prod", "--role", "runtime"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "orchestrator/set_role_recovers_a_preexisting_invalid_pairing", normalize.Apply(result.Combined))

	raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
	if err != nil {
		t.Fatalf("read root config: %v", err)
	}
	if strings.Contains(string(raw), "role: code") || !strings.Contains(string(raw), "role: runtime") {
		t.Fatalf("expected the invalid role: code to be replaced with role: runtime, got:\n%s", raw)
	}
}

func runOrchestratorScenario(t *testing.T, setup env.Setup, args []string, succeed bool, name string) {
	t.Helper()
	result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if succeed && result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !succeed && result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "orchestrator/"+name, normalize.Apply(result.Combined))
}

func requireOrchestratorsBlockHas(t *testing.T, setup env.Setup, want string) {
	t.Helper()
	if block := orchestratorsBlock(t, setup); !strings.Contains(block, want) {
		t.Fatalf("expected %q to be persisted, got:\n%s", want, block)
	}
}

func requireOrchestratorsBlockLacks(t *testing.T, setup env.Setup, unwanted string) {
	t.Helper()
	if block := orchestratorsBlock(t, setup); strings.Contains(block, unwanted) {
		t.Fatalf("expected %q not to be persisted, got:\n%s", unwanted, block)
	}
}

// orchestratorsBlock returns the top-level "orchestrators:" block, so assertions
// ignore the `alias:` and model keys that cloudproviders and the gateway catalog also carry.
func orchestratorsBlock(t testing.TB, setup env.Setup) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
	if err != nil {
		t.Fatalf("read root config: %v", err)
	}
	idx := strings.Index(string(raw), "orchestrators:")
	if idx < 0 {
		return ""
	}
	block := string(raw)[idx:]
	lines := strings.SplitAfter(block, "\n")
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if line != "" && line[0] != ' ' && line[0] != '-' && line[0] != '\n' {
			return strings.Join(lines[:i], "")
		}
	}
	return block
}
