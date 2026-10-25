package main

import (
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// busy fires after sustained output, clears after idle or close, and orchestrator
// sessions never drive it: their TUI repaints forever and has no pod heartbeat.
func TestRecordAIActivityDebounce(t *testing.T) {
	tests := []struct {
		name       string
		kind       sessionKind
		wantEmits  bool
		wantSecond bool
	}{
		{name: "AI session emits ai-activity", kind: sessionKindAI, wantEmits: true},
		{name: "Orchestrator session is silent: its spinner comes from its own report", kind: sessionKindOrchestrator, wantEmits: false},
		{name: "Local session is silent", kind: sessionKindLocal, wantEmits: false},
		{name: "Open session is silent", kind: sessionKindOpen, wantEmits: false},
		{name: "Command session is silent", kind: sessionKindCommand, wantEmits: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			emits := newCapturedEmits()
			app := &App{
				sessions: make(map[string]*managedTerminal),
				emitFn:   emits.fn(),
			}
			managed := &managedTerminal{
				kind:      tc.kind,
				selection: uimodel.Selection{Tenant: "t", Environment: "e"},
				key:       string(tc.kind) + "\x00t\x00e",
				serial:    7,
			}

			// No time has passed, so busy must not be set yet.
			app.recordAIActivity(managed)
			if len(emits.events(uimodel.AIActivityEvent)) != 0 {
				t.Fatalf("unexpected immediate emit: %+v", emits.events(uimodel.AIActivityEvent))
			}

			app.mu.Lock()
			managed.aiActiveSince = time.Now().Add(-(aiActivitySustainedThreshold + time.Second))
			app.mu.Unlock()
			app.recordAIActivity(managed)
			assertBusyEmit(t, emits.events(uimodel.AIActivityEvent), tc.kind, tc.wantEmits)

			app.finalizeAIActivity(managed)
			assertFinalizeEmit(t, emits.events(uimodel.AIActivityEvent), tc.kind, tc.wantEmits)
		})
	}
}

func assertBusyEmit(t *testing.T, busyEvents []any, kind sessionKind, wantEmits bool) {
	t.Helper()

	if !wantEmits {
		if len(busyEvents) != 0 {
			t.Fatalf("expected no emit for kind %s, got %+v", kind, busyEvents)
		}
		return
	}
	if len(busyEvents) != 1 {
		t.Fatalf("expected one busy=true emit, got %+v", busyEvents)
	}
	payload, ok := busyEvents[0].(aiActivityPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", busyEvents[0])
	}
	if !payload.Busy {
		t.Fatalf("expected busy=true, got %+v", payload)
	}
	if payload.Tenant != "t" || payload.Environment != "e" {
		t.Fatalf("expected selection echoed in payload, got %+v", payload)
	}
}

func assertFinalizeEmit(t *testing.T, allEvents []any, kind sessionKind, wantEmits bool) {
	t.Helper()

	if !wantEmits {
		if len(allEvents) != 0 {
			t.Fatalf("expected no emits for kind %s, got %+v", kind, allEvents)
		}
		return
	}
	if len(allEvents) != 2 {
		t.Fatalf("expected busy=true then busy=false, got %+v", allEvents)
	}
	payload, ok := allEvents[1].(aiActivityPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", allEvents[1])
	}
	if payload.Busy {
		t.Fatalf("expected busy=false from finalize, got %+v", payload)
	}
}

// A stale timer must not clear busy when fresh output arrived.
func TestClearAIActivityIfQuietBouncesNewOutput(t *testing.T) {
	emits := newCapturedEmits()
	app := &App{
		sessions: make(map[string]*managedTerminal),
		emitFn:   emits.fn(),
	}
	managed := &managedTerminal{
		kind:          sessionKindAI,
		selection:     uimodel.Selection{Tenant: "t", Environment: "e"},
		key:           "ai\x00t\x00e",
		serial:        9,
		aiBusyEmitted: true,
		aiLastOutput:  time.Now(), // fresh output between schedule and fire
	}
	app.clearAIActivityIfQuiet(managed)
	if len(emits.events(uimodel.AIActivityEvent)) != 0 {
		t.Fatalf("clearAIActivityIfQuiet must not emit while output is fresh, got %+v", emits.events(uimodel.AIActivityEvent))
	}
	if !managed.aiBusyEmitted {
		t.Fatalf("clearAIActivityIfQuiet must keep busy latch on while output is fresh")
	}
}

type capturedEmits struct {
	mu       sync.Mutex
	arrived  *sync.Cond
	byName   map[string][]any
	deadline bool
}

func newCapturedEmits() *capturedEmits {
	c := &capturedEmits{byName: make(map[string][]any)}
	c.arrived = sync.NewCond(&c.mu)
	return c
}

func (c *capturedEmits) fn() func(string, ...any) {
	return func(name string, args ...any) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.byName[name] = append(c.byName[name], args...)
		if len(args) == 0 {
			c.byName[name] = append(c.byName[name], nil)
		}
		c.arrived.Broadcast()
	}
}

// waitFor waits on the emitter's signal rather than a wall-clock poll, so CPU
// contention cannot fail it; pred runs under the lock.
func (c *capturedEmits) waitFor(bound time.Duration, pred func(byName map[string][]any) bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline = false
	timer := time.AfterFunc(bound, func() {
		c.mu.Lock()
		c.deadline = true
		c.mu.Unlock()
		c.arrived.Broadcast()
	})
	defer timer.Stop()
	for !pred(c.byName) {
		if c.deadline {
			return false
		}
		c.arrived.Wait()
	}
	return true
}

func (c *capturedEmits) events(name string) []any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]any, len(c.byName[name]))
	copy(out, c.byName[name])
	return out
}
