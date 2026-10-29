package aisessions

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity/aisession"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

func requireEchoedTarget(t *testing.T, result AISessionsResult, tenant, environment string) {
	t.Helper()
	if result.Tenant != tenant || result.Environment != environment {
		t.Fatalf("expected the resolved tenant/environment to be echoed back, got %+v", result)
	}
}

func TestAISessionsToolResolvesAgainstServerContext(t *testing.T) {
	isolateLeaseCache(t)
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}

	if err := aisession.RecordAISessionEvent(model.AISessionEventParams{
		Tenant: "tenant-a", Environment: "dev", SessionID: "sess-1",
		Tool: "claude", Event: model.AISessionEventTurnEnd,
	}); err != nil {
		t.Fatalf("record turn-end: %v", err)
	}

	_, one, err := AiSessionsTool(runtime)(context.Background(), nil, AISessionsInput{Session: "sess-1"})
	if err != nil {
		t.Fatalf("resolve single session: %v", err)
	}
	if len(one.Sessions) != 1 || one.Sessions[0].State != model.AISessionStateAwaitingInput {
		t.Fatalf("expected one awaiting-input session, got %+v", one.Sessions)
	}
	requireEchoedTarget(t, one, "tenant-a", "dev")

	_, all, err := AiSessionsTool(runtime)(context.Background(), nil, AISessionsInput{})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(all.Sessions) != 1 || all.Sessions[0].SessionID != "sess-1" {
		t.Fatalf("expected the listing to include the recorded session, got %+v", all.Sessions)
	}
	requireEchoedTarget(t, all, "tenant-a", "dev")
}

func TestAISessionsToolEmptyEnvironmentReturnsEmptyArrayNotNull(t *testing.T) {
	isolateLeaseCache(t)
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "tenant-a", Environment: "never-touched"}}

	_, result, err := AiSessionsTool(runtime)(context.Background(), nil, AISessionsInput{})
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
