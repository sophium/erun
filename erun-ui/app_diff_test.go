package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestLoadDiffUsesSelectedMCPPort(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {
				Name:               "erun",
				DefaultEnvironment: "local",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "rancher-desktop",
			},
		},
	}
	var gotEndpoint string
	ensureCalls := 0
	app := NewApp(erunUIDeps{
		store: store,
		canConnectLocalPort: func(int) bool {
			return true
		},
		canReachMCPEndpoint: func(int) bool {
			return true
		},
		ensureMCP: func(_ context.Context, _ configpkg.OpenResult) error {
			ensureCalls++
			return nil
		},
		loadDiff: func(_ context.Context, endpoint, _ string, options uimodel.DiffOptions) (model.DiffResult, error) {
			gotEndpoint = endpoint
			if options.Scope != "commit" || options.SelectedCommit != "abc123" {
				t.Fatalf("unexpected diff options: %+v", options)
			}
			return model.DiffResult{RawDiff: "diff --git a/a.txt b/a.txt\n"}, nil
		},
	})

	result, err := app.LoadDiff(uimodel.Selection{Tenant: "erun", Environment: "local"}, uimodel.DiffOptions{Scope: " commit ", SelectedCommit: " abc123 "})
	if err != nil {
		t.Fatalf("LoadDiff failed: %v", err)
	}
	if gotEndpoint != "http://127.0.0.1:17000/mcp" {
		t.Fatalf("unexpected endpoint: %q", gotEndpoint)
	}
	if ensureCalls != 0 {
		t.Fatalf("LoadDiff must not implicitly run erun open; got ensureCalls=%d", ensureCalls)
	}
	if result.RawDiff == "" {
		t.Fatalf("unexpected diff result: %+v", result)
	}
}

func TestLoadDiffReturnsUnreachableWhenPortClosed(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {
				Name:               "erun",
				DefaultEnvironment: "test",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/test": {
				Name:              "test",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "orbstack",
			},
		},
	}
	ensureCalls := 0
	loadCalls := 0
	app := NewApp(erunUIDeps{
		store: store,
		canConnectLocalPort: func(int) bool {
			return false
		},
		canReachMCPEndpoint: func(int) bool {
			return false
		},
		ensureMCP: func(_ context.Context, _ configpkg.OpenResult) error {
			ensureCalls++
			return nil
		},
		loadDiff: func(_ context.Context, _, _ string, _ uimodel.DiffOptions) (model.DiffResult, error) {
			loadCalls++
			return model.DiffResult{}, nil
		},
	})

	_, err := app.LoadDiff(uimodel.Selection{Tenant: "erun", Environment: "test"}, uimodel.DiffOptions{})
	if err == nil {
		t.Fatalf("expected unreachable error when port is closed")
	}
	if !errors.Is(err, errMCPUnreachable) {
		t.Fatalf("expected errMCPUnreachable, got %v", err)
	}
	// A closed local port means not opened, not a stale forward.
	if !strings.Contains(err.Error(), "ERUN_MCP_UNREACHABLE_NOT_OPEN: ") {
		t.Fatalf("expected the not-open reachability marker, got %v", err)
	}
	if ensureCalls != 0 || loadCalls != 0 {
		t.Fatalf("LoadDiff must not run erun open or attempt the dial when the port is closed; got ensure=%d load=%d", ensureCalls, loadCalls)
	}
}

func TestLoadDiffReturnsStaleForwardKindWhenPortHeldButEdgeDead(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {
				Name:               "erun",
				DefaultEnvironment: "test",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/test": {
				Name:              "test",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "orbstack",
			},
		},
	}
	app := NewApp(erunUIDeps{
		store: store,
		canConnectLocalPort: func(int) bool {
			return true
		},
		canReachMCPEndpoint: func(int) bool {
			return false
		},
		loadDiff: func(_ context.Context, _, _ string, _ uimodel.DiffOptions) (model.DiffResult, error) {
			return model.DiffResult{}, nil
		},
	})

	_, err := app.LoadDiff(uimodel.Selection{Tenant: "erun", Environment: "test"}, uimodel.DiffOptions{})
	if err == nil {
		t.Fatalf("expected unreachable error when the edge never answers")
	}
	if !errors.Is(err, errMCPUnreachable) {
		t.Fatalf("expected errMCPUnreachable, got %v", err)
	}
	if !strings.Contains(err.Error(), "ERUN_MCP_UNREACHABLE_STALE: ") {
		t.Fatalf("expected the stale-forward reachability marker, got %v", err)
	}
}

func TestLoadDiffWrapsDialFailureAsUnreachable(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {
				Name:               "erun",
				DefaultEnvironment: "test",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/test": {
				Name:              "test",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "orbstack",
			},
		},
	}
	ensureCalls := 0
	app := NewApp(erunUIDeps{
		store: store,
		canConnectLocalPort: func(int) bool {
			return true
		},
		canReachMCPEndpoint: func(int) bool {
			return true
		},
		ensureMCP: func(_ context.Context, _ configpkg.OpenResult) error {
			ensureCalls++
			return nil
		},
		loadDiff: func(_ context.Context, _, _ string, _ uimodel.DiffOptions) (model.DiffResult, error) {
			return model.DiffResult{}, errors.New("EOF")
		},
	})

	_, err := app.LoadDiff(uimodel.Selection{Tenant: "erun", Environment: "test"}, uimodel.DiffOptions{})
	if err == nil {
		t.Fatalf("expected dial failure to surface as unreachable")
	}
	if !errors.Is(err, errMCPUnreachable) {
		t.Fatalf("expected errMCPUnreachable, got %v", err)
	}
	// The port is still held; the RPC dial failed mid-call, so this is stale.
	if !strings.Contains(err.Error(), "ERUN_MCP_UNREACHABLE_STALE: ") {
		t.Fatalf("expected the stale-forward reachability marker, got %v", err)
	}
	if ensureCalls != 0 {
		t.Fatalf("LoadDiff must not implicitly run erun open after a dial failure; got ensureCalls=%d", ensureCalls)
	}
}

func TestReconnectMCPRunsOpenAndStreamsLines(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {
				Name:               "erun",
				DefaultEnvironment: "test",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/test": {
				Name:              "test",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "orbstack",
			},
		},
	}
	calls := 0
	var lines []string
	app := NewApp(erunUIDeps{
		store: store,
		reconnectMCP: func(_ context.Context, result configpkg.OpenResult, onLine func(string)) error {
			calls++
			if result.Tenant != "erun" || result.Environment != "test" {
				t.Fatalf("unexpected target: %+v", result)
			}
			onLine("step: deploying erun-devops")
			lines = append(lines, "step: deploying erun-devops")
			onLine("==> Deployed erun/test")
			lines = append(lines, "==> Deployed erun/test")
			return nil
		},
	})

	if err := app.ReconnectMCP(uimodel.Selection{Tenant: " erun ", Environment: " test "}); err != nil {
		t.Fatalf("ReconnectMCP failed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected reconnect to invoke open exactly once, got %d", calls)
	}
	if len(lines) != 2 {
		t.Fatalf("expected two streamed lines, got %d", len(lines))
	}
}
