package aisession

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
)

func TestRecordAISessionEventRejectsPathTraversalSessionID(t *testing.T) {
	err := RecordAISessionEvent(model.AISessionEventParams{
		Tenant: "acme", Environment: "dev", SessionID: "../escape", Event: model.AISessionEventTurnStart,
	})
	if err == nil {
		t.Fatalf("expected an error for a path-traversal session id")
	}
}

func TestRecordAISessionEventRequiresTenantAndEnvironment(t *testing.T) {
	if err := RecordAISessionEvent(model.AISessionEventParams{SessionID: "s", Event: model.AISessionEventTurnStart}); err == nil {
		t.Fatalf("expected an error for missing tenant/environment")
	}
}
