package main

import (
	"context"
	"sync"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// These tests lock the env-status event contract behind the sidebar's open
// dot (issue #470): tab presence alone is not running-ness, so the desktop
// flags the row "stopped" when reconnect is refused because the linked cloud
// context is not running, "failed" when reconnect gives up (deploy failure /
// loop guard), and clears the flag on every fresh open attempt and every
// successful respawn.

func envStatusTestApp(t *testing.T, emits *capturedEmits, sessionsMu *sync.Mutex, sessions *[]*stubTerminalSession) *App {
	t.Helper()
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &eruncommon.ERunConfig{
			CloudContexts: []eruncommon.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", RepoPath: projectRoot, KubernetesContext: "cluster-cloud"},
		},
	}
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			sessionsMu.Lock()
			*sessions = append(*sessions, session)
			sessionsMu.Unlock()
			return session, nil
		},
	})
	t.Cleanup(func() { app.shutdown(context.Background()) })
	app.SetEmitter(emits.fn())
	return app
}

// envStatuses decodes the captured env-status payloads in emission order.
func envStatuses(emits *capturedEmits) []envStatusPayload {
	var out []envStatusPayload
	for _, raw := range emits.events(envStatusEvent) {
		if payload, ok := raw.(envStatusPayload); ok {
			out = append(out, payload)
		}
	}
	return out
}

func waitForEnvStatus(t *testing.T, emits *capturedEmits, status string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, payload := range envStatuses(emits) {
			if payload.Status == status {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("env-status %q was not emitted; got %+v", status, envStatuses(emits))
}

func TestEnvStatusStoppedWhenReconnectRefusedForStoppedContext(t *testing.T) {
	var sessionsMu sync.Mutex
	var sessions []*stubTerminalSession
	emits := newCapturedEmits()
	app := envStatusTestApp(t, emits, &sessionsMu, &sessions)

	selection := uiSelection{Tenant: "erun", Environment: "remote"}
	if _, err := app.StartSession(selection, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	// The fresh open attempt clears any stale flag.
	waitForEnvStatus(t, emits, "", 2*time.Second)

	// An explicit Stop marks the env's cloud context intentionally stopped;
	// the session exit that follows must flag the row, not respawn.
	app.markIntentionalStopForCloudContext("managed-cloud")
	sessionsMu.Lock()
	current := sessions[0]
	sessionsMu.Unlock()
	_ = current.Close()

	waitForEnvStatus(t, emits, envStatusStopped, 2*time.Second)

	// No respawn happened for the stopped env.
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

	selection := uiSelection{Tenant: "erun", Environment: "remote"}
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

	waitForEnvStatus(t, emits, envStatusFailed, 2*time.Second)

	statuses := envStatuses(emits)
	last := statuses[len(statuses)-1]
	if last.Status != envStatusFailed {
		t.Fatalf("expected the final env-status to be failed, got %+v", statuses)
	}
	if last.Tenant != "erun" || last.Environment != "remote" {
		t.Fatalf("env-status carries the wrong selection: %+v", last)
	}
	// Each successful respawn before the cap cleared the flag.
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

// TestEnvStatusClearedByLaterSuccessfulDeploy locks the recovery path from
// issue #498: an env flagged 'failed' (amber dot, refused ERun-tab respawn)
// must drop the flag the moment a later deploy for it succeeds through the
// activity queue — e.g. an `erun upgrade` run — instead of keeping a stale
// failure on the row until the next manual row click.
func TestEnvStatusClearedByLaterSuccessfulDeploy(t *testing.T) {
	var sessionsMu sync.Mutex
	var sessions []*stubTerminalSession
	emits := newCapturedEmits()
	app := envStatusTestApp(t, emits, &sessionsMu, &sessions)

	app.activityQueue.start(activityQueueEntry{Command: "deploy", Tenant: "erun", Environment: "remote", Version: "1.0.1"})
	app.finishDeployByTenantEnv(uiSelection{}, "erun", "remote", activityQueueStatusSucceeded, "")

	waitForEnvStatus(t, emits, "", 2*time.Second)
	statuses := envStatuses(emits)
	last := statuses[len(statuses)-1]
	if last.Tenant != "erun" || last.Environment != "remote" || last.Status != "" {
		t.Fatalf("expected a clearing env-status for the deployed env, got %+v", statuses)
	}
}

// TestEnvStatusNotClearedByFailedDeploy is the negative guard: a deploy that
// finishes failed must not emit the clear.
func TestEnvStatusNotClearedByFailedDeploy(t *testing.T) {
	var sessionsMu sync.Mutex
	var sessions []*stubTerminalSession
	emits := newCapturedEmits()
	app := envStatusTestApp(t, emits, &sessionsMu, &sessions)

	app.activityQueue.start(activityQueueEntry{Command: "deploy", Tenant: "erun", Environment: "remote", Version: "1.0.1"})
	app.finishDeployByTenantEnv(uiSelection{}, "erun", "remote", activityQueueStatusFailed, "==> Deploy failed after 2m1s")

	if got := envStatuses(emits); len(got) != 0 {
		t.Fatalf("a failed deploy must not emit env-status, got %+v", got)
	}
}
