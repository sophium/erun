package eruncommon

import (
	"strings"
	"testing"
	"time"
)

// fakeWhipRunner records every command it was asked to run and answers from a
// scripted table, so these tests never shell out to a real dtach.
type fakeWhipRunner struct {
	calls   [][]string
	outputs map[string]string
	errs    map[string]error
}

func (f *fakeWhipRunner) run(name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	key := strings.Join(call, "\x00")
	if err, ok := f.errs[key]; ok {
		return nil, err
	}
	return []byte(f.outputs[key]), nil
}

func TestEnvironmentWhipSessionAliveReadsMasterPID(t *testing.T) {
	f := &fakeWhipRunner{outputs: map[string]string{}}
	// The scan script is generated dynamically (it embeds the socket path), so
	// stub every "sh -c ..." call with a live master pid regardless of body.
	f.outputs["placeholder"] = ""
	alive, err := environmentWhipSessionAlive(func(name string, args ...string) ([]byte, error) {
		f.calls = append(f.calls, append([]string{name}, args...))
		return []byte("4242\n"), nil
	}, "acme", "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !alive {
		t.Fatalf("expected alive=true for a non-empty master pid")
	}
	if len(f.calls) != 1 || f.calls[0][0] != "sh" {
		t.Fatalf("expected exactly one sh -c probe call, got %v", f.calls)
	}
}

func TestEnvironmentWhipSessionAliveFalseWhenNoMaster(t *testing.T) {
	alive, err := environmentWhipSessionAlive(func(name string, args ...string) ([]byte, error) {
		return []byte("\n"), nil
	}, "acme", "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if alive {
		t.Fatalf("expected alive=false when the probe reports no master pid")
	}
}

// TestPushEnvironmentWhipNudgeSettleOrder is the settle-write assertion the
// task calls out explicitly: the nudge text and the carriage return that
// submits it must be two separate writes, spaced by the settle duration, never
// coalesced into one — mirroring orchestrator_pacing.go's writeOrchestratorPacingNudge.
func TestPushEnvironmentWhipNudgeSettleOrder(t *testing.T) {
	f := &fakeWhipRunner{outputs: map[string]string{}}
	settled := false
	origSleep := whipNudgeSleep
	whipNudgeSleep = func(d time.Duration) { settled = true }
	defer func() { whipNudgeSleep = origSleep }()

	err := pushEnvironmentWhipNudge(f.run, "acme", "dev", "keep going", time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.calls) != 2 {
		t.Fatalf("expected exactly two writes (text, then CR), got %d: %v", len(f.calls), f.calls)
	}
	first := strings.Join(f.calls[0], " ")
	second := strings.Join(f.calls[1], " ")
	if !strings.Contains(first, "keep going") {
		t.Fatalf("first write must carry the message text, got %q", first)
	}
	if strings.Contains(second, "keep going") {
		t.Fatalf("second write must be the bare carriage return, not the message again: %q", second)
	}
	if !settled {
		t.Fatalf("expected the settle sleep to run between the two writes")
	}
}

func TestRunLocalEnvironmentWhipDryRunPushesNothing(t *testing.T) {
	isolateActivityCache(t)
	cfg := ResolveWhipConfig(nil)
	runner := func(name string, args ...string) ([]byte, error) { return []byte("4242\n"), nil }
	result, err := RunLocalEnvironmentWhip(time.Now(), runner, "acme", "dev", cfg, true, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Decision != WhipDecisionNudge {
		t.Fatalf("expected a live, explicit call to decide nudge, got %v/%v", result.Decision, result.Reason)
	}
	if result.Pushed {
		t.Fatalf("dry-run must not push")
	}
}

func TestRunLocalEnvironmentWhipPushesAndPersistsCount(t *testing.T) {
	isolateActivityCache(t)
	cfg := ResolveWhipConfig(nil)
	whipNudgeSleep = func(time.Duration) {}
	runner := func(name string, args ...string) ([]byte, error) { return []byte("4242\n"), nil }

	first, err := RunLocalEnvironmentWhip(time.Now(), runner, "acme", "dev", cfg, true, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !first.Pushed {
		t.Fatalf("expected the first explicit call to push, got decision=%v reason=%v", first.Decision, first.Reason)
	}

	state, ok := loadWhipEnvironmentState("acme", "dev")
	if !ok || state.NudgeCount != 1 {
		t.Fatalf("expected persisted nudge count 1, got ok=%v state=%+v", ok, state)
	}
}

func TestRunLocalEnvironmentWhipCapsAfterMaxNudges(t *testing.T) {
	isolateActivityCache(t)
	maxNudges := 1
	cfg := ResolveWhipConfig(&WhipConfigOverride{MaxNudges: &maxNudges})
	whipNudgeSleep = func(time.Duration) {}
	runner := func(name string, args ...string) ([]byte, error) { return []byte("4242\n"), nil }

	if _, err := RunLocalEnvironmentWhip(time.Now(), runner, "acme", "dev", cfg, true, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := RunLocalEnvironmentWhip(time.Now(), runner, "acme", "dev", cfg, true, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if second.Decision != WhipDecisionCap || second.Reason != WhipReasonCapCrossed {
		t.Fatalf("expected the second call to cross the cap, got decision=%v reason=%v", second.Decision, second.Reason)
	}
	third, err := RunLocalEnvironmentWhip(time.Now(), runner, "acme", "dev", cfg, true, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if third.Decision != WhipDecisionNone || third.Reason != WhipReasonAlreadyCapped {
		t.Fatalf("expected the third call to stay capped, got decision=%v reason=%v", third.Decision, third.Reason)
	}
}

func TestRunLocalEnvironmentWhipNotAliveWhenNoMaster(t *testing.T) {
	isolateActivityCache(t)
	cfg := ResolveWhipConfig(nil)
	runner := func(name string, args ...string) ([]byte, error) { return []byte("\n"), nil }
	result, err := RunLocalEnvironmentWhip(time.Now(), runner, "acme", "dev", cfg, true, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Decision != WhipDecisionNone || result.Reason != WhipReasonNotAlive {
		t.Fatalf("expected none/not-alive, got %v/%v", result.Decision, result.Reason)
	}
}
