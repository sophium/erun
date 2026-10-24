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

// TestOrchestrator locks the CLI writers for orchestrator definitions:
// OrchestratorEnvConfig.Role (`erun orchestrator set-role`), the platform
// alias (`set-alias`) and the launch settings (`set-gateway`, `set-model`,
// `set-effort`, see testOrchestratorLaunchSettings). The set-role scenarios
// cover that writer's dry-run trace, real-run persistence, and error paths,
// reusing list_test.go's seedOrchestratorsWithEnvRoles so both commands seed
// and read the same on-disk shape. The desktop dialog's own writer is covered by
// erun-ui/orchestrator_test.go and erun-ui/playwright/tests/
// orchestrator-env-role.spec.ts; this suite has no Wails runtime to drive it
// from.
//
// set-role also re-checks the requested role against the linked
// environment's real type (config.OrchestratorEnvRoleAllowed), the same
// gate the desktop's link/edit dialog enforces, so every scenario below that
// reaches the gate seeds a real environment config via
// fixture.SeedTenantEnv/SeedRuntimeTenantEnv rather than only the
// orchestrator's own link entry. There is no MCP tool for this operation
// (grep erun-mcp for "orchestrator" or "set-role" -- neither exists), and none
// for any other writer here, so there is no second transport to keep in sync.
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

// set_role_real_run_accepts_the_runtime_role locks the third role value
// on this writer: "runtime" parses and persists exactly like "code" and
// "build" above. team/dev is a local-agent environment, and
// OrchestratorEnvRoleAllowed permits any role -- including runtime -- for
// that type, so the environment-type gate does not block this.
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

// set_role_refuses_role_not_allowed_for_the_linked_environments_type locks
// the environment-type gate on this writer: a runtime-type environment
// only ever accepts the runtime role (config.OrchestratorEnvRoleAllowed),
// the same gate the desktop's link/edit dialog enforces, and set-role
// refuses to walk past it on an already-linked entry. The refusal names
// the environment's type, the role that was requested, and the escape
// hatch (set it to runtime instead), the same information the desktop's
// link-time refusal gives.
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

// set_role_refuses_clearing_role_on_a_runtime_type_environment locks the
// deliberate exception to "clearing must always work": a runtime-type
// environment has no worktree to review and no in-pod agent to delegate
// to, so undeclared is exactly as illegal for it as code or build --
// config.OrchestratorEnvRoleAllowed(runtime, "") is false, the same as
// for any other non-runtime role. "none" is only ever a clear escape for
// a type that has one to begin with.
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

// set_role_real_run_accepts_the_runtime_role_on_a_runtime_type_environment
// proves the one legal pairing a runtime-type environment has actually
// still sets -- the gate blocks every *other* role, not this one.
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

// set_role_refuses_the_runtime_role_on_a_host_environment is this gate's
// other refused-by-type case, and the mirror image of the runtime pair
// above: a host environment is a directory on this machine with no pod and
// no cluster at all, so the runtime role -- which means the orchestrator
// operates the environment directly, deploy, pin, observe -- has nothing to
// act on, and every one of those verbs refuses a host env outright
// (config.OrchestratorEnvRoleAllowed(host, runtime) is false). Offering
// it would promise a relationship the link cannot deliver and the mismatch
// would surface only later, in the orchestrator session, as an environment
// tool that never works. The refusal names the type and the escape hatch
// the same way the runtime-type refusal does, and the persisted role is
// left untouched.
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

// set_role_real_run_accepts_a_non_runtime_role_on_a_host_environment proves
// the refusal above is specific to the runtime role rather than a blanket
// "host environments cannot be linked": a host env is a working link that
// an orchestrator reviews in place, so every other role -- including
// undeclared -- is legal for it, and the write actually lands. Without this
// paired with the refusal, a gate that refused *everything* for host would
// satisfy the refusal test and still be wrong.
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

// set_role_recovers_a_preexisting_invalid_pairing locks the decision for
// a config written before this gate existed: role=code persisted against a
// runtime-type environment, which the desktop's link gate refuses to create
// but an older config can still carry.
// The gate does not special-case "already invalid" -- it checks the
// requested role the same way regardless of what was there before -- so
// setting a legal role succeeds and clears the bad pairing, with no separate recovery path needed.
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

// runOrchestratorScenario runs args, requires the expected outcome, and
// compares the normalized output against testdata/orchestrator/<name>.txt.
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

// orchestratorsBlock returns the root config's top-level "orchestrators:"
// block, up to the next top-level key.
//
// The writers' scenarios assert on what was persisted, and a whole-file
// substring check cannot tell those assertions apart from other blocks'
// keys: every set-alias scenario seeds *cloudproviders* entries carrying an
// `alias:` key of their own, and the gateway catalog the launch-setting
// scenarios seed carries `defaultmodel:` and `models:`, which a save writes
// back after the orchestrators. Reading only the orchestrators block is what
// makes the assertion about the write.
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
