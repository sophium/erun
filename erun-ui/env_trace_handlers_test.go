package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// setTestHomeDir points os.UserHomeDir() at dir on the current OS. Windows reads
// USERPROFILE, not HOME, so setting HOME alone leaves production resolving the host
// trace under the real profile while the test staged it under the temp dir.
func setTestHomeDir(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	}
}

func envTraceApp(t *testing.T, env eruncommon.EnvConfig, reachable bool, podOut string, podErr error) *App {
	t.Helper()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"acme": {Name: "acme", DefaultEnvironment: "dev"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"acme/dev": env,
		},
	}
	app := NewApp(erunUIDeps{
		store:               store,
		findProjectRoot:     func() (string, string, error) { return "acme", "/tmp/acme", nil },
		canConnectLocalPort: func(int) bool { return reachable },
		canReachMCPEndpoint: func(int) bool { return reachable },
		runPodRaw: func(context.Context, string, string, []string) (string, error) {
			return podOut, podErr
		},
	})
	t.Cleanup(func() { app.shutdown(context.Background()) })
	return app
}

func TestLoadEnvTraceHostFile(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	logPath := filepath.Join(home, ".erun", "acme", "dev", "trace.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("2026-06-11T00:00:00Z ==> Deploying acme/dev 1.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := envTraceApp(t, eruncommon.EnvConfig{
		Name: "dev", LocalRepoPath: t.TempDir(), Type: eruncommon.EnvironmentTypeLocalAgent, KubernetesContext: "ctx",
	}, false, "", nil)

	trace, err := app.LoadEnvTrace(uiSelection{Tenant: "acme", Environment: "dev"})
	if err != nil {
		t.Fatalf("LoadEnvTrace: %v", err)
	}
	if !trace.Available || !strings.Contains(trace.Content, "==> Deploying acme/dev") {
		t.Fatalf("unexpected host trace: %+v", trace)
	}
}

func TestLoadEnvTraceHostFileMissing(t *testing.T) {
	setTestHomeDir(t, t.TempDir())
	app := envTraceApp(t, eruncommon.EnvConfig{
		Name: "dev", LocalRepoPath: t.TempDir(), Type: eruncommon.EnvironmentTypeLocalAgent, KubernetesContext: "ctx",
	}, false, "", nil)

	trace, err := app.LoadEnvTrace(uiSelection{Tenant: "acme", Environment: "dev"})
	if err != nil {
		t.Fatalf("LoadEnvTrace: %v", err)
	}
	if trace.Available || trace.Reason != "no trace captured yet" {
		t.Fatalf("expected the honest empty state, got %+v", trace)
	}
}

// TestLoadEnvTraceRemoteUnreachableKeepsHostTrace pins the fix: a
// remote env's operator-driven commands trace on the host, so an
// unreachable pod must degrade to a notice — not blank the pane.
func TestLoadEnvTraceRemoteUnreachableKeepsHostTrace(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	writeHostTrace(t, home, "2026-06-11T00:00:00Z open: tenant=acme environment=dev\n")
	app := envTraceApp(t, eruncommon.EnvConfig{
		Name: "dev", LocalRepoPath: "/home/erun/git/acme", Type: eruncommon.EnvironmentTypeRemoteAgent, KubernetesContext: "ctx", LocalPortRangeStart: 17500,
	}, false, "", nil)

	trace, err := app.LoadEnvTrace(uiSelection{Tenant: "acme", Environment: "dev"})
	if err != nil {
		t.Fatalf("LoadEnvTrace: %v", err)
	}
	if !trace.Available || !strings.Contains(trace.Content, "open: tenant=acme environment=dev") {
		t.Fatalf("expected the host trace despite the unreachable pod, got %+v", trace)
	}
	if !strings.Contains(trace.Notice, "in-pod trace unavailable") {
		t.Fatalf("expected the in-pod-unavailable notice, got %+v", trace)
	}
}

// TestLoadEnvTraceRemoteMergesHostAndPod pins the merged timeline: the two
// stamped tails interleave chronologically and pod-origin lines carry the
// [pod] marker so the vantage point stays attributable.
func TestLoadEnvTraceRemoteMergesHostAndPod(t *testing.T) {
	home := t.TempDir()
	setTestHomeDir(t, home)
	writeHostTrace(t, home,
		"2026-06-11T00:00:01Z open: tenant=acme environment=dev\n"+
			"2026-06-11T00:00:04Z kubectl config use-context ctx\n")
	app := envTraceApp(t, eruncommon.EnvConfig{
		Name: "dev", LocalRepoPath: "/home/erun/git/acme", Type: eruncommon.EnvironmentTypeRemoteAgent, KubernetesContext: "ctx", LocalPortRangeStart: 17500,
	}, true,
		"2026-06-11T00:00:02Z deploy: resolved 1 spec(s)\n"+
			"2026-06-11T00:00:05Z doctor: all checks passed\n", nil)

	trace, err := app.LoadEnvTrace(uiSelection{Tenant: "acme", Environment: "dev"})
	if err != nil {
		t.Fatalf("LoadEnvTrace: %v", err)
	}
	want := "2026-06-11T00:00:01Z open: tenant=acme environment=dev\n" +
		"2026-06-11T00:00:02Z [pod] deploy: resolved 1 spec(s)\n" +
		"2026-06-11T00:00:04Z kubectl config use-context ctx\n" +
		"2026-06-11T00:00:05Z [pod] doctor: all checks passed\n"
	if !trace.Available || trace.Content != want {
		t.Fatalf("unexpected merged timeline:\n got: %q\nwant: %q (trace %+v)", trace.Content, want, trace)
	}
	if trace.Notice != "" {
		t.Fatalf("expected no notice on a clean merge, got %+v", trace)
	}
}

func TestLoadEnvTraceRemotePodOnly(t *testing.T) {
	setTestHomeDir(t, t.TempDir())
	app := envTraceApp(t, eruncommon.EnvConfig{
		Name: "dev", LocalRepoPath: "/home/erun/git/acme", Type: eruncommon.EnvironmentTypeRemoteAgent, KubernetesContext: "ctx", LocalPortRangeStart: 17500,
	}, true, "2026-06-11T00:00:00Z deploy: resolved 1 spec(s)\n", nil)

	trace, err := app.LoadEnvTrace(uiSelection{Tenant: "acme", Environment: "dev"})
	if err != nil {
		t.Fatalf("LoadEnvTrace: %v", err)
	}
	if !trace.Available || !strings.Contains(trace.Content, "[pod] deploy: resolved 1 spec(s)") {
		t.Fatalf("expected the marked in-pod tail, got %+v", trace)
	}
}

func TestLoadEnvTraceRemoteBothEmpty(t *testing.T) {
	setTestHomeDir(t, t.TempDir())
	app := envTraceApp(t, eruncommon.EnvConfig{
		Name: "dev", LocalRepoPath: "/home/erun/git/acme", Type: eruncommon.EnvironmentTypeRemoteAgent, KubernetesContext: "ctx", LocalPortRangeStart: 17500,
	}, true, "", nil)

	trace, err := app.LoadEnvTrace(uiSelection{Tenant: "acme", Environment: "dev"})
	if err != nil {
		t.Fatalf("LoadEnvTrace: %v", err)
	}
	if trace.Available || trace.Reason != "no trace captured yet" {
		t.Fatalf("expected the honest empty state, got %+v", trace)
	}
}

func writeHostTrace(t *testing.T, home, content string) {
	t.Helper()
	logPath := filepath.Join(home, ".erun", "acme", "dev", "trace.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
