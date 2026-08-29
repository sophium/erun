package erunmcp

import (
	"context"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

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

	_, all, err := aiSessionsTool(runtime)(context.Background(), nil, AISessionsInput{})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(all.Sessions) != 1 || all.Sessions[0].SessionID != "sess-1" {
		t.Fatalf("expected the listing to include the recorded session, got %+v", all.Sessions)
	}
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
