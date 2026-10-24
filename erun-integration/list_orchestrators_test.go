package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testListWithOrchestratorEnvironmentRoles(t *testing.T) {
	// erun list shows each orchestrator's
	// linked environments with their role, and an undeclared role
	// renders distinctly ("undeclared") rather than guessing a default.
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
		{
			id:   "eng-1",
			name: "Eng One",
			environments: []orchestratorEnvSeed{
				{tenant: "team", environment: "dev", directory: "/repo/team-dev", role: "code"},
				{tenant: "team", environment: "staging", directory: "/repo/team-staging"},
			},
		},
		{
			id:   "eng-2",
			name: "Eng Two",
			environments: []orchestratorEnvSeed{
				{tenant: "other", environment: "build", directory: "/repo/other-build", role: "build"},
			},
		},
	})
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/with_orchestrator_environment_roles", normalize.Apply(result.Combined))
}

func testListWithOrchestratorRuntimeRoleOnARuntimeTypeEnvironment(t *testing.T) {
	// Type and role share the
	// spelling "runtime" for two different things. The tenant's own
	// environment carries `type: runtime` (its worktree/pod shape); the
	// orchestrator link to that same environment carries `role=runtime`
	// (what the orchestrator uses it for). Both must render, each in its
	// own labeled section, so a reader can tell which is which.
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
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/with_orchestrator_runtime_role_on_a_runtime_type_environment", normalize.Apply(result.Combined))
}

func testListWithOrchestratorPairingInvalidBeforeTheRoleGateExisted(t *testing.T) {
	// `erun orchestrator set-role` now refuses a role
	// `config.OrchestratorEnvRoleAllowed` would not have let the
	// desktop link this environment with in the first place, but a
	// config written before that gate existed can still carry the
	// invalid pairing on disk (role=code against a runtime-type
	// environment, which the desktop's link gate would refuse to create
	// today). `erun list` reads the persisted role directly and never
	// re-resolves the environment's type to render it, so this must
	// still load and display the mismatch rather than erroring out --
	// only a write through set-role is gated, never a read.
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
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/with_orchestrator_pairing_invalid_before_the_role_gate_existed", normalize.Apply(result.Combined))
}

func testListWithOrchestratorDirectories(t *testing.T) {
	// An orchestrator may be pointed at directories of its own and link no
	// environment at all -- that is a complete definition, not an empty one.
	// `erun list` has to report them, or an orchestrator working only in
	// directories reads as having no scope.
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
		{
			id:          "scratch",
			name:        "Scratch",
			directories: []string{"/opt/operator/notes", "/opt/operator/scratch"},
		},
	})
	result := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "list/with_orchestrator_directories", normalize.Apply(result.Combined))
}
