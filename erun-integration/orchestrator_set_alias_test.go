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

// Writes that should land seed an erun-type alias: the writer refuses ones this host cannot resolve.
func testOrchestratorSetAliasHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"orchestrator", "set-alias", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "orchestrator/set_alias_help", normalize.Apply(result.Combined))
}

func testOrchestratorSetAliasDryRunTracesWrite(t *testing.T) {
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
}

func testOrchestratorSetAliasRealRunPersistsAndIsVisibleInList(t *testing.T) {
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

	listResult := erun.Run(t, []string{"list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if listResult.ExitCode != 0 {
		t.Fatalf("exit %d: %s", listResult.ExitCode, listResult.Combined)
	}
	golden.Equal(t, "orchestrator/set_alias_real_run_persists_and_is_visible_in_list_erun_list", normalize.Apply(listResult.Combined))
}

func testOrchestratorSetAliasRealRunCanClearBackToNone(t *testing.T) {
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
}

// An unresolvable alias is indistinguishable from an unset one, so it is refused.
func testOrchestratorSetAliasRefusesAnAliasThisHostHasNotConfigured(t *testing.T) {
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
}

func testOrchestratorSetAliasRefusesANonErunAlias(t *testing.T) {
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
}

func testOrchestratorSetAliasMissingAliasFlagFails(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
		{id: "eng-1", name: "Eng One"},
	})
	result := erun.Run(t, []string{"orchestrator", "set-alias", "eng-1"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when --alias is omitted, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "orchestrator/set_alias_missing_alias_flag_fails", normalize.Apply(result.Combined))
}

func testOrchestratorSetAliasUnknownOrchestratorFails(t *testing.T) {
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
}

// set-alias never creates or signs in to an alias.
func testOrchestratorSetAliasRefusesWhenThisHostHasNoErunAlias(t *testing.T) {
	setup := env.New(t)
	seedOrchestratorsWithEnvRoles(t, setup, []orchestratorSeed{
		{id: "eng-1", name: "Eng One"},
	})
	result := erun.Run(t, []string{"orchestrator", "set-alias", "eng-1", "--alias", "erun+api.example.test@erun"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit with no erun alias configured, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "orchestrator/set_alias_refuses_when_this_host_has_no_erun_alias", normalize.Apply(result.Combined))
}
