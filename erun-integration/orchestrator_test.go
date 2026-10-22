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
// environment's real type (eruncommon.OrchestratorEnvRoleAllowed), the same
// gate the desktop's link/edit dialog enforces, so every scenario below that
// reaches the gate seeds a real environment config via
// fixture.SeedTenantEnv/SeedRuntimeTenantEnv rather than only the
// orchestrator's own link entry. There is no MCP tool for this operation
// (grep erun-mcp for "orchestrator" or "set-role" -- neither exists), and none
// for any other writer here, so there is no second transport to keep in sync.
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

	// set_role_refuses_the_runtime_role_on_a_host_environment is this gate's
	// other refused-by-type case, and the mirror image of the runtime pair
	// above: a host environment is a directory on this machine with no pod and
	// no cluster at all, so the runtime role -- which means the orchestrator
	// operates the environment directly, deploy, pin, observe -- has nothing to
	// act on, and every one of those verbs refuses a host env outright
	// (eruncommon.OrchestratorEnvRoleAllowed(host, runtime) is false). Offering
	// it would promise a relationship the link cannot deliver and the mismatch
	// would surface only later, in the orchestrator session, as an environment
	// tool that never works. The refusal names the type and the escape hatch
	// the same way the runtime-type refusal does, and the persisted role is
	// left untouched.
	t.Run("set_role_refuses_the_runtime_role_on_a_host_environment", func(t *testing.T) {
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
	})

	// set_role_real_run_accepts_a_non_runtime_role_on_a_host_environment proves
	// the refusal above is specific to the runtime role rather than a blanket
	// "host environments cannot be linked": a host env is a working link that
	// an orchestrator reviews in place, so every other role -- including
	// undeclared -- is legal for it, and the write actually lands. Without this
	// paired with the refusal, a gate that refused *everything* for host would
	// satisfy the refusal test and still be wrong.
	t.Run("set_role_real_run_accepts_a_non_runtime_role_on_a_host_environment", func(t *testing.T) {
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

	// The set-alias half. `erun orchestrator set-alias` is the CLI writer for
	// OrchestratorConfig.Alias, the field that names which erun platform alias
	// an orchestrator declares as its own -- the piece erun-common's
	// operator-settable-field registry exists to keep from shipping with a
	// reader and no writer. Every scenario that expects a write to land seeds a
	// configured erun-type alias first, because the writer validates against
	// this host's own cloudproviders and refuses one it cannot resolve.
	t.Run("set_alias_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"orchestrator", "set-alias", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "orchestrator/set_alias_help", normalize.Apply(result.Combined))
	})

	t.Run("set_alias_dry_run_traces_write", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+api.example.test@erun", "https://api.example.test", "cli-test-client")
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
			{id: "eng-1", name: "Eng One"},
		})
		result := erun.Run(t, []string{"orchestrator", "set-alias", "eng-1", "--alias", "erun+api.example.test@erun", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "orchestrator/set_alias_dry_run_traces_write", normalize.Apply(result.Combined))

		if block := orchestratorsBlock(t, setup); strings.Contains(block, "alias:") {
			t.Fatalf("dry-run must not persist the alias write:\n%s", block)
		}
	})

	t.Run("set_alias_real_run_persists_and_is_visible_in_list", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+api.example.test@erun", "https://api.example.test", "cli-test-client")
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
			{id: "eng-1", name: "Eng One", directories: []string{"/repo/scratch"}},
		})
		result := erun.Run(t, []string{"orchestrator", "set-alias", "eng-1", "--alias", "erun+api.example.test@erun"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "orchestrator/set_alias_real_run_persists_and_is_visible_in_list", normalize.Apply(result.Combined))

		if block := orchestratorsBlock(t, setup); !strings.Contains(block, "alias: erun+api.example.test@erun") {
			t.Fatalf("expected the alias to be persisted, got:\n%s", block)
		}

		// The reader half: the alias reaches `erun list`, which is what makes
		// this a reader and a writer rather than a field written into a file
		// nothing shows.
		listResult := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if listResult.ExitCode != 0 {
			t.Fatalf("exit %d: %s", listResult.ExitCode, listResult.Combined)
		}
		golden.Equal(t, "orchestrator/set_alias_real_run_persists_and_is_visible_in_list_erun_list", normalize.Apply(listResult.Combined))
	})

	t.Run("set_alias_real_run_can_clear_back_to_none", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+api.example.test@erun", "https://api.example.test", "cli-test-client")
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
			{id: "eng-1", name: "Eng One", alias: "erun+api.example.test@erun"},
		})
		result := erun.Run(t, []string{"orchestrator", "set-alias", "eng-1", "--alias", "none"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "orchestrator/set_alias_real_run_can_clear_back_to_none", normalize.Apply(result.Combined))

		if block := orchestratorsBlock(t, setup); strings.Contains(block, "alias:") {
			t.Fatalf("expected the alias key to be gone, got:\n%s", block)
		}
	})

	// set_alias_refuses_an_alias_this_host_has_not_configured is the writer's
	// whole reason for validating rather than storing the string: an alias
	// nothing resolves is indistinguishable from one that was never set, so the
	// operator would have declared an attribution that silently does not exist.
	// The refusal names the command that configures one, so it is a next action
	// rather than a wall.
	t.Run("set_alias_refuses_an_alias_this_host_has_not_configured", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+api.example.test@erun", "https://api.example.test", "cli-test-client")
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
			{id: "eng-1", name: "Eng One"},
		})
		result := erun.Run(t, []string{"orchestrator", "set-alias", "eng-1", "--alias", "erun+elsewhere.example@erun"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an unresolvable alias, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "orchestrator/set_alias_refuses_an_alias_this_host_has_not_configured", normalize.Apply(result.Combined))

		if block := orchestratorsBlock(t, setup); strings.Contains(block, "alias:") {
			t.Fatalf("a refused alias must not be persisted, got:\n%s", block)
		}
	})

	// set_alias_refuses_a_non_erun_alias keeps the field to the one provider
	// type it describes. An orchestrator's platform attribution has no meaning
	// against an AWS or Cloudflare alias, and storing one would promise a
	// resolution that can never happen.
	t.Run("set_alias_refuses_a_non_erun_alias", func(t *testing.T) {
		setup := env.New(t)
		root := filepath.Join(setup.ConfigHome, "erun")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", root, err)
		}
		mustWrite(t, filepath.Join(root, "config.yaml"),
			"cloudproviders:\n"+
				"  - alias: aws-prod\n"+
				"    provider: aws\n"+
				"    username: test-user\n"+
				"    accountid: \"123456789012\"\n"+
				"    profile: test-profile\n")
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
			{id: "eng-1", name: "Eng One"},
		})
		result := erun.Run(t, []string{"orchestrator", "set-alias", "eng-1", "--alias", "aws-prod"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a non-erun alias, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "orchestrator/set_alias_refuses_a_non_erun_alias", normalize.Apply(result.Combined))
	})

	t.Run("set_alias_missing_alias_flag_fails", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
			{id: "eng-1", name: "Eng One"},
		})
		result := erun.Run(t, []string{"orchestrator", "set-alias", "eng-1"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when --alias is omitted, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "orchestrator/set_alias_missing_alias_flag_fails", normalize.Apply(result.Combined))
	})

	t.Run("set_alias_unknown_orchestrator_fails", func(t *testing.T) {
		setup := env.New(t)
		seedERunCloudProviderAlias(t, setup, "erun+api.example.test@erun", "https://api.example.test", "cli-test-client")
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
			{id: "eng-1", name: "Eng One"},
		})
		result := erun.Run(t, []string{"orchestrator", "set-alias", "nope", "--alias", "erun+api.example.test@erun"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an unknown orchestrator, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "orchestrator/set_alias_unknown_orchestrator_fails", normalize.Apply(result.Combined))
	})

	// set_alias_is_not_an_alias_creator locks the boundary the command's own
	// help states: it selects among aliases this host already has and never
	// creates or signs in to one. A host with no erun alias at all is the case
	// that would otherwise be indistinguishable from a typo.
	t.Run("set_alias_refuses_when_this_host_has_no_erun_alias", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
			{id: "eng-1", name: "Eng One"},
		})
		result := erun.Run(t, []string{"orchestrator", "set-alias", "eng-1", "--alias", "erun+api.example.test@erun"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit with no erun alias configured, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "orchestrator/set_alias_refuses_when_this_host_has_no_erun_alias", normalize.Apply(result.Combined))
	})

	testOrchestratorLaunchSettings(t)
}

// testOrchestratorLaunchSettings covers the launch-setting writers --
// `erun orchestrator set-gateway`, `set-model` and `set-effort`, the CLI
// counterparts of the desktop dialog's launch controls for
// OrchestratorConfig.UseGateway, .Model and .Effort. Every refusal here is
// decided by eruncommon.ValidateOrchestratorLaunchSettings against the
// orchestrator as it would be after the write, so the gateway and model
// scenarios cross each other: a model refused only because the gateway is on,
// and a gateway refused only because of the model already recorded.
func testOrchestratorLaunchSettings(t *testing.T) {
	t.Run("set_gateway_help", func(t *testing.T) {
		setup := env.New(t)
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-gateway", "--help"}, true, "set_gateway_help")
	})

	t.Run("set_model_help", func(t *testing.T) {
		setup := env.New(t)
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", "--help"}, true, "set_model_help")
	})

	t.Run("set_effort_help", func(t *testing.T) {
		setup := env.New(t)
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-effort", "--help"}, true, "set_effort_help")
	})

	// The dry runs validate exactly as a real run does (the catalog is seeded so
	// the gateway write is accepted) and trace the write without making it.
	t.Run("set_gateway_dry_run_traces_write", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
		seedGatewayCatalog(t, setup, defaultGatewayCatalogSeed())
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-gateway", "eng-1", "--gateway", "on", "--dry-run"}, true, "set_gateway_dry_run_traces_write")
		requireOrchestratorsBlockLacks(t, setup, "usegateway:")
	})

	t.Run("set_model_dry_run_traces_write", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", "eng-1", "--model", "sonnet", "--dry-run"}, true, "set_model_dry_run_traces_write")
		requireOrchestratorsBlockLacks(t, setup, "    model:")
	})

	t.Run("set_effort_dry_run_traces_write", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-effort", "eng-1", "--effort", "high", "--dry-run"}, true, "set_effort_dry_run_traces_write")
		requireOrchestratorsBlockLacks(t, setup, "effort:")
	})

	// The real runs persist the setting and `erun list` reads it back, which is
	// what makes each command a writer with a reader rather than a field written
	// into a file nothing shows.
	t.Run("set_gateway_real_run_persists_and_is_visible_in_list", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
		seedGatewayCatalog(t, setup, defaultGatewayCatalogSeed())
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-gateway", "eng-1", "--gateway", "on"}, true, "set_gateway_real_run_persists_and_is_visible_in_list")
		requireOrchestratorsBlockHas(t, setup, "usegateway: true")
		runOrchestratorScenario(t, setup, []string{"list"}, true, "set_gateway_real_run_persists_and_is_visible_in_list_erun_list")
	})

	// set_model_real_run_persists_a_catalog_model_through_the_gateway records a
	// model the gateway can drive on an orchestrator already routed through it,
	// the accepted side of the reasoning-echo refusal below.
	t.Run("set_model_real_run_persists_a_catalog_model_through_the_gateway", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One", useGateway: true}})
		seedGatewayCatalog(t, setup, defaultGatewayCatalogSeed())
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", "eng-1", "--model", "deepseek/deepseek-v4-pro-0813"}, true, "set_model_real_run_persists_a_catalog_model_through_the_gateway")
		requireOrchestratorsBlockHas(t, setup, "model: deepseek/deepseek-v4-pro-0813")
		runOrchestratorScenario(t, setup, []string{"list"}, true, "set_model_real_run_persists_a_catalog_model_through_the_gateway_erun_list")
	})

	t.Run("set_effort_real_run_persists_and_is_visible_in_list", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-effort", "eng-1", "--effort", "high"}, true, "set_effort_real_run_persists_and_is_visible_in_list")
		requireOrchestratorsBlockHas(t, setup, "effort: high")
		runOrchestratorScenario(t, setup, []string{"list"}, true, "set_effort_real_run_persists_and_is_visible_in_list_erun_list")
	})

	// Clearing writes the zero value, which omitempty drops: the key is gone
	// rather than recorded as an explicit default. Turning the gateway off needs
	// no catalog, so none is seeded.
	t.Run("set_gateway_real_run_can_turn_it_off", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One", useGateway: true}})
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-gateway", "eng-1", "--gateway", "off"}, true, "set_gateway_real_run_can_turn_it_off")
		requireOrchestratorsBlockLacks(t, setup, "usegateway:")
	})

	t.Run("set_model_real_run_can_clear_back_to_default", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One", model: "sonnet"}})
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", "eng-1", "--model", "default"}, true, "set_model_real_run_can_clear_back_to_default")
		requireOrchestratorsBlockLacks(t, setup, "    model:")
	})

	t.Run("set_effort_real_run_can_clear_back_to_default", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One", effort: "high"}})
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-effort", "eng-1", "--effort", "default"}, true, "set_effort_real_run_can_clear_back_to_default")
		requireOrchestratorsBlockLacks(t, setup, "effort:")
	})

	t.Run("set_gateway_missing_gateway_flag_fails", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-gateway", "eng-1"}, false, "set_gateway_missing_gateway_flag_fails")
	})

	t.Run("set_model_missing_model_flag_fails", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", "eng-1"}, false, "set_model_missing_model_flag_fails")
	})

	t.Run("set_effort_missing_effort_flag_fails", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-effort", "eng-1"}, false, "set_effort_missing_effort_flag_fails")
	})

	t.Run("set_gateway_unknown_orchestrator_fails", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-gateway", "nope", "--gateway", "off"}, false, "set_gateway_unknown_orchestrator_fails")
	})

	t.Run("set_model_unknown_orchestrator_fails", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", "nope", "--model", "sonnet"}, false, "set_model_unknown_orchestrator_fails")
	})

	t.Run("set_effort_unknown_orchestrator_fails", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-effort", "nope", "--effort", "high"}, false, "set_effort_unknown_orchestrator_fails")
	})

	// A blank id reaches the shared writer (cobra counts it as the one
	// argument), whose refusal names the command to rerun.
	t.Run("set_model_blank_orchestrator_id_fails", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", " ", "--model", "sonnet"}, false, "set_model_blank_orchestrator_id_fails")
	})

	t.Run("set_gateway_invalid_value_fails", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-gateway", "eng-1", "--gateway", "maybe"}, false, "set_gateway_invalid_value_fails")
	})

	// An empty --model is neither a model nor the clearing token; it is refused
	// at parse time rather than read as "clear".
	t.Run("set_model_empty_value_fails", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One", model: "sonnet"}})
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", "eng-1", "--model", ""}, false, "set_model_empty_value_fails")
		requireOrchestratorsBlockHas(t, setup, "model: sonnet")
	})

	t.Run("set_effort_refuses_an_unknown_level", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-effort", "eng-1", "--effort", "extreme"}, false, "set_effort_refuses_an_unknown_level")
		requireOrchestratorsBlockLacks(t, setup, "effort:")
	})

	// set_gateway_on_refused_without_a_catalog: with nothing to route through,
	// the orchestrator would launch on this machine's own sign-in while reading
	// as routed. The refusal names where the catalog is configured.
	t.Run("set_gateway_on_refused_without_a_catalog", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-gateway", "eng-1", "--gateway", "on"}, false, "set_gateway_on_refused_without_a_catalog")
		requireOrchestratorsBlockLacks(t, setup, "usegateway:")
	})

	// A model is passed to `claude --model` on the launch line, so a value that
	// starts with '-' would be read as a flag. Passed with '=' so cobra hands it
	// to --model rather than parsing it as a flag of its own.
	t.Run("set_model_refuses_a_leading_dash", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One"}})
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", "eng-1", "--model=--dangerously-skip-permissions"}, false, "set_model_refuses_a_leading_dash")
		requireOrchestratorsBlockLacks(t, setup, "    model:")
	})

	// The reasoning-echo pair. The model is refused because the gateway is on;
	// the gateway is refused because of the model already recorded. Either way
	// the combination the gateway cannot drive is never written.
	t.Run("set_model_refuses_a_reasoning_echo_model_when_the_gateway_is_on", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One", useGateway: true}})
		seedGatewayCatalog(t, setup, defaultGatewayCatalogSeed())
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-model", "eng-1", "--model", "deepseek/deepseek-v4.1-flash"}, false, "set_model_refuses_a_reasoning_echo_model_when_the_gateway_is_on")
		requireOrchestratorsBlockLacks(t, setup, "    model:")
	})

	t.Run("set_gateway_on_refused_when_the_recorded_model_needs_reasoning_echo", func(t *testing.T) {
		setup := env.New(t)
		seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{{id: "eng-1", name: "Eng One", model: "deepseek/deepseek-v4.1-flash"}})
		seedGatewayCatalog(t, setup, defaultGatewayCatalogSeed())
		runOrchestratorScenario(t, setup, []string{"orchestrator", "set-gateway", "eng-1", "--gateway", "on"}, false, "set_gateway_on_refused_when_the_recorded_model_needs_reasoning_echo")
		requireOrchestratorsBlockLacks(t, setup, "usegateway:")
	})
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
