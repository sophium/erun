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
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
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
			"erun": {Name: "erun", DefaultEnvironment: "local"},
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

// newEndAISessionsTestApp is a test helper for the EndAISessions contract
// tests (issues #477/#482): an App over a stub store whose env declares the
// given AI tool, with terminals stubbed and kubectl PATH-stubbed so every
// invocation appends its argv to the returned capture file.
func newEndAISessionsTestApp(t *testing.T, aiTool string) (*App, string) {
	t.Helper()
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
				"erun": {Name: "erun", DefaultEnvironment: "remote"},
			},
			envs: map[string]eruncommon.EnvConfig{
				"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx", AITool: aiTool},
			},
		},
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
	})
	t.Cleanup(func() { app.shutdown(context.Background()) })
	app.SetEmitter(func(string, ...any) {})
	return app, captureFile
}

// TestEndAISessionsEndsBothPodSessions pins the pod side of the relaunch
// contract behind the Manage dialog's Claude launch-flag save (issues
// #477/#482): both AI pod sessions ("ai" and "contribute-ai") are ended —
// `dtach -A` would otherwise reattach to the running claude and a changed
// launch flag could never apply. contribute-ai is ended even though no
// contribute tab is open: a detached claude must not keep stale flags.
func TestEndAISessionsEndsBothPodSessions(t *testing.T) {
	app, captureFile := newEndAISessionsTestApp(t, "")
	selection := uiSelection{Tenant: "erun", Environment: "remote"}
	if _, err := app.StartAISession(selection, 0, 80, 24); err != nil {
		t.Fatalf("StartAISession: %v", err)
	}
	ended, err := app.EndAISessions(selection)
	if err != nil {
		t.Fatalf("EndAISessions: %v", err)
	}
	if !ended {
		t.Fatalf("expected EndAISessions to end the managed claude sessions")
	}
	data, _ := os.ReadFile(captureFile)
	captured := string(data)
	for _, socket := range []string{"erun-remote-ai.dtach", "erun-remote-contribute-ai.dtach"} {
		if !strings.Contains(captured, socket) || !strings.Contains(captured, "rm -f") {
			t.Fatalf("expected the end-script kubectl exec for %s, captured: %q", socket, captured)
		}
	}
}

// TestEndAISessionsSpawnsFreshAIAndLeavesShellAttached pins the desktop side
// of the relaunch contract: after EndAISessions the next StartAISession must
// spawn a fresh session (whose `erun open --ai` re-resolves the env's launch
// flags) instead of reusing the dying one, while the ERun tab — whose launch
// command does not change — stays attached to its live session.
func TestEndAISessionsSpawnsFreshAIAndLeavesShellAttached(t *testing.T) {
	app, _ := newEndAISessionsTestApp(t, "")
	selection := uiSelection{Tenant: "erun", Environment: "remote"}
	ai, err := app.StartAISession(selection, 0, 80, 24)
	if err != nil {
		t.Fatalf("StartAISession: %v", err)
	}
	shell, err := app.StartSession(selection, 0, 80, 24)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if _, err := app.EndAISessions(selection); err != nil {
		t.Fatalf("EndAISessions: %v", err)
	}
	aiAgain, err := app.StartAISession(selection, 0, 80, 24)
	if err != nil {
		t.Fatalf("StartAISession after end: %v", err)
	}
	if aiAgain.SessionID == ai.SessionID {
		t.Fatalf("expected a fresh AI session after EndAISessions, got the old id %d", ai.SessionID)
	}
	shellAgain, err := app.StartSession(selection, 0, 80, 24)
	if err != nil {
		t.Fatalf("StartSession after end: %v", err)
	}
	if shellAgain.SessionID != shell.SessionID {
		t.Fatalf("ERun tab must stay attached: got new id %d, want %d", shellAgain.SessionID, shell.SessionID)
	}
}

// TestEndAISessionsSkipsVerbatimAITool pins the guard for envs whose AI tool
// launches verbatim: the managed Claude launch flags never participate in a
// non-claude launch, so a claude-flag save must not discard the running
// session (codex has no --continue). EndAISessions reports false and the AI
// tab keeps its live session.
func TestEndAISessionsSkipsVerbatimAITool(t *testing.T) {
	app, _ := newEndAISessionsTestApp(t, "codex")
	selection := uiSelection{Tenant: "erun", Environment: "remote"}
	ai, err := app.StartAISession(selection, 0, 80, 24)
	if err != nil {
		t.Fatalf("StartAISession: %v", err)
	}
	ended, err := app.EndAISessions(selection)
	if err != nil {
		t.Fatalf("EndAISessions: %v", err)
	}
	if ended {
		t.Fatalf("a verbatim AI tool must not be ended by a claude-flag change")
	}
	aiAgain, err := app.StartAISession(selection, 0, 80, 24)
	if err != nil {
		t.Fatalf("StartAISession after no-op end: %v", err)
	}
	if aiAgain.SessionID != ai.SessionID {
		t.Fatalf("AI session must stay live: got new id %d, want %d", aiAgain.SessionID, ai.SessionID)
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
				"erun": {Name: "erun", DefaultEnvironment: "remote"},
			},
			envs: map[string]eruncommon.EnvConfig{
				"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
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
	waitForKubectlEndScript(t, captureFile, "erun-remote-open-2.dtach")

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

// waitForKubectlEndScript polls the PATH-stub kubectl capture file until it
// records the end-script exec (an "rm -f" of the named dtach socket), failing
// the test if it does not appear within the deadline.
func waitForKubectlEndScript(t *testing.T, captureFile, socket string) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for {
		data, _ := os.ReadFile(captureFile)
		if strings.Contains(string(data), socket) && strings.Contains(string(data), "rm -f") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected the end-script kubectl exec for %s, captured: %q", socket, data)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
