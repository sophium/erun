package main

import (
	"sync"
	"testing"
	"time"
)

// TestRecordAIActivityDebounce locks the debounced "AI tab is working"
// signal that drives the sidebar busy badge for non-active envs.
//
// Policy:
//   - busy=true must fire only after aiActivitySustainedThreshold (5 s)
//     of *sustained* output. A single burst that ends inside the window
//     must not toggle the badge.
//   - busy=false must fire aiActivityIdleThreshold (3 s) after the last
//     output, regardless of how long busy=true was latched.
//   - Session close (finalizeAIActivity) must release a latched
//     busy=true even mid-generation.
//   - The signal is silent for non-AI session kinds.
func TestRecordAIActivityDebounce(t *testing.T) {
	tests := []struct {
		name       string
		kind       sessionKind
		wantEmits  bool
		wantSecond bool
	}{
		{name: "AI session emits ai-activity", kind: sessionKindAI, wantEmits: true},
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
				selection: uiSelection{Tenant: "t", Environment: "e"},
				key:       string(tc.kind) + "\x00t\x00e",
				serial:    7,
			}

			// Single burst: no time has passed, so even an AI session
			// must not have flipped busy=true yet.
			app.recordAIActivity(managed)
			if len(emits.events(aiActivityEvent)) != 0 {
				t.Fatalf("unexpected immediate emit: %+v", emits.events(aiActivityEvent))
			}

			// Simulate 5 s of sustained output by backdating the
			// session's aiActiveSince before the second record call.
			app.mu.Lock()
			managed.aiActiveSince = time.Now().Add(-(aiActivitySustainedThreshold + time.Second))
			app.mu.Unlock()
			app.recordAIActivity(managed)
			busyEvents := emits.events(aiActivityEvent)
			if tc.wantEmits {
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
			} else {
				if len(busyEvents) != 0 {
					t.Fatalf("expected no emit for kind %s, got %+v", tc.kind, busyEvents)
				}
			}

			// finalizeAIActivity must release a latched busy=true. For
			// non-AI kinds it must remain a no-op (no emits at all).
			app.finalizeAIActivity(managed)
			allEvents := emits.events(aiActivityEvent)
			if tc.wantEmits {
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
			} else if len(allEvents) != 0 {
				t.Fatalf("expected no emits for kind %s, got %+v", tc.kind, allEvents)
			}
		})
	}
}

// TestClearAIActivityIfQuietBouncesNewOutput verifies that a stale
// AfterFunc firing does not clear the busy latch when fresh output has
// arrived since the timer was scheduled. The production path relies on
// this to suppress flicker when recordAIActivity calls overlap with a
// previously scheduled timer.
func TestClearAIActivityIfQuietBouncesNewOutput(t *testing.T) {
	emits := newCapturedEmits()
	app := &App{
		sessions: make(map[string]*managedTerminal),
		emitFn:   emits.fn(),
	}
	managed := &managedTerminal{
		kind:          sessionKindAI,
		selection:     uiSelection{Tenant: "t", Environment: "e"},
		key:           "ai\x00t\x00e",
		serial:        9,
		aiBusyEmitted: true,
		aiLastOutput:  time.Now(), // fresh output between schedule and fire
	}
	app.clearAIActivityIfQuiet(managed)
	if len(emits.events(aiActivityEvent)) != 0 {
		t.Fatalf("clearAIActivityIfQuiet must not emit while output is fresh, got %+v", emits.events(aiActivityEvent))
	}
	if !managed.aiBusyEmitted {
		t.Fatalf("clearAIActivityIfQuiet must keep busy latch on while output is fresh")
	}
}

type capturedEmits struct {
	mu     sync.Mutex
	byName map[string][]any
}

func newCapturedEmits() *capturedEmits {
	return &capturedEmits{byName: make(map[string][]any)}
}

func (c *capturedEmits) fn() func(string, ...any) {
	return func(name string, args ...any) {
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, arg := range args {
			c.byName[name] = append(c.byName[name], arg)
		}
		if len(args) == 0 {
			c.byName[name] = append(c.byName[name], nil)
		}
	}
}

func (c *capturedEmits) events(name string) []any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]any, len(c.byName[name]))
	copy(out, c.byName[name])
	return out
}
