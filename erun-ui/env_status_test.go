package main

import (
	"testing"
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func envStatuses(emits *capturedEmits) []uimodel.EnvStatusPayload {
	var out []uimodel.EnvStatusPayload
	for _, raw := range emits.events(uimodel.EnvStatusEvent) {
		if payload, ok := raw.(uimodel.EnvStatusPayload); ok {
			out = append(out, payload)
		}
	}
	return out
}

// envStatusWaitBound is a safety net, not a budget: waits return on the emit.
const envStatusWaitBound = 30 * time.Second

func waitForEnvStatus(t *testing.T, emits *capturedEmits, status string) {
	t.Helper()
	arrived := emits.waitFor(envStatusWaitBound, func(byName map[string][]any) bool {
		for _, raw := range byName[uimodel.EnvStatusEvent] {
			if payload, ok := raw.(uimodel.EnvStatusPayload); ok && payload.Status == status {
				return true
			}
		}
		return false
	})
	if !arrived {
		t.Fatalf("env-status %q was not emitted; got %+v", status, envStatuses(emits))
	}
}
