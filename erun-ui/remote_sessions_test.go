package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestListRemoteAppSessionsParsesPodSockets drives the read-model through a
// PATH-stubbed kubectl that prints a pod's /tmp/erun-app listing: dtach
// sockets for this env become session ids, while owner files and other envs'
// sockets are ignored. This is what lets a fresh ERun window rebuild tabs for
// sessions another window created.
func TestListRemoteAppSessionsParsesPodSockets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH-stub kubectl uses a shell script; skipping on Windows")
	}
	stubDir := t.TempDir()
	script := "#!/bin/sh\n" +
		"printf 'erun-remote-open-1.dtach\\nerun-remote-ai.dtach\\nerun-remote-open-1.owner\\nother-env-open-2.dtach\\n'\n"
	if err := os.WriteFile(filepath.Join(stubDir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	app := NewApp(erunUIDeps{store: stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: t.TempDir(), DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", KubernetesContext: "ctx"},
		},
	}})

	got := app.ListRemoteAppSessions(uiSelection{Tenant: "erun", Environment: "remote"})
	want := []string{"open-1", "ai"}
	if !slices.Equal(got, want) {
		t.Fatalf("ListRemoteAppSessions = %v, want %v", got, want)
	}
}

// TestListRemoteAppSessionsFailsSoft pins the contract that detection never
// surfaces an error into the open flow: an unknown env and an env without a
// kubernetes context both yield nil.
func TestListRemoteAppSessionsFailsSoft(t *testing.T) {
	app := NewApp(erunUIDeps{store: stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: t.TempDir(), DefaultEnvironment: "local"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/local": {Name: "local"}, // no kubernetes context
		},
	}})

	if got := app.ListRemoteAppSessions(uiSelection{Tenant: "erun", Environment: "local"}); got != nil {
		t.Fatalf("env without kubernetes context must yield nil, got %v", got)
	}
	if got := app.ListRemoteAppSessions(uiSelection{Tenant: "erun", Environment: "missing"}); got != nil {
		t.Fatalf("unknown env must yield nil, got %v", got)
	}
}

// TestCloseSessionEndsRemoteCustomTerminal pins the explicit-close contract
// end to end through the desktop: X-ing a custom terminal tab (slot > 0) must
// run the end-script in the pod — otherwise the session outlives the close
// and detection resurrects the tab on the next env open. The PATH-stubbed
// kubectl captures the exec; a default tab close (slot 0) must not end
// anything (long-running default sessions are the feature).
func TestCloseSessionEndsRemoteCustomTerminal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH-stub kubectl uses a shell script; skipping on Windows")
	}
	stubDir := t.TempDir()
	captureFile := filepath.Join(stubDir, "kubectl-args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + captureFile + "\"\n"
	if err := os.WriteFile(filepath.Join(stubDir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	projectRoot := t.TempDir()
	app := NewApp(erunUIDeps{
		store: stubUIStore{
			tenants: map[string]eruncommon.TenantConfig{
				"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
			},
			envs: map[string]eruncommon.EnvConfig{
				"erun/remote": {Name: "remote", RepoPath: projectRoot, KubernetesContext: "ctx"},
			},
		},
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())
	app.SetEmitter(func(string, ...any) {})

	selection := uiSelection{Tenant: "erun", Environment: "remote"}
	extra, err := app.StartSession(selection, 2, 80, 24)
	if err != nil {
		t.Fatalf("StartSession(slot 2): %v", err)
	}
	if err := app.CloseSession(extra.SessionID); err != nil {
		t.Fatalf("CloseSession(extra): %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, _ := os.ReadFile(captureFile)
		if strings.Contains(string(data), "erun-remote-open-2.dtach") &&
			strings.Contains(string(data), "rm -f") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected the end-script kubectl exec for open-2, captured: %q", data)
		}
		time.Sleep(25 * time.Millisecond)
	}

	_ = os.Remove(captureFile)
	def, err := app.StartSession(selection, 0, 80, 24)
	if err != nil {
		t.Fatalf("StartSession(slot 0): %v", err)
	}
	if err := app.CloseSession(def.SessionID); err != nil {
		t.Fatalf("CloseSession(default): %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if data, _ := os.ReadFile(captureFile); strings.Contains(string(data), "rm -f") {
		t.Fatalf("default tab close must not end the pod session, captured: %q", data)
	}
}
