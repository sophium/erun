package review

import (
	"context"
	"testing"

	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// TestReviewCommentToolRefusesACallMissingReviewIDOrBody keeps the remaining
// required inputs required, and keeps the refusal naming which one is
// missing rather than listing every field the tool accepts.
func TestReviewCommentToolRefusesACallMissingReviewIDOrBody(t *testing.T) {
	handler := ReviewCommentTool(shared.RuntimeConfig{})
	cases := []struct {
		name  string
		input ReviewCommentInput
		want  string
	}{
		{
			name:  "no reviewId",
			input: ReviewCommentInput{Body: "overall this looks good"},
			want:  "reviewId is required",
		},
		{
			name:  "no body",
			input: ReviewCommentInput{ReviewID: "018f0000-0000-7000-8000-000000000000"},
			want:  "body is required",
		},
		{
			name:  "whitespace-only body",
			input: ReviewCommentInput{ReviewID: "018f0000-0000-7000-8000-000000000000", Body: "  \n"},
			want:  "body is required",
		},
		{
			name:  "neither",
			input: ReviewCommentInput{},
			want:  "reviewId and body are required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// No store is configured: the refusal must precede any platform
			// resolution, so a missing input never depends on config state.
			_, _, err := handler(context.Background(), nil, tc.input)
			if err == nil {
				t.Fatal("expected a refusal, got none")
			}
			if err.Error() != tc.want {
				t.Errorf("refusal message:\n got %q\nwant %q", err.Error(), tc.want)
			}
		})
	}
}
