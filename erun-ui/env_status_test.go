package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Tab presence alone is not running-ness.

func envStatusTestApp(t *testing.T, emits *capturedEmits, sessionsMu *sync.Mutex, sessions *[]*stubTerminalSession) *App {
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
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(terminal.StartParams) (terminal.PTY, error) {
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

func TestEnvStatusClearedByLaterSuccessfulDeploy(t *testing.T) {
	var sessionsMu sync.Mutex
	var sessions []*stubTerminalSession
	emits := newCapturedEmits()
	app := envStatusTestApp(t, emits, &sessionsMu, &sessions)

	app.activityQueue.start(activityQueueEntry{Command: "deploy", Tenant: "erun", Environment: "remote", Version: "1.0.1"})
	app.finishDeployByTenantEnv(uimodel.Selection{}, "erun", "remote", activityQueueStatusSucceeded, "")

	waitForEnvStatus(t, emits, "")
	statuses := envStatuses(emits)
	last := statuses[len(statuses)-1]
	if last.Tenant != "erun" || last.Environment != "remote" || last.Status != "" {
		t.Fatalf("expected a clearing env-status for the deployed env, got %+v", statuses)
	}
}

func TestEnvStatusNotClearedByFailedDeploy(t *testing.T) {
	var sessionsMu sync.Mutex
	var sessions []*stubTerminalSession
	emits := newCapturedEmits()
	app := envStatusTestApp(t, emits, &sessionsMu, &sessions)

	app.activityQueue.start(activityQueueEntry{Command: "deploy", Tenant: "erun", Environment: "remote", Version: "1.0.1"})
	app.finishDeployByTenantEnv(uimodel.Selection{}, "erun", "remote", activityQueueStatusFailed, "==> Deploy failed after 2m1s")

	if got := envStatuses(emits); len(got) != 0 {
		t.Fatalf("a failed deploy must not emit env-status, got %+v", got)
	}
}
