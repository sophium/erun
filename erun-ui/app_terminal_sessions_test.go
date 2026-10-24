package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

func TestStartLocalSessionStartsShellAtRepoPath(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]configpkg.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}

	var started startTerminalSessionParams
	var startCalls int
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		startTerminal: func(params startTerminalSessionParams) (terminalSession, error) {
			startCalls++
			started = params
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	result, err := app.StartLocalSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24)
	if err != nil {
		t.Fatalf("StartLocalSession failed: %v", err)
	}
	if result.Kind != string(sessionKindLocal) {
		t.Fatalf("expected kind %q, got %q", sessionKindLocal, result.Kind)
	}
	if startCalls != 1 {
		t.Fatalf("startTerminal called %d times, want 1", startCalls)
	}
	if started.Dir != projectRoot {
		t.Fatalf("unexpected start dir: %q", started.Dir)
	}
	if strings.TrimSpace(started.Executable) == "" {
		t.Fatalf("expected non-empty shell executable, got %q", started.Executable)
	}
}

func TestStartAISessionRunsErunOpenAsPersistentAITab(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]configpkg.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}

	var started startTerminalSessionParams
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(params startTerminalSessionParams) (terminalSession, error) {
			started = params
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	result, err := app.StartAISession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24, false)
	if err != nil {
		t.Fatalf("StartAISession failed: %v", err)
	}
	if result.Kind != string(sessionKindAI) {
		t.Fatalf("expected kind %q, got %q", sessionKindAI, result.Kind)
	}
	if started.Executable != "/tmp/erun" {
		t.Fatalf("expected erun executable, got %q", started.Executable)
	}
	// Claude launches pod-side on create, so a reopen pipes no initial input.
	wantArgs := []string{"open", "erun", "remote", "--app-session", "ai", "--ai"}
	if strings.Join(started.Args, "\n") != strings.Join(wantArgs, "\n") {
		t.Fatalf("unexpected args: got %+v want %+v", started.Args, wantArgs)
	}
	if len(started.InitialInput) != 0 {
		t.Fatalf("AI tab must not pipe an initial input (claude launches pod-side), got %q", string(started.InitialInput))
	}
}

func swapAIRepaintNudgeTimings(delay, settle time.Duration) func() {
	prevDelay, prevSettle := aiRepaintNudgeDelay, aiRepaintNudgeSettle
	aiRepaintNudgeDelay, aiRepaintNudgeSettle = delay, settle
	return func() { aiRepaintNudgeDelay, aiRepaintNudgeSettle = prevDelay, prevSettle }
}

// Claude repaints only on a geometry change, so a same-size reattach renders blank
// until the pty is nudged. The OSC 0 title set right before `dtach -A` marks the attach.
func TestAISessionRepaintNudgeFiresOnAttachMarker(t *testing.T) {
	defer swapAIRepaintNudgeTimings(time.Millisecond, time.Millisecond)()

	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]configpkg.TenantConfig{"erun": {Name: "erun", DefaultEnvironment: "remote"}},
		envs:    map[string]configpkg.EnvConfig{"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"}},
	}
	session := newStubTerminalSession()
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal:   func(startTerminalSessionParams) (terminalSession, error) { return session, nil },
	})
	defer app.shutdown(context.Background())

	managed := &managedTerminal{
		session:   session,
		selection: uiSelection{Tenant: "erun", Environment: "remote"},
		key:       aiSessionKey(uiSelection{Tenant: "erun", Environment: "remote"}, 0),
		serial:    1,
		kind:      sessionKindAI,
		lastCols:  80,
		lastRows:  24,
	}
	app.mu.Lock()
	app.sessions[managed.key] = managed
	app.mu.Unlock()

	var lastActivity time.Time
	app.handleSessionOutput(managed, []byte("audit: erun open --ai --app-session ai erun remote\n"), &lastActivity)
	time.Sleep(20 * time.Millisecond)
	if got := session.Resizes(); len(got) != 0 {
		t.Fatalf("non-marker output must not nudge; got resizes %v", got)
	}

	app.handleSessionOutput(managed, []byte("\x1b]0;erun-remote\x07"), &lastActivity)
	want := [][2]int{{80, 23}, {80, 24}}
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := session.Resizes()
		if len(got) >= 2 {
			if got[0] != want[0] || got[1] != want[1] {
				t.Fatalf("unexpected nudge resizes: got %v want %v", got, want)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("repaint nudge did not fire; resizes=%v", got)
		}
		time.Sleep(5 * time.Millisecond)
	}

	app.handleSessionOutput(managed, []byte("\x1b]0;erun-remote\x07"), &lastActivity)
	time.Sleep(30 * time.Millisecond)
	if got := session.Resizes(); len(got) != 2 {
		t.Fatalf("nudge must fire once per attach; got %v", got)
	}
}

func TestStartSessionAutoReconnectsOnExit(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]configpkg.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}

	var sessions []*stubTerminalSession
	var mu sync.Mutex
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			mu.Lock()
			sessions = append(sessions, session)
			mu.Unlock()
			return session, nil
		},
	})
	defer app.shutdown(context.Background())

	if _, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	mu.Lock()
	first := sessions[0]
	mu.Unlock()
	_ = first.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(sessions)
		mu.Unlock()
		if count >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	count := len(sessions)
	mu.Unlock()
	if count < 2 {
		t.Fatalf("expected reconnect to spawn a second session, got %d", count)
	}
}

func TestStartSessionDoesNotReconnectIntoStoppedCloudContext(t *testing.T) {
	// A respawn reruns CloudContextPreflight, which would undo an auto-stop.
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &configpkg.ERunConfig{
			CloudContexts: []configpkg.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]configpkg.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-cloud",
				Type:              configpkg.EnvironmentTypeRuntime,
			},
		},
	}

	var sessions []*stubTerminalSession
	var mu sync.Mutex
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			mu.Lock()
			sessions = append(sessions, session)
			mu.Unlock()
			return session, nil
		},
	})
	defer app.shutdown(context.Background())
	app.setCloudContextStatusInCache("managed-cloud", cloud.CloudContextStatusStopped)

	if _, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	mu.Lock()
	first := sessions[0]
	mu.Unlock()
	_ = first.Close()

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(sessions)
		mu.Unlock()
		if count > 1 {
			t.Fatalf("expected no respawn against stopped cloud context, got %d sessions", count)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStartAISessionRespawnsAfterStoppedCloudContextDeath(t *testing.T) {
	// After an auto-stop tears down the AI session, StartAISession must return a new
	// serial so the dead tab's click-to-respawn works.
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &configpkg.ERunConfig{
			CloudContexts: []configpkg.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]configpkg.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-cloud",
				Type:              configpkg.EnvironmentTypeRuntime,
			},
		},
	}

	var sessions []*stubTerminalSession
	var mu sync.Mutex
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			mu.Lock()
			sessions = append(sessions, session)
			mu.Unlock()
			return session, nil
		},
	})
	defer app.shutdown(context.Background())
	app.setCloudContextStatusInCache("managed-cloud", cloud.CloudContextStatusStopped)

	selection := uiSelection{Tenant: "erun", Environment: "remote"}
	first, err := app.StartAISession(selection, 0, 80, 24, false)
	if err != nil {
		t.Fatalf("first StartAISession failed: %v", err)
	}

	mu.Lock()
	deadSession := sessions[0]
	mu.Unlock()
	_ = deadSession.Close()

	key := aiSessionKey(selection, 0)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		app.mu.Lock()
		_, present := app.sessions[key]
		app.mu.Unlock()
		if !present {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	app.mu.Lock()
	_, stillPresent := app.sessions[key]
	app.mu.Unlock()
	if stillPresent {
		t.Fatalf("expected streamSession to drop the dead AI session from a.sessions after the cloud-context gate refused respawn")
	}

	second, err := app.StartAISession(selection, 0, 80, 24, false)
	if err != nil {
		t.Fatalf("second StartAISession failed: %v", err)
	}
	if second.SessionID == first.SessionID {
		t.Fatalf("expected a fresh session id after restart, got the same serial %d", second.SessionID)
	}
	mu.Lock()
	count := len(sessions)
	mu.Unlock()
	if count < 2 {
		t.Fatalf("expected a second PTY spawn after click-driven respawn, got %d", count)
	}
}

func TestStartSessionLogsOpenCommandToLocal(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]configpkg.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}

	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	if _, err := app.StartLocalSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartLocalSession failed: %v", err)
	}
	if _, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if _, err := app.StartAISession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24, false); err != nil {
		t.Fatalf("StartAISession failed: %v", err)
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	var local *managedTerminal
	for _, m := range app.sessions {
		if m != nil && m.kind == sessionKindLocal {
			local = m
			break
		}
	}
	if local == nil {
		t.Fatalf("Local session not found")
	}
	if _, ok := local.loggedCommands["erun"]; !ok {
		t.Fatalf("expected ERun command logged to Local, got %+v", local.loggedCommands)
	}
	if _, ok := local.loggedCommands["ai"]; !ok {
		t.Fatalf("expected AI command logged to Local, got %+v", local.loggedCommands)
	}
}

func TestStartSessionDoesNotLogWhenLocalAbsent(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]configpkg.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}

	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	if _, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
}

func TestStartLocalSessionDoesNotAutoReconnect(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]configpkg.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}

	var sessions []*stubTerminalSession
	var mu sync.Mutex
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			mu.Lock()
			sessions = append(sessions, session)
			mu.Unlock()
			return session, nil
		},
	})
	defer app.shutdown(context.Background())

	if _, err := app.StartLocalSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartLocalSession failed: %v", err)
	}

	mu.Lock()
	first := sessions[0]
	mu.Unlock()
	_ = first.Close()

	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	count := len(sessions)
	mu.Unlock()
	if count != 1 {
		t.Fatalf("local session should not auto-reconnect; got %d spawn(s)", count)
	}
}
