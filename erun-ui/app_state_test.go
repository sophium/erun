package main

import (
	"context"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle"
)

func TestStateFromListResultUsesEffectiveSelection(t *testing.T) {
	state := stateFromListResult(lifecycle.ListResult{
		CurrentDirectory: lifecycle.ListCurrentDirectoryResult{
			Effective: &lifecycle.ListEffectiveTargetResult{
				Tenant:      "erun",
				Environment: "local",
			},
		},
		Tenants: []lifecycle.ListTenantResult{
			{
				Name: "erun",
				Environments: []lifecycle.ListEnvironmentResult{
					{Name: "local", APIURL: "http://127.0.0.1:17033", RuntimeVersion: "1.0.19-snapshot-20260418141901", LocalPorts: configpkg.EnvironmentLocalPorts{MCP: 17000, API: 17033}, SSH: lifecycle.ListSSHResult{Enabled: true}},
					{Name: "remote", APIURL: "http://127.0.0.1:17133", RuntimeVersion: "1.0.18", Type: configpkg.EnvironmentTypeRemoteAgent, LocalPorts: configpkg.EnvironmentLocalPorts{MCP: 17100, API: 17133}},
				},
			},
		},
	}, model.BuildInfo{Version: "1.0.50"})

	if state.Selected == nil {
		t.Fatal("expected selected environment")
	}
	if state.Selected.Tenant != "erun" || state.Selected.Environment != "local" {
		t.Fatalf("unexpected selected environment: %+v", state.Selected)
	}
	if len(state.Tenants) != 1 || len(state.Tenants[0].Environments) != 2 {
		t.Fatalf("unexpected tenants: %+v", state.Tenants)
	}
	assertEffectiveSelectionEnvironments(t, state.Tenants[0].Environments)
}

func assertEffectiveSelectionEnvironments(t *testing.T, envs []uiEnvironment) {
	t.Helper()

	if envs[0].MCPURL != "http://127.0.0.1:17000/mcp" {
		t.Fatalf("unexpected MCP URL: %+v", envs[0])
	}
	if envs[0].APIURL != "http://127.0.0.1:17033" {
		t.Fatalf("unexpected API URL: %+v", envs[0])
	}
	if envs[0].RuntimeVersion != "1.0.19-snapshot-20260418141901" {
		t.Fatalf("unexpected runtime version: %+v", envs[0])
	}
	if !envs[0].SSHDEnabled || envs[1].SSHDEnabled {
		t.Fatalf("unexpected SSHD flags: %+v", envs)
	}
	if envs[0].Type == string(configpkg.EnvironmentTypeRemoteAgent) || envs[1].Type != string(configpkg.EnvironmentTypeRemoteAgent) {
		t.Fatalf("unexpected env types: %+v", envs)
	}
}

func TestStateFromListResultOmitsEmptyTenants(t *testing.T) {
	state := stateFromListResult(lifecycle.ListResult{
		Tenants: []lifecycle.ListTenantResult{
			{Name: "empty"},
			{
				Name: "active",
				Environments: []lifecycle.ListEnvironmentResult{
					{Name: "prod"},
				},
			},
		},
	}, model.BuildInfo{Version: "1.0.50"})

	if len(state.Tenants) != 1 || state.Tenants[0].Name != "active" {
		t.Fatalf("unexpected tenants: %+v", state.Tenants)
	}
}

// TestLoadStateFirstRunNeverRendersBareCLIInstruction: a genuinely fresh install (no tool
// config at all) must resolve to the same renderable, non-nil-tenants shape
// as "initialized with zero tenants", never a bare CLI instruction the
// desktop cannot carry out and never a shape that crashes the frontend's
// unconditional tenants iteration on boot.
func TestLoadStateFirstRunNeverRendersBareCLIInstruction(t *testing.T) {
	app := NewApp(erunUIDeps{
		store: stubUIStore{
			listTenantsErr: configpkg.ErrNotInitialized,
		},
		findProjectRoot:  func() (string, string, error) { return "", "", configpkg.ErrNotInitialized },
		resolveBuildInfo: func() model.BuildInfo { return model.BuildInfo{Version: "1.0.50"} },
	})

	state, err := app.LoadState()
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}
	if state.Tenants == nil {
		t.Fatal("expected a non-nil Tenants slice so the frontend's boot-time iteration and JSON marshaling both stay safe")
	}
	if len(state.Tenants) != 0 {
		t.Fatalf("expected zero tenants for a fresh install, got: %+v", state.Tenants)
	}
	if strings.Contains(state.Message, "`") || strings.Contains(state.Message, "erun init") {
		t.Fatalf("expected no bare CLI instruction in the first-run message, got: %q", state.Message)
	}
}

// TestLoadStateConfigCorruptedRendersDistinctFromEmptyList: a torn/corrupt config file must not surface as "zero
// environments configured" (which the sidebar renders identically to a
// genuinely empty install), and the raw yaml/unmarshal error text must never
// reach the caller -- it crosses the headless HTTP bridge verbatim into a
// browser console otherwise (headlessserver.handleInvoke writes err.Error()
// into the response body).
func TestLoadStateConfigCorruptedRendersDistinctFromEmptyList(t *testing.T) {
	app := NewApp(erunUIDeps{
		store: stubUIStore{
			listTenantsErr: configpkg.ErrConfigCorrupted,
		},
		findProjectRoot:  func() (string, string, error) { return "", "", configpkg.ErrNotInGitRepository },
		resolveBuildInfo: func() model.BuildInfo { return model.BuildInfo{Version: "1.0.50"} },
	})

	state, err := app.LoadState()
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}
	if !state.ConfigUnreadable {
		t.Fatal("expected ConfigUnreadable=true so the sidebar renders a distinct state from a genuine empty list")
	}
	if state.Tenants == nil {
		t.Fatal("expected a non-nil Tenants slice so the frontend's boot-time iteration and JSON marshaling both stay safe")
	}
	if len(state.Tenants) != 0 {
		t.Fatalf("expected zero tenants (the read genuinely failed), got: %+v", state.Tenants)
	}
	if strings.Contains(state.Message, "unmarshal") || strings.Contains(state.Message, "yaml:") {
		t.Fatalf("expected no raw unmarshal error text in the message, got: %q", state.Message)
	}
}

func TestLoadKubernetesContextsNormalizesContexts(t *testing.T) {
	app := NewApp(erunUIDeps{
		listKubeContexts: func() ([]string, error) {
			return []string{" cluster-b ", "cluster-a", "cluster-b", ""}, nil
		},
	})
	defer app.shutdown(context.Background())

	contexts, err := app.LoadKubernetesContexts()
	if err != nil {
		t.Fatalf("LoadKubernetesContexts failed: %v", err)
	}

	want := []string{"cluster-b", "cluster-a"}
	if strings.Join(contexts, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected contexts: got %+v want %+v", contexts, want)
	}
}
