package core

import (
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
)

func TestAISessionNotifyResolvesToAwaitingInput(t *testing.T) {
	record := model.AISessionRecord{SessionID: "s", Event: model.AISessionEventNotify, At: time.Now()}
	status := ResolveAISessionStatus(record)
	if status.State != model.AISessionStateAwaitingInput {
		t.Fatalf("notify event: want awaiting-input, got %s", status.State)
	}
}
