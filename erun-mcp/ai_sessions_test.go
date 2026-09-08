package erunmcp

import (
	"context"
	"encoding/json"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// requireEchoedTarget fails the test unless the result echoes the given
// resolved tenant/environment, keeping that check out of each scenario's own
// cyclomatic complexity below.
func requireEchoedTarget(t *testing.T, result AISessionsResult, tenant, environment string) {
	t.Helper()
	if result.Tenant != tenant || result.Environment != environment {
		t.Fatalf("expected the resolved tenant/environment to be echoed back, got %+v", result)
	}
}

// The read model here must resolve AwaitingInput from the last reported
// event rather than from silence -- see erun-common's own coverage of
// ResolveAISessionStatus for the state machine itself. This test only pins
// the MCP transport contract: server context defaulting and the single vs.
// list shapes.
func TestAISessionsToolResolvesAgainstServerContext(t *testing.T) {
	isolateLeaseCache(t)
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}

	if err := eruncommon.RecordAISessionEvent(eruncommon.AISessionEventParams{
		Tenant: "tenant-a", Environment: "dev", SessionID: "sess-1",
		Tool: "claude", Event: eruncommon.AISessionEventTurnEnd,
	}); err != nil {
		t.Fatalf("record turn-end: %v", err)
	}

	_, one, err := aiSessionsTool(runtime)(context.Background(), nil, AISessionsInput{Session: "sess-1"})
	if err != nil {
		t.Fatalf("resolve single session: %v", err)
	}
	if len(one.Sessions) != 1 || one.Sessions[0].State != eruncommon.AISessionStateAwaitingInput {
		t.Fatalf("expected one awaiting-input session, got %+v", one.Sessions)
	}
	requireEchoedTarget(t, one, "tenant-a", "dev")

	_, all, err := aiSessionsTool(runtime)(context.Background(), nil, AISessionsInput{})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(all.Sessions) != 1 || all.Sessions[0].SessionID != "sess-1" {
		t.Fatalf("expected the listing to include the recorded session, got %+v", all.Sessions)
	}
	requireEchoedTarget(t, all, "tenant-a", "dev")
}

// TestAISessionsToolEmptyEnvironmentReturnsEmptyArrayNotNull pins the exact
// shape reported in erun#2128: an environment with no recorded AI sessions
// must return "sessions": [] like idle_stop_history's "entries": [], never
// "sessions": null, which forces every caller to special-case this one tool.
func TestAISessionsToolEmptyEnvironmentReturnsEmptyArrayNotNull(t *testing.T) {
	isolateLeaseCache(t)
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "tenant-a", Environment: "never-touched"}}

	_, result, err := aiSessionsTool(runtime)(context.Background(), nil, AISessionsInput{})
	if err != nil {
		t.Fatalf("list sessions on an untouched environment: %v", err)
	}
	if result.Sessions == nil {
		t.Fatalf("want a non-nil empty slice so JSON marshals to [], got a nil slice which marshals to null")
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if string(decoded["sessions"]) != "[]" {
		t.Fatalf("want sessions to marshal as [], got %s", decoded["sessions"])
	}
	requireEchoedTarget(t, result, "tenant-a", "never-touched")
}

func TestAISessionsToolUnknownSessionReadsAsIdle(t *testing.T) {
	isolateLeaseCache(t)
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}

	_, result, err := aiSessionsTool(runtime)(context.Background(), nil, AISessionsInput{Session: "never-started"})
	if err != nil {
		t.Fatalf("resolve unknown session: %v", err)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].State != eruncommon.AISessionStateIdle {
		t.Fatalf("expected an idle status for an unrecorded session, got %+v", result.Sessions)
	}
}
