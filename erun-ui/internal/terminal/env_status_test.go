package terminal

import (
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Tab presence alone is not running-ness.

func envStatusTestApp(t *testing.T, emits *capturedEmits, sessionsMu *sync.Mutex, sessions *[]*stubTerminalSession) *Service {
	t.Helper()
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
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "cluster-cloud"},
		},
	}
	app := newTestService(Deps{
		Store:           store,
		Emit:            emits.fn(),
		FindProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		ResolveCLIPath:  func() string { return "/tmp/erun" },
		StartTerminal: func(StartParams) (PTY, error) {
			session := newStubTerminalSession()
			sessionsMu.Lock()
			*sessions = append(*sessions, session)
			sessionsMu.Unlock()
			return session, nil
		},
	})
	t.Cleanup(func() { closeTestService(app) })
	return app
}

func envStatuses(emits *capturedEmits) []uimodel.EnvStatusPayload {
	var out []uimodel.EnvStatusPayload
	for _, raw := range emits.events(uimodel.EnvStatusEvent) {
		if payload, ok := raw.(uimodel.EnvStatusPayload); ok {
			out = append(out, payload)
		}
	}
	return out
}

// envStatusWaitBound is a safety net, not a budget: waits return on the emit.
const envStatusWaitBound = 30 * time.Second

func waitForEnvStatus(t *testing.T, emits *capturedEmits, status string) {
	t.Helper()
	arrived := emits.waitFor(envStatusWaitBound, func(byName map[string][]any) bool {
		for _, raw := range byName[uimodel.EnvStatusEvent] {
			if payload, ok := raw.(uimodel.EnvStatusPayload); ok && payload.Status == status {
				return true
			}
		}
		return false
	})
	if !arrived {
		t.Fatalf("env-status %q was not emitted; got %+v", status, envStatuses(emits))
	}
}

func TestEnvStatusStoppedWhenReconnectRefusedForStoppedContext(t *testing.T) {
	var sessionsMu sync.Mutex
	var sessions []*stubTerminalSession
	emits := newCapturedEmits()
	app := envStatusTestApp(t, emits, &sessionsMu, &sessions)

	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}
	if _, err := app.StartSession(selection, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	waitForEnvStatus(t, emits, "")

	// After an intentional Stop, the session exit flags the row instead of respawning.
	app.MarkIntentionalStopForCloudContext("managed-cloud")
	sessionsMu.Lock()
	current := sessions[0]
	sessionsMu.Unlock()
	_ = current.Close()

	waitForEnvStatus(t, emits, uimodel.EnvStatusStopped)

	sessionsMu.Lock()
	got := len(sessions)
	sessionsMu.Unlock()
	if got != 1 {
		t.Fatalf("expected no respawn for a stopped context, got %d sessions", got)
	}
}

func TestEnvStatusFailedAfterReconnectLoopCapAndClearedByRespawns(t *testing.T) {
	var sessionsMu sync.Mutex
	var sessions []*stubTerminalSession
	emits := newCapturedEmits()
	app := envStatusTestApp(t, emits, &sessionsMu, &sessions)

	selection := uimodel.Selection{Tenant: "erun", Environment: "remote"}
	if _, err := app.StartSession(selection, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	const closes = reconnectLoopMaxExits + 1
	for i := 0; i < closes; i++ {
		expected := i + 1
		waitForSessionCount(t, &sessionsMu, &sessions, expected, 2*time.Second)
		sessionsMu.Lock()
		current := sessions[expected-1]
		sessionsMu.Unlock()
		_ = current.Close()
	}

	waitForEnvStatus(t, emits, uimodel.EnvStatusFailed)

	statuses := envStatuses(emits)
	last := statuses[len(statuses)-1]
	if last.Status != uimodel.EnvStatusFailed {
		t.Fatalf("expected the final env-status to be failed, got %+v", statuses)
	}
	if last.Tenant != "erun" || last.Environment != "remote" {
		t.Fatalf("env-status carries the wrong selection: %+v", last)
	}
	clears := 0
	for _, payload := range statuses {
		if payload.Status == "" {
			clears++
		}
	}
	if clears < reconnectLoopMaxExits {
		t.Fatalf("expected at least %d clearing emissions (initial open + respawns), got %d in %+v", reconnectLoopMaxExits, clears, statuses)
	}
}
