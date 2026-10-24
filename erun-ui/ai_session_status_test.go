package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
)

// aiSessionProbeOutput frames statuses the way aiSessionStatusScript output does.
func aiSessionProbeOutput(statuses ...model.AISessionStatus) string {
	if statuses == nil {
		// The CLI prints an empty list, never null.
		statuses = []model.AISessionStatus{}
	}
	data, err := json.Marshal(statuses)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s\tbegin\n%s\n%s\tend\n", aiSessionStatusMarker, data, aiSessionStatusMarker)
}

func reportedAISessionStatus(sessionID string, state model.AISessionState, at time.Time) model.AISessionStatus {
	return model.AISessionStatus{
		SessionID:    sessionID,
		Tool:         "claude",
		State:        state,
		Reason:       "reason",
		LastActivity: at,
	}
}

// newAISessionStatusTestApp's pod exec answers with output and records each script.
func newAISessionStatusTestApp(output string, execErr error) (*App, *capturedEmits, *[]string) {
	emits := newCapturedEmits()
	scripts := &[]string{}
	app := &App{
		sessions:          make(map[string]*managedTerminal),
		sessionHeartbeats: make(map[string]sessionHeartbeat),
		aiSessionReadings: make(map[string]aiSessionReading),
		emitFn:            emits.fn(),
	}
	app.deps = erunUIDeps{
		execRuntimePod: func(_ context.Context, _ uiSelection, script string) (string, error) {
			*scripts = append(*scripts, script)
			if execErr != nil {
				return "", execErr
			}
			return output, nil
		},
	}
	return app, emits, scripts
}

// aiStatusTestTab is latched busy by volume, now silent, with a live process.
func aiStatusTestTab(selection uiSelection) *managedTerminal {
	return &managedTerminal{
		kind:          sessionKindAI,
		selection:     selection,
		key:           "ai\x00" + selection.Tenant + "\x00" + selection.Environment,
		serial:        11,
		appSession:    "ai",
		aiBusyEmitted: true,
		aiLastOutput:  time.Now().Add(-time.Minute),
		startedAt:     time.Now().Add(-5 * time.Minute),
	}
}

// A reported awaiting-input boundary must release the badge even though the
// process is alive and output is silent.
func TestAwaitingInputReleasesTheBadgeOnALiveSession(t *testing.T) {
	selection := uiSelection{Tenant: "petios", Environment: "local"}
	tests := []struct {
		name        string
		state       model.AISessionState
		wantCleared bool
	}{
		{
			name:        "turn-end reads as awaiting input, so the badge clears",
			state:       model.AISessionStateAwaitingInput,
			wantCleared: true,
		},
		{
			name:        "a report of work in flight keeps the badge, though the stream is silent",
			state:       model.AISessionStateBusy,
			wantCleared: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app, emits, _ := newAISessionStatusTestApp("", nil)
			managed := aiStatusTestTab(selection)
			app.sessions[managed.key] = managed
			app.sessionHeartbeats[selectionKey(selection)] = heartbeatFor(true)
			app.recordAISessionReading(selection, []model.AISessionStatus{
				reportedAISessionStatus("conv-1", tc.state, time.Now()),
			})

			app.clearAIActivityIfQuiet(managed)

			cleared := len(emits.events(aiActivityEvent)) > 0
			if cleared != tc.wantCleared {
				t.Fatalf("badge cleared = %v, want %v; events: %+v",
					cleared, tc.wantCleared, emits.events(aiActivityEvent))
			}
			if managed.aiBusyEmitted == tc.wantCleared {
				t.Fatalf("latch state %v contradicts the emitted signal", managed.aiBusyEmitted)
			}
		})
	}
}

// A reported working state lights the badge without any output.
func TestModelReportedWorkLightsTheBadgeWithoutOutput(t *testing.T) {
	selection := uiSelection{Tenant: "petios", Environment: "local"}
	app, emits, scripts := newAISessionStatusTestApp(
		aiSessionProbeOutput(reportedAISessionStatus("conv-1", model.AISessionStateBusy, time.Now())), nil)
	managed := aiStatusTestTab(selection)
	managed.aiBusyEmitted = false
	managed.aiLastOutput = time.Time{}
	app.sessions[managed.key] = managed

	app.reconcileAISessionStatusesOnce()

	events := emits.events(aiActivityEvent)
	if len(events) != 1 {
		t.Fatalf("expected one busy=true emit from the report alone, got %+v", events)
	}
	payload, ok := events[0].(aiActivityPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", events[0])
	}
	if !payload.Busy || payload.Tenant != selection.Tenant || payload.Environment != selection.Environment {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if !managed.aiModelLatch {
		t.Fatalf("a latch the tool raised must be marked as such, so pod liveness does not hold it open")
	}
	// The read names its target explicitly.
	if len(*scripts) != 1 {
		t.Fatalf("expected exactly one pod read, got %d", len(*scripts))
	}
	for _, want := range []string{"erun activity ai-session status", "--tenant 'petios'", "--environment 'local'"} {
		if !strings.Contains((*scripts)[0], want) {
			t.Fatalf("read script %q is missing %q", (*scripts)[0], want)
		}
	}
}

// Where the tool reports state, repaint volume must not raise the badge.
func TestReportedStateOutranksVolumeWhenTheToolRepaints(t *testing.T) {
	selection := uiSelection{Tenant: "petios", Environment: "local"}
	tests := []struct {
		name       string
		reporting  bool
		wantEmit   bool
		wantLatch  bool
		reportNote string
	}{
		{
			name:       "a reporting tool's redraws do not raise the badge",
			reporting:  true,
			wantEmit:   false,
			wantLatch:  false,
			reportNote: "the report says awaiting-input, and it outranks the redraws",
		},
		{
			name:       "with no report at all the volume rule still raises it",
			reporting:  false,
			wantEmit:   true,
			wantLatch:  true,
			reportNote: "the fallback the badge has always had",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app, emits, _ := newAISessionStatusTestApp("", nil)
			managed := aiStatusTestTab(selection)
			managed.aiBusyEmitted = false
			managed.aiActiveSince = time.Now().Add(-(aiActivitySustainedThreshold + time.Second))
			managed.aiLastOutput = time.Now()
			app.sessions[managed.key] = managed
			if tc.reporting {
				app.recordAISessionReading(selection, []model.AISessionStatus{
					reportedAISessionStatus("conv-1", model.AISessionStateAwaitingInput, time.Now()),
				})
			}

			app.recordAIActivity(managed)
			if managed.aiInactivityTimer != nil {
				managed.aiInactivityTimer.Stop()
			}

			events := emits.events(aiActivityEvent)
			if got := len(events) > 0; got != tc.wantEmit {
				t.Fatalf("emitted = %v, want %v (%s); events: %+v", got, tc.wantEmit, tc.reportNote, events)
			}
			if managed.aiBusyEmitted != tc.wantLatch {
				t.Fatalf("latch = %v, want %v (%s)", managed.aiBusyEmitted, tc.wantLatch, tc.reportNote)
			}
		})
	}
}

// Stale, empty, failed, and unparseable reads all leave the volume latch alone.
func TestAISessionStatusLeavesTheVolumeLatchAloneWhenItSaysNothing(t *testing.T) {
	selection := uiSelection{Tenant: "petios", Environment: "local"}
	tests := []struct {
		name    string
		output  string
		execErr error
		reading []model.AISessionStatus
	}{
		{
			name:   "nothing recorded for the environment",
			output: aiSessionProbeOutput(),
		},
		{
			name: "a record from before this tab existed belongs to another session",
			reading: []model.AISessionStatus{
				reportedAISessionStatus("older", model.AISessionStateAwaitingInput, time.Now().Add(-time.Hour)),
			},
		},
		{
			name:    "the read failed",
			execErr: errors.New("pod unreachable"),
		},
		{
			name:   "the read did not parse",
			output: aiSessionStatusMarker + "\tbegin\nnot json\n" + aiSessionStatusMarker + "\tend\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app, emits, _ := newAISessionStatusTestApp(tc.output, tc.execErr)
			managed := aiStatusTestTab(selection)
			app.sessions[managed.key] = managed
			app.sessionHeartbeats[selectionKey(selection)] = heartbeatFor(true)
			if tc.reading != nil {
				app.recordAISessionReading(selection, tc.reading)
			}

			app.reconcileAISessionStatusesOnce()
			if events := emits.events(aiActivityEvent); len(events) != 0 {
				t.Fatalf("no evidence must leave the volume latch untouched, got %+v", events)
			}
			if !managed.aiBusyEmitted {
				t.Fatalf("the volume latch must survive a reading that says nothing")
			}
		})
	}
}

// A tool-raised latch must clear when reports stop, even with the process alive.
func TestModelLatchIsReleasedWhenItsReportStopsArriving(t *testing.T) {
	selection := uiSelection{Tenant: "petios", Environment: "local"}
	app, emits, _ := newAISessionStatusTestApp("", nil)
	managed := aiStatusTestTab(selection)
	managed.aiBusyEmitted = false
	managed.aiLastOutput = time.Time{}
	app.sessions[managed.key] = managed
	app.sessionHeartbeats[selectionKey(selection)] = heartbeatFor(true)

	app.recordAISessionReading(selection, []model.AISessionStatus{
		reportedAISessionStatus("conv-1", model.AISessionStateBusy, time.Now()),
	})
	app.latchAIActivity(managed)
	if len(emits.events(aiActivityEvent)) != 1 {
		t.Fatalf("expected the report to raise the latch, got %+v", emits.events(aiActivityEvent))
	}

	reading := app.aiSessionReadings[selectionKey(selection)]
	reading.observedAt = time.Now().Add(-2 * aiSessionStatusTTL)
	app.aiSessionReadings[selectionKey(selection)] = reading

	app.releaseUnobservedAIActivity()

	events := emits.events(aiActivityEvent)
	if len(events) != 2 {
		t.Fatalf("a latch with no report left to re-assert it must be released, got %+v", events)
	}
	if payload, ok := events[1].(aiActivityPayload); !ok || payload.Busy {
		t.Fatalf("expected a busy=false emit, got %+v", events[1])
	}
	if managed.aiBusyEmitted || managed.aiModelLatch {
		t.Fatalf("latch and its origin must both clear")
	}
}

func TestParseAISessionStatuses(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		wantOK     bool
		wantStates []model.AISessionState
	}{
		{
			name:       "a marked array",
			output:     aiSessionProbeOutput(reportedAISessionStatus("conv-1", model.AISessionStateAwaitingInput, time.Unix(100, 0).UTC())),
			wantOK:     true,
			wantStates: []model.AISessionState{model.AISessionStateAwaitingInput},
		},
		{
			name:   "an empty environment is a reading, not a failure",
			output: aiSessionProbeOutput(),
			wantOK: true,
		},
		{
			name:       "trace output around the array is dropped",
			output:     "==> Resolving\n" + aiSessionProbeOutput(reportedAISessionStatus("conv-1", model.AISessionStateBusy, time.Unix(100, 0).UTC())) + "done\n",
			wantOK:     true,
			wantStates: []model.AISessionState{model.AISessionStateBusy},
		},
		{
			name:   "no markers at all",
			output: "erun: command not found\n",
		},
		{
			name:   "markers around something that is not an array",
			output: aiSessionStatusMarker + "\tbegin\nwarning: nothing\n" + aiSessionStatusMarker + "\tend\n",
		},
		{
			name:   "empty output",
			output: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			statuses, ok := parseAISessionStatuses(tc.output)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if len(statuses) != len(tc.wantStates) {
				t.Fatalf("got %d statuses, want %d", len(statuses), len(tc.wantStates))
			}
			for i, want := range tc.wantStates {
				if statuses[i].State != want {
					t.Fatalf("status %d state = %q, want %q", i, statuses[i].State, want)
				}
			}
		})
	}
}
