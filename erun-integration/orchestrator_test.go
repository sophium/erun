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

// TestOrchestrator locks OrchestratorEnvConfig.Role's CLI writer: `erun
// orchestrator set-role` is that writer's dry-run trace, real-run
// persistence, and error paths -- reusing list_test.go's
// seedOrchestratorsWithEnvRoles so both commands seed and read the same
// on-disk shape. The desktop dialog's own writer is covered by
// erun-ui/orchestrator_test.go and erun-ui/playwright/tests/
// orchestrator-env-role.spec.ts; this suite has no Wails runtime to drive it
// from.
//
// set-role also re-checks the requested role against the linked
// environment's real type (eruncommon.OrchestratorEnvRoleAllowed), the same
// gate the desktop's link/edit dialog enforces, so every scenario below that
// reaches the gate seeds a real environment config via
// fixture.SeedTenantEnv/SeedRuntimeTenantEnv rather than only the
// orchestrator's own link entry. There is no MCP tool for this operation
// (grep erun-mcp for "orchestrator" or "set-role" -- neither exists), so
// there is no second transport to keep in sync here.
func TestOrchestrator(t *testing.T) {
	t.Parallel()
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"orchestrator", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "orchestrator/help", normalize.Apply(result.Combined))
	})

	t.Run("set_role_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"orchestrator", "set-role", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "orchestrator/set_role_help", normalize.Apply(result.Combined))
	})

	t.Run("set_role_dry_run_traces_write", func(t *testing.T) {
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
	})

	t.Run("set_role_real_run_persists_and_is_visible_in_list", func(t *testing.T) {
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
	})

	// set_role_real_run_accepts_the_runtime_role locks the third role value
	// on this writer: "runtime" parses and persists exactly like "code" and
	// "build" above. team/dev is a local-agent environment, and
	// OrchestratorEnvRoleAllowed permits any role -- including runtime -- for
	// that type, so the environment-type gate does not block this.
	t.Run("set_role_real_run_accepts_the_runtime_role", func(t *testing.T) {
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
	})

	t.Run("set_role_real_run_can_clear_back_to_undeclared", func(t *testing.T) {
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
	})

	t.Run("set_role_invalid_value_fails", func(t *testing.T) {
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
	})

	t.Run("set_role_missing_orchestrator_fails", func(t *testing.T) {
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
	})

	t.Run("set_role_unlinked_environment_fails", func(t *testing.T) {
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
	})

	// set_role_refuses_role_not_allowed_for_the_linked_environments_type locks
	// the environment-type gate on this writer: a runtime-type environment
	// only ever accepts the runtime role (eruncommon.OrchestratorEnvRoleAllowed),
	// the same gate the desktop's link/edit dialog enforces, and set-role now
	// refuses to walk past it on an already-linked entry. The refusal names
	// the environment's type, the role that was requested, and the escape
	// hatch (set it to runtime instead), the same information the desktop's
	// link-time refusal gives.
	t.Run("set_role_refuses_role_not_allowed_for_the_linked_environments_type", func(t *testing.T) {
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
	})

	// set_role_refuses_clearing_role_on_a_runtime_type_environment locks the
	// deliberate exception to "clearing must always work": a runtime-type
	// environment has no worktree to review and no in-pod agent to delegate
	// to, so undeclared is exactly as illegal for it as code or build --
	// eruncommon.OrchestratorEnvRoleAllowed(runtime, "") is false, the same as
	// for any other non-runtime role. "none" is only ever a clear escape for
	// a type that has one to begin with.
	t.Run("set_role_refuses_clearing_role_on_a_runtime_type_environment", func(t *testing.T) {
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
	})

	// set_role_real_run_accepts_the_runtime_role_on_a_runtime_type_environment
	// proves the one legal pairing a runtime-type environment has actually
	// still sets -- the gate blocks every *other* role, not this one.
	t.Run("set_role_real_run_accepts_the_runtime_role_on_a_runtime_type_environment", func(t *testing.T) {
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
	})

	// set_role_recovers_a_preexisting_invalid_pairing locks the decision for
	// a config written before this gate existed: role=code was persisted
	// against a runtime-type environment (something the desktop's link gate
	// would refuse to create today, but an older config can still carry).
	// The gate does not special-case "already invalid" -- it checks the
	// requested role the same way regardless of what was there before -- so
	// the fix is simply that setting a legal role succeeds and clears the
	// bad pairing, with no separate recovery path needed.
	t.Run("set_role_recovers_a_preexisting_invalid_pairing", func(t *testing.T) {
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
	})
}
