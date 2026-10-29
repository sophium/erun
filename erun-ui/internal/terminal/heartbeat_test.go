package terminal

import (
	"testing"
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestSessionRunningFollowsHeartbeatNotStreamSilence(t *testing.T) {
	tests := []struct {
		name          string
		podRunning    bool
		wantBusyClear bool
	}{
		{
			name:          "quiet session whose program is alive keeps running",
			podRunning:    true,
			wantBusyClear: false,
		},
		{
			name:          "quiet session whose program is gone stops claiming to run",
			podRunning:    false,
			wantBusyClear: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			emits := newCapturedEmits()
			app := newTestService(Deps{Emit: emits.fn()})
			selection := uimodel.Selection{Tenant: "petios", Environment: "local"}
			managed := &Session{
				Kind:       KindAI,
				Selection:  selection,
				Key:        "ai\x00petios\x00local",
				Serial:     3,
				AppSession: "ai",
				// Busy and silent past the idle threshold.
				aiBusyEmitted: true,
				aiLastOutput:  time.Now().Add(-time.Minute),
			}
			app.sessions[managed.Key] = managed
			app.sessionHeartbeats[uimodel.SelectionKey(selection)] = heartbeatFor(tc.podRunning)

			app.clearAIActivityIfQuiet(managed)

			cleared := len(emits.events(uimodel.AIActivityEvent)) > 0
			if cleared != tc.wantBusyClear {
				t.Fatalf("busy cleared = %v, want %v (events: %+v)", cleared, tc.wantBusyClear, emits.events(uimodel.AIActivityEvent))
			}
			if managed.aiBusyEmitted == tc.wantBusyClear {
				t.Fatalf("latch state %v contradicts the emitted signal", managed.aiBusyEmitted)
			}
		})
	}
}

func TestHeartbeatReleasesBusyWhenThePodReportsTheSessionFinished(t *testing.T) {
	emits := newCapturedEmits()
	selection := uimodel.Selection{Tenant: "petios", Environment: "local"}
	app := newTestService(Deps{Emit: emits.fn()})
	managed := &Session{
		Kind:          KindAI,
		Selection:     selection,
		Key:           "ai\x00petios\x00local",
		Serial:        4,
		AppSession:    "ai",
		aiBusyEmitted: true,
		aiLastOutput:  time.Now(),
	}
	app.sessions[managed.Key] = managed

	app.applySessionHeartbeat(selection, []PodSession{{ID: "ai", Running: true}})
	if len(emits.events(uimodel.AIActivityEvent)) != 0 {
		t.Fatalf("a running session must not be released: %+v", emits.events(uimodel.AIActivityEvent))
	}

	app.applySessionHeartbeat(selection, []PodSession{{ID: "ai", Running: false}})
	if len(emits.events(uimodel.AIActivityEvent)) != 1 {
		t.Fatalf("a finished session must be released once: %+v", emits.events(uimodel.AIActivityEvent))
	}
	if managed.aiBusyEmitted {
		t.Fatalf("latch must be down after the pod reports the program gone")
	}
}

func TestHeartbeatObservationExpires(t *testing.T) {
	selection := uimodel.Selection{Tenant: "petios", Environment: "local"}
	app := newTestService(Deps{})
	managed := &Session{
		Kind:       KindAI,
		Selection:  selection,
		AppSession: "ai",
	}
	stale := heartbeatFor(true)
	stale.observedAt = time.Now().Add(-2 * sessionHeartbeatTTL)
	app.sessionHeartbeats[uimodel.SelectionKey(selection)] = stale

	if app.heartbeatSaysRunning(managed) {
		t.Fatalf("an expired observation must not keep a session marked running")
	}
}

func TestSelectionsWithPodSessionsSkipsSessionsWithoutAPodSession(t *testing.T) {
	app := newTestService(Deps{})
	app.sessions["local"] = &Session{Kind: KindLocal, Selection: uimodel.Selection{Tenant: "petios", Environment: "local"}}
	app.sessions["ai"] = &Session{Kind: KindAI, AppSession: "ai", Selection: uimodel.Selection{Tenant: "petios", Environment: "remote"}}
	app.sessions["open"] = &Session{Kind: KindOpen, AppSession: "open-0", Selection: uimodel.Selection{Tenant: "petios", Environment: "remote"}}

	selections := app.selectionsWithPodSessions()
	if len(selections) != 1 || selections[0].Environment != "remote" {
		t.Fatalf("expected only the env with pod sessions, got %+v", selections)
	}
}

func heartbeatFor(running bool) sessionHeartbeat {
	heartbeat := sessionHeartbeat{
		observedAt: time.Now(),
		running:    map[string]struct{}{},
		sessions:   map[string]struct{}{"ai": {}},
	}
	if running {
		heartbeat.running["ai"] = struct{}{}
	}
	return heartbeat
}
