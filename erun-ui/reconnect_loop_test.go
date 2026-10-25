package main

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/open/appsession"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// The on-disk status gate races the poller, so Stop records an intent marker.
func TestStopCloudContextSuppressesReconnect(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &config.ERunConfig{
			CloudContexts: []model.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]config.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-cloud",
				Type:              config.EnvironmentTypeRuntime,
			},
		},
	}

	var sessionsMu sync.Mutex
	var sessions []*stubTerminalSession
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(terminal.StartParams) (terminal.PTY, error) {
			session := newStubTerminalSession()
			sessionsMu.Lock()
			sessions = append(sessions, session)
			sessionsMu.Unlock()
			return session, nil
		},
		// The cache stays running, so only the intent marker can block the respawn.
		stopCloudContext: func(_ context.Context, name string) (model.CloudContextStatus, error) {
			return model.CloudContextStatus{
				CloudContextConfig: model.CloudContextConfig{Name: name, KubernetesContext: "cluster-cloud"},
				Status:             "stopping",
			}, nil
		},
	})
	defer app.shutdown(context.Background())
	app.cloudContexts.SetStatus("managed-cloud", cloud.CloudContextStatusRunning)

	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}
	if _, err := app.StartSession(selection, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	waitForSessionCount(t, &sessionsMu, &sessions, 1, 2*time.Second)

	if _, err := app.StopCloudContext("managed-cloud"); err != nil {
		t.Fatalf("StopCloudContext failed: %v", err)
	}

	sessionsMu.Lock()
	first := sessions[0]
	sessionsMu.Unlock()
	_ = first.Close()

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		sessionsMu.Lock()
		count := len(sessions)
		sessionsMu.Unlock()
		if count > 1 {
			t.Fatalf("StopCloudContext intent marker did not block reconnect; got %d sessions", count)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestClearIntentionalStopForCloudContext(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &config.ERunConfig{
			CloudContexts: []model.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]config.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-cloud",
				Type:              config.EnvironmentTypeRuntime,
			},
		},
	}

	app := NewApp(erunUIDeps{
		store: store,
		stopCloudContext: func(_ context.Context, name string) (model.CloudContextStatus, error) {
			return model.CloudContextStatus{
				CloudContextConfig: model.CloudContextConfig{Name: name, KubernetesContext: "cluster-cloud"},
				Status:             "stopping",
			}, nil
		},
	})
	defer app.shutdown(context.Background())
	app.cloudContexts.SetStatus("managed-cloud", cloud.CloudContextStatusRunning)

	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}

	if _, err := app.StopCloudContext("managed-cloud"); err != nil {
		t.Fatalf("StopCloudContext failed: %v", err)
	}
	if !app.terminal.IsIntentionalStop(selection) {
		t.Fatal("expected intentional-stop marker after StopCloudContext")
	}

	app.terminal.ClearIntentionalStopForCloudContext("managed-cloud")
	if app.terminal.IsIntentionalStop(selection) {
		t.Fatal("expected intentional-stop marker to clear after clearIntentionalStopForCloudContext")
	}
}

// A failed Stop leaves the cluster up, so reconnect must stay enabled.
func TestStopCloudContextErrorClearsIntentionalStop(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &config.ERunConfig{
			CloudContexts: []model.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]config.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-cloud",
				Type:              config.EnvironmentTypeRuntime,
			},
		},
	}

	wantErr := errors.New("aws stop failed")
	app := NewApp(erunUIDeps{
		store: store,
		stopCloudContext: func(_ context.Context, _ string) (model.CloudContextStatus, error) {
			return model.CloudContextStatus{}, wantErr
		},
	})
	defer app.shutdown(context.Background())
	app.cloudContexts.SetStatus("managed-cloud", cloud.CloudContextStatusRunning)

	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}
	if _, err := app.StopCloudContext("managed-cloud"); err == nil {
		t.Fatal("expected StopCloudContext to surface stub error")
	}
	if app.terminal.IsIntentionalStop(selection) {
		t.Fatal("expected intentional-stop marker to clear after StopCloudContext error")
	}
}

const takenOverMarkerNeedle = "re-attached in another ERun window"

// Respawning would steal the session back; the window shows take-back instead.
func TestSessionTakenOverByAnotherWindowDoesNotReconnect(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]config.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}

	var sessionsMu sync.Mutex
	var sessions []*stubTerminalSession
	emits := newCapturedEmits()
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(terminal.StartParams) (terminal.PTY, error) {
			session := newStubTerminalSession()
			session.initialOutput = append(session.initialOutput,
				[]byte(appsession.ShellSessionTakenOverNotice+"\n")...)
			sessionsMu.Lock()
			sessions = append(sessions, session)
			sessionsMu.Unlock()
			return session, nil
		},
	})
	defer app.shutdown(context.Background())
	app.SetEmitter(emits.fn())

	if _, err := app.StartSession(uimodel.Selection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	waitForSessionCount(t, &sessionsMu, &sessions, 1, 2*time.Second)

	waitForTakenOverFlag(t, app, 2*time.Second)
	sessionsMu.Lock()
	current := sessions[0]
	sessionsMu.Unlock()
	_ = current.Close()

	waitForTerminalMarker(t, emits, takenOverMarkerNeedle, 10*time.Second)

	sessionsMu.Lock()
	got := len(sessions)
	sessionsMu.Unlock()
	if got != 1 {
		t.Fatalf("takeover must not respawn: expected 1 session, got %d", got)
	}
}

func waitForTakenOverFlag(t *testing.T, app *App, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var managed []*terminal.Session
		app.terminal.WithLock(func(l *terminal.Locked) {
			for _, session := range l.Sessions() {
				managed = append(managed, session)
			}
		})
		for _, session := range managed {
			if session != nil && app.terminal.TakenOver(session) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the taken-over notice to be scanned from session output")
}

// waitForTerminalMarker polls for a marker emitted from the session's goroutine.
func waitForTerminalMarker(t *testing.T, emits *capturedEmits, needle string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if sawTerminalMarker(emits, needle) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for the %q marker on the terminal-output channel", needle)
}

func sawTerminalMarker(emits *capturedEmits, needle string) bool {
	for _, evt := range emits.events(uimodel.TerminalOutputEvent) {
		payload, ok := evt.(uimodel.TerminalOutputPayload)
		if !ok {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(payload.Data)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), needle) {
			return true
		}
	}
	return false
}
