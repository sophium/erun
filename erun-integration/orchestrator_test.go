package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// TestOrchestrator locks erun#1745's fix: OrchestratorEnvConfig.Role could be
// read (`erun list`) but had no CLI writer. `erun orchestrator set-role` is
// that writer's dry-run trace, real-run persistence, and error paths --
// reusing list_test.go's seedOrchestratorsWithEnvRoles so both commands seed
// and read the same on-disk shape. The desktop dialog's own writer is covered
// by erun-ui/orchestrator_test.go and erun-ui/playwright/tests/
// orchestrator-env-role.spec.ts; this suite has no Wails runtime to drive it
// from.
func TestOrchestrator(t *testing.T) {
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

	// set_role_real_run_accepts_the_runtime_role locks erun#1770's third role
	// value on this writer: "runtime" parses and persists exactly like "code"
	// and "build" above. set-role has no environment-type gate (see
	// erun-common's OrchestratorRoleStore doc comment for why that is a
	// tracked follow-up, not this test's concern) -- it only proves the value
	// itself is now legal here, the same way the desktop's link/edit gate
	// (erun-ui/orchestrator_test.go) proves it for the link path.
	t.Run("set_role_real_run_accepts_the_runtime_role", func(t *testing.T) {
		setup := env.New(t)
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
}
