package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// envTraceApp builds an App for the Diagnostics console read paths (issue
// #466), hermetic per the #492 rule: reachability and the pod runner are
// always injected.
func envTraceApp(t *testing.T, env eruncommon.EnvConfig, reachable bool, podOut string, podErr error) *App {
	t.Helper()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"acme": {Name: "acme", ProjectRoot: t.TempDir(), DefaultEnvironment: "dev"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"acme/dev": env,
		},
	}
	app := NewApp(erunUIDeps{
		store:               store,
		findProjectRoot:     func() (string, string, error) { return "acme", "/tmp/acme", nil },
		canConnectLocalPort: func(int) bool { return reachable },
		runPodRaw: func(context.Context, string, []string) (string, error) {
			return podOut, podErr
		},
	})
	t.Cleanup(func() { app.shutdown(context.Background()) })
	return app
}

func TestLoadEnvTraceHostFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	logPath := filepath.Join(home, ".erun", "acme", "dev", "trace.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("2026-06-11T00:00:00Z ==> Deploying acme/dev 1.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := envTraceApp(t, eruncommon.EnvConfig{
		Name: "dev", Type: eruncommon.EnvironmentTypeLocalAgent, KubernetesContext: "ctx", DebugOutput: true,
	}, false, "", nil)

	trace, err := app.LoadEnvTrace(uiSelection{Tenant: "acme", Environment: "dev"})
	if err != nil {
		t.Fatalf("LoadEnvTrace: %v", err)
	}
	if !trace.Available || !trace.Enabled || !strings.Contains(trace.Content, "==> Deploying acme/dev") {
		t.Fatalf("unexpected host trace: %+v", trace)
	}
}

func TestLoadEnvTraceHostFileMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := envTraceApp(t, eruncommon.EnvConfig{
		Name: "dev", Type: eruncommon.EnvironmentTypeLocalAgent, KubernetesContext: "ctx",
	}, false, "", nil)

	trace, err := app.LoadEnvTrace(uiSelection{Tenant: "acme", Environment: "dev"})
	if err != nil {
		t.Fatalf("LoadEnvTrace: %v", err)
	}
	if trace.Available || trace.Enabled || trace.Reason != "no trace captured yet" {
		t.Fatalf("expected the honest empty state, got %+v", trace)
	}
}

func TestLoadEnvTracePodGatedOnReachability(t *testing.T) {
	app := envTraceApp(t, eruncommon.EnvConfig{
		Name: "dev", Type: eruncommon.EnvironmentTypeRemoteAgent, KubernetesContext: "ctx", LocalPortRangeStart: 17500,
	}, false, "", nil)

	trace, err := app.LoadEnvTrace(uiSelection{Tenant: "acme", Environment: "dev"})
	if err != nil {
		t.Fatalf("LoadEnvTrace: %v", err)
	}
	if trace.Available || !strings.Contains(trace.Reason, "not reachable") {
		t.Fatalf("expected the not-reachable state, got %+v", trace)
	}
}

func TestLoadEnvTracePodTail(t *testing.T) {
	app := envTraceApp(t, eruncommon.EnvConfig{
		Name: "dev", Type: eruncommon.EnvironmentTypeRemoteAgent, KubernetesContext: "ctx", LocalPortRangeStart: 17500, DebugOutput: true,
	}, true, "2026-06-11T00:00:00Z deploy: resolved 1 spec(s)\n", nil)

	trace, err := app.LoadEnvTrace(uiSelection{Tenant: "acme", Environment: "dev"})
	if err != nil {
		t.Fatalf("LoadEnvTrace: %v", err)
	}
	if !trace.Available || !strings.Contains(trace.Content, "resolved 1 spec(s)") {
		t.Fatalf("expected the in-pod tail, got %+v", trace)
	}
}

func TestSetEnvDebugOutputPersists(t *testing.T) {
	app := envTraceApp(t, eruncommon.EnvConfig{
		Name: "dev", Type: eruncommon.EnvironmentTypeLocalAgent, KubernetesContext: "ctx",
	}, false, "", nil)
	if err := app.SetEnvDebugOutput(uiSelection{Tenant: "acme", Environment: "dev"}, true); err != nil {
		t.Fatalf("SetEnvDebugOutput: %v", err)
	}
	config, _, err := app.deps.store.LoadEnvConfig("acme", "dev")
	if err != nil || !config.DebugOutput {
		t.Fatalf("expected debugoutput persisted, got %+v err=%v", config, err)
	}
}
