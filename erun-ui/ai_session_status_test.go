package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// aiSessionProbeOutput builds the pod exec's combined output the way
// aiSessionStatusScript does: the CLI's JSON array between the begin and end
// markers.
func aiSessionProbeOutput(statuses ...eruncommon.AISessionStatus) string {
	if statuses == nil {
		// The CLI prints an empty list for an environment with nothing
		// recorded, never null.
		statuses = []eruncommon.AISessionStatus{}
	}
	data, err := json.Marshal(statuses)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s\tbegin\n%s\n%s\tend\n", aiSessionStatusMarker, data, aiSessionStatusMarker)
}

// reportedAISessionStatus is one resolved status the tool reported at a given
// time, as erun-common's own resolver would produce it.
func reportedAISessionStatus(sessionID string, state eruncommon.AISessionState, at time.Time) eruncommon.AISessionStatus {
	return eruncommon.AISessionStatus{
		SessionID:    sessionID,
		Tool:         "claude",
		State:        state,
		Reason:       "reason",
		LastActivity: at,
	}
}

// newAISessionStatusTestApp is an App holding the three maps the badge's
// decision path touches, with a pod exec that answers with one environment's
// reading and records the script it was asked to run.
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

// aiStatusTestTab is a latched-busy AI tab whose pod session is still running:
// the state the reported defect described. The latch was raised by output
// volume, the stream has since gone quiet, and the claude process is alive.
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

// TestAwaitingInputReleasesTheBadgeOnALiveSession is the reproduction of the
// defect: an AI tool that reported a turn boundary — its turn ended, and it is
// waiting on the operator — was still shown as working, because the only two
// inputs the badge had were output volume (silent, so no opinion) and the pod
// heartbeat (the claude process is alive, so "still running"). Neither can tell
// a blocked session from a working one; the tool's own report can, and the
// badge must take it.
func TestAwaitingInputReleasesTheBadgeOnALiveSession(t *testing.T) {
	selection := uiSelection{Tenant: "petios", Environment: "local"}
	tests := []struct {
		name        string
		state       eruncommon.AISessionState
		wantCleared bool
	}{
		{
			name:        "turn-end reads as awaiting input, so the badge clears",
			state:       eruncommon.AISessionStateAwaitingInput,
			wantCleared: true,
		},
		{
			name:        "a report of work in flight keeps the badge, though the stream is silent",
			state:       eruncommon.AISessionStateBusy,
			wantCleared: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app, emits, _ := newAISessionStatusTestApp("", nil)
			managed := aiStatusTestTab(selection)
			app.sessions[managed.key] = managed
			// The pod reports the session still running, which is what held the
			// latch open through every silence the tab produced.
			app.sessionHeartbeats[selectionKey(selection)] = heartbeatFor(true)
			app.recordAISessionReading(selection, []eruncommon.AISessionStatus{
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

// emittedAIActivity reads an emitted ai-activity event back as the JSON the
// frontend actually receives. The assertions below are on the wire shape rather
// than on the payload struct on purpose: the struct field is the thing being
// added, so a test written against it could not have been run against the code
// the defect was in.
func emittedAIActivity(t *testing.T, event any) map[string]any {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal the emitted event: %v", err)
	}
	var emitted map[string]any
	if err := json.Unmarshal(data, &emitted); err != nil {
		t.Fatalf("read the emitted event back: %v", err)
	}
	return emitted
}

// TestAnAwaitingInputReportReachesTheSidebarAsItsOwnState is the reproduction of
// the defect this change fixes. The model resolved awaiting-input and the badge
// consumed it, releasing the spinner — correctly, the turn is not in flight —
// and then had nothing left to say: its payload was a boolean, so the one state
// the model exists to carry arrived at the renderer as its opposite,
// indistinguishable from a session that had merely gone quiet. A consumer
// cannot render a state the event does not name.
func TestAnAwaitingInputReportReachesTheSidebarAsItsOwnState(t *testing.T) {
	selection := uiSelection{Tenant: "petios", Environment: "local"}
	app, emits, _ := newAISessionStatusTestApp("", nil)
	managed := aiStatusTestTab(selection)
	app.sessions[managed.key] = managed
	app.sessionHeartbeats[selectionKey(selection)] = heartbeatFor(true)
	app.recordAISessionReading(selection, []eruncommon.AISessionStatus{
		reportedAISessionStatus("conv-1", eruncommon.AISessionStateAwaitingInput, time.Now()),
	})

	app.reconcileAISessionStatusesOnce()

	events := emits.events(aiActivityEvent)
	if len(events) != 1 {
		t.Fatalf("expected one signal for the turn boundary, got %+v", events)
	}
	emitted := emittedAIActivity(t, events[0])
	if emitted["busy"] != false {
		t.Fatalf("a turn boundary is not work in flight; emitted %v", emitted)
	}
	if emitted["awaitingInput"] != true {
		t.Fatalf("awaiting-input reached the sidebar as 'not busy'; emitted %v", emitted)
	}

	// The state can be left as well as entered: the next turn hands control back
	// to the tool, and the row must stop saying it needs the operator.
	app.recordAISessionReading(selection, []eruncommon.AISessionStatus{
		reportedAISessionStatus("conv-1", eruncommon.AISessionStateBusy, time.Now()),
	})
	app.applyAISessionReading(managed)

	events = emits.events(aiActivityEvent)
	emitted = emittedAIActivity(t, events[len(events)-1])
	if emitted["busy"] != true {
		t.Fatalf("a turn start is work in flight; emitted %v", emitted)
	}
	if _, present := emitted["awaitingInput"]; present {
		t.Fatalf("a turn start must clear the awaiting state; emitted %v", emitted)
	}
}

// TestWorkInFlightOutranksASiblingSessionsTurnBoundary pins the reduction when
// an environment holds more than one AI conversation: the sidebar draws one
// badge for all of them, and reporting the quieter of two truths would show a
// row waiting on the operator while a turn in that same environment is still
// running.
func TestWorkInFlightOutranksASiblingSessionsTurnBoundary(t *testing.T) {
	selection := uiSelection{Tenant: "petios", Environment: "local"}
	app, emits, _ := newAISessionStatusTestApp("", nil)
	managed := aiStatusTestTab(selection)
	managed.aiBusyEmitted = false
	app.sessions[managed.key] = managed
	app.sessionHeartbeats[selectionKey(selection)] = heartbeatFor(true)
	app.recordAISessionReading(selection, []eruncommon.AISessionStatus{
		reportedAISessionStatus("conv-awaiting", eruncommon.AISessionStateAwaitingInput, time.Now()),
		reportedAISessionStatus("conv-busy", eruncommon.AISessionStateBusy, time.Now()),
	})

	app.reconcileAISessionStatusesOnce()

	events := emits.events(aiActivityEvent)
	if len(events) != 1 {
		t.Fatalf("expected one signal, got %+v", events)
	}
	emitted := emittedAIActivity(t, events[0])
	if emitted["busy"] != true {
		t.Fatalf("a turn in flight must win; emitted %v", emitted)
	}
	if _, present := emitted["awaitingInput"]; present {
		t.Fatalf("the row is working, not waiting; emitted %v", emitted)
	}
}

// TestModelReportedWorkLightsTheBadgeWithoutOutput pins the other direction:
// the badge is the tool's answer, not a threshold crossing. An agent inside a
// long silent tool call has produced nothing to measure, and the report is the
// only thing that can say it is working.
func TestModelReportedWorkLightsTheBadgeWithoutOutput(t *testing.T) {
	selection := uiSelection{Tenant: "petios", Environment: "local"}
	app, emits, scripts := newAISessionStatusTestApp(
		aiSessionProbeOutput(reportedAISessionStatus("conv-1", eruncommon.AISessionStateBusy, time.Now())), nil)
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
	// The read names its target explicitly rather than relying on the pod's own
	// environment context.
	if len(*scripts) != 1 {
		t.Fatalf("expected exactly one pod read, got %d", len(*scripts))
	}
	for _, want := range []string{"erun activity ai-session status", "--tenant 'petios'", "--environment 'local'"} {
		if !strings.Contains((*scripts)[0], want) {
			t.Fatalf("read script %q is missing %q", (*scripts)[0], want)
		}
	}
}

// TestReportedStateOutranksVolumeWhenTheToolRepaints pins that the two sources
// do not both get a say. A tool blocked on the human can still repaint its
// prompt, and those redraws would keep raising a badge the report keeps
// clearing — a badge flickering between two inputs is worse than either answer.
// So where the tool reports its own state, volume stands down completely: the
// poller owns the latch, and the repaints leave it alone.
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
			// Sustained output: five seconds of it, unbroken up to now, which
			// is what the volume rule needs to raise the badge on its own.
			managed.aiActiveSince = time.Now().Add(-(aiActivitySustainedThreshold + time.Second))
			managed.aiLastOutput = time.Now()
			app.sessions[managed.key] = managed
			if tc.reporting {
				app.recordAISessionReading(selection, []eruncommon.AISessionStatus{
					reportedAISessionStatus("conv-1", eruncommon.AISessionStateAwaitingInput, time.Now()),
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

// TestAISessionStatusLeavesTheVolumeLatchAloneWhenItSaysNothing pins that the
// new input only ever decides when it has something to say about this tab. A
// record left by an earlier session, an environment with nothing recorded, a
// read that failed, and a read that did not parse are all "no evidence", and
// the badge keeps the volume behaviour it has always had.
func TestAISessionStatusLeavesTheVolumeLatchAloneWhenItSaysNothing(t *testing.T) {
	selection := uiSelection{Tenant: "petios", Environment: "local"}
	tests := []struct {
		name    string
		output  string
		execErr error
		reading []eruncommon.AISessionStatus
	}{
		{
			name:   "nothing recorded for the environment",
			output: aiSessionProbeOutput(),
		},
		{
			name: "a record from before this tab existed belongs to another session",
			reading: []eruncommon.AISessionStatus{
				reportedAISessionStatus("older", eruncommon.AISessionStateAwaitingInput, time.Now().Add(-time.Hour)),
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

// TestModelLatchIsReleasedWhenItsReportStopsArriving guards the pinning hazard
// the pod heartbeat cannot reach: a latch the tool raised is re-asserted by its
// own report, never by pod liveness, so when the reports stop the latch has to
// go even though the program is still running. Otherwise a report that quietly
// stops arriving would leave the row spinning forever — the failure this
// desktop has already paid for once on the orchestrator path.
func TestModelLatchIsReleasedWhenItsReportStopsArriving(t *testing.T) {
	selection := uiSelection{Tenant: "petios", Environment: "local"}
	app, emits, _ := newAISessionStatusTestApp("", nil)
	managed := aiStatusTestTab(selection)
	managed.aiBusyEmitted = false
	managed.aiLastOutput = time.Time{}
	app.sessions[managed.key] = managed
	app.sessionHeartbeats[selectionKey(selection)] = heartbeatFor(true)

	app.recordAISessionReading(selection, []eruncommon.AISessionStatus{
		reportedAISessionStatus("conv-1", eruncommon.AISessionStateBusy, time.Now()),
	})
	app.applyAISessionReading(managed)
	if len(emits.events(aiActivityEvent)) != 1 {
		t.Fatalf("expected the report to raise the latch, got %+v", emits.events(aiActivityEvent))
	}

	// The report stops arriving while the pod keeps saying the program is up.
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

// TestParseAISessionStatuses pins the reader against the shape the pod actually
// returns: the CLI's array between the markers, with whatever else the combined
// pod-exec stream carries around it.
func TestParseAISessionStatuses(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		wantOK     bool
		wantStates []eruncommon.AISessionState
	}{
		{
			name:       "a marked array",
			output:     aiSessionProbeOutput(reportedAISessionStatus("conv-1", eruncommon.AISessionStateAwaitingInput, time.Unix(100, 0).UTC())),
			wantOK:     true,
			wantStates: []eruncommon.AISessionState{eruncommon.AISessionStateAwaitingInput},
		},
		{
			name:   "an empty environment is a reading, not a failure",
			output: aiSessionProbeOutput(),
			wantOK: true,
		},
		{
			name:       "trace output around the array is dropped",
			output:     "==> Resolving\n" + aiSessionProbeOutput(reportedAISessionStatus("conv-1", eruncommon.AISessionStateBusy, time.Unix(100, 0).UTC())) + "done\n",
			wantOK:     true,
			wantStates: []eruncommon.AISessionState{eruncommon.AISessionStateBusy},
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
