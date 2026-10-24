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
	// The AI tab runs a pure `erun open --ai`: claude launches pod-side (once on
	// create), so a reopen reconnects to the running tool and the desktop pipes no
	// initial input. open is pure — no --skip-ensure — so deploy stays the
	// caller's job.
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

// TestAISessionRepaintNudgeFiresOnAttachMarker pins the fix: an AI tab
// reattaching to a running Claude renders blank because Claude (a main-screen
// TUI) only repaints on a real geometry change, and a same-size reattach
// raises none. The desktop forces the repaint by briefly resizing the pty one
// row shorter then back. The attach is detected by the bootstrap's window-title
// escape (OSC 0), emitted right before `dtach -A` — nudging on the first output
// overall would fire too early (it is the `erun open` audit trace). Verified
// end-to-end against a live pod via the headless app + DOM read; this locks the
// backend contract.
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
	// Output without the attach marker (the open audit trace) must not nudge.
	app.handleSessionOutput(managed, []byte("audit: erun open --ai --app-session ai erun remote\n"), &lastActivity)
	time.Sleep(20 * time.Millisecond)
	if got := session.Resizes(); len(got) != 0 {
		t.Fatalf("non-marker output must not nudge; got resizes %v", got)
	}

	// The attach marker (OSC 0 window-title set) triggers shrink-then-restore.
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

	// The nudge is once per attach: a second marker chunk must not re-fire it.
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
	// The respawn loop used to re-launch `erun open` on every PTY exit,
	// even when the env's cloud context had just been auto-stopped. Each
	// respawn re-ran CloudContextPreflight, which immediately undid the
	// stop. Gate the respawn on cloud-context state so the auto-stop is
	// not fought by the desktop's own reconnect machinery; the user
	// recovers via the titlebar start button.
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

	// Wait long enough that a respawn would have shown up if the gate
	// were broken; the existing reconnect test races for ~2s, so the
	// same window here would surface a regression.
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
	// When the linked cloud context auto-stops while a Claude/codex AI
	// session is attached, `tryReconnect` refuses to fight the stop and
	// `streamSession` cleans up the managed session (closed=true, removed
	// from a.sessions). The desktop's tab-respawn flow then calls
	// StartAISession again from the user's click on the dead AI tab; the
	// backend must respond with a brand new session instead of returning
	// the stale serial. This pins that contract so the click-driven
	// recovery in tabRespawnThunks does not regress into a no-op.
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

	// Wait until streamSession finishes cleaning up the dead managed
	// terminal. The shouldRespawnForCloudContext gate is what makes this
	// observable: it sees the stopped context, refuses to respawn, and
	// drops the entry from a.sessions.
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
	// Should not crash. No assertion required beyond that.
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
