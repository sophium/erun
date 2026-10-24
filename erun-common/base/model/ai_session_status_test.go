package model

import (
	"testing"
	"time"
)

func TestAISessionNotifyResolvesToAwaitingInput(t *testing.T) {
	record := AISessionRecord{SessionID: "s", Event: AISessionEventNotify, At: time.Now()}
	status := ResolveAISessionStatus(record)
	if status.State != AISessionStateAwaitingInput {
		t.Fatalf("notify event: want awaiting-input, got %s", status.State)
	}
}
