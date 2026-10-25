package tenant

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/infra/platformclient"
)

func TestOperatorPlatformErrorNamesTheReasonNotTheWire(t *testing.T) {
	wire := fmt.Errorf("platform api PATCH /v1/reviews/r1/status: http 409: conflict: %w", platformclient.ErrPlatformConflict)

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
		{"forbidden", platformclient.ErrPlatformForbidden, "do not have permission"},
		{"unauthorized", platformclient.ErrPlatformUnauthorized, "Sign in again"},
		{"not found", platformclient.ErrPlatformNotFound, "no longer exists"},
		{"conflict", platformclient.ErrPlatformConflict, "Refresh and try again"},
		{"not implemented", platformclient.ErrPlatformNotImplemented, "cannot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := operatorPlatformError(actionCloseReview, fmt.Errorf("wrapped: %w", tc.sentinel))
			if got == nil || !strings.Contains(got.Error(), tc.expect) {
				t.Fatalf("expected a message containing %q, got %v", tc.expect, got)
			}
		})
	}
}

// A reviewer 404 may mean the reviewer was removed, not the review.
func TestOperatorPlatformErrorNamesReviewerNotFoundDistinctly(t *testing.T) {
	for _, action := range []platformAction{actionAddReviewer, actionRemoveReviewer} {
		got := operatorPlatformError(action, fmt.Errorf("wrapped: %w", platformclient.ErrPlatformNotFound))
		if got == nil {
			t.Fatalf("%s: expected an error", action)
		}
		if !strings.Contains(got.Error(), "review or reviewer no longer exists") {
			t.Fatalf("%s: expected the reviewer-specific not-found message, got %v", action, got)
		}
	}
}

func TestOperatorPlatformErrorPassesThroughWhatItCannotClassify(t *testing.T) {
	original := errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
	if got := operatorPlatformError(actionCreateReview, original); !errors.Is(got, original) || got.Error() != original.Error() {
		t.Fatalf("expected the original error unchanged, got %v", got)
	}
	if operatorPlatformError(actionCreateReview, nil) != nil {
		t.Fatal("nil must stay nil")
	}
}
