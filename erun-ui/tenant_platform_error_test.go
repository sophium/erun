package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// A refusal is shown beside the control the operator just used, so it must
// read as a reason rather than as the exchange that produced it.
func TestOperatorPlatformErrorNamesTheReasonNotTheWire(t *testing.T) {
	wire := fmt.Errorf("platform api PATCH /v1/reviews/r1/status: http 409: conflict: %w", eruncommon.ErrPlatformConflict)

	got := operatorPlatformError(actionAdvanceQueue, wire)
	if got == nil {
		t.Fatal("expected an error")
	}
	message := got.Error()
	for _, leak := range []string{"http 409", "/v1/reviews", "PATCH", "platform api"} {
		if strings.Contains(message, leak) {
			t.Errorf("operator message leaks the wire form %q: %s", leak, message)
		}
	}
	if !strings.Contains(message, "advance the merge queue") {
		t.Errorf("message should name the attempt: %s", message)
	}
}

func TestOperatorPlatformErrorMapsEachDistinctRefusal(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sentinel error
		expect   string
	}{
		{"forbidden", eruncommon.ErrPlatformForbidden, "do not have permission"},
		{"unauthorized", eruncommon.ErrPlatformUnauthorized, "Sign in again"},
		{"not found", eruncommon.ErrPlatformNotFound, "no longer exists"},
		{"conflict", eruncommon.ErrPlatformConflict, "Refresh and try again"},
		{"not implemented", eruncommon.ErrPlatformNotImplemented, "cannot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := operatorPlatformError(actionCloseReview, fmt.Errorf("wrapped: %w", tc.sentinel))
			if got == nil || !strings.Contains(got.Error(), tc.expect) {
				t.Fatalf("expected a message containing %q, got %v", tc.expect, got)
			}
		})
	}
}

// A failure erun cannot classify keeps its own text: a sentence that guesses
// wrong is worse than one that is merely technical.
func TestOperatorPlatformErrorPassesThroughWhatItCannotClassify(t *testing.T) {
	original := errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
	if got := operatorPlatformError(actionCreateReview, original); got != original {
		t.Fatalf("expected the original error unchanged, got %v", got)
	}
	if operatorPlatformError(actionCreateReview, nil) != nil {
		t.Fatal("nil must stay nil")
	}
}
