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
	// An undeclared role renders as "undeclared" rather than a guessed default.
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
	// Environment `type: runtime` and link `role=runtime` are different things and
	// must render in separate labeled sections.
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
	// A config written before set-role's gate can still carry an invalid pairing;
	// reads must display it, only writes are gated.
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
	// An orchestrator with only directories still has a scope that must be reported.
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
