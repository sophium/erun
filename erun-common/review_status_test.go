package eruncommon

import (
	"strings"
	"testing"
)

// A status filter is operator input, and the listing it narrows is the merge
// queue's audit trail: a mistyped value must be refused as a bad argument
// rather than return an empty listing the operator reads as "no reviews".
func TestNormalizeReviewStatus(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty means no status filter", "", ""},
		{"blank means no status filter", "   ", ""},
		{"open", "OPEN", ReviewStatusOpen},
		{"closed", "CLOSED", ReviewStatusClosed},
		{"failed", "FAILED", ReviewStatusFailed},
		{"ready", "READY", ReviewStatusReady},
		{"merge", "MERGE", ReviewStatusMerge},
		{"merged", "MERGED", ReviewStatusMerged},
		{"lower case resolves to the stored spelling", "merged", ReviewStatusMerged},
		{"lower case ready", "ready", ReviewStatusReady},
		{"lower case open", "open", ReviewStatusOpen},
		{"surrounding space is ignored", "  closed  ", ReviewStatusClosed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeReviewStatus(tc.in)
			if err != nil {
				t.Fatalf("NormalizeReviewStatus(%q) returned an unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeReviewStatus(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeReviewStatusNamesTheAcceptedValues(t *testing.T) {
	for _, in := range []string{"bogus", "APPROVED", "MERGING"} {
		got, err := NormalizeReviewStatus(in)
		if err == nil {
			t.Fatalf("NormalizeReviewStatus(%q) = %q, want an error naming the accepted values", in, got)
		}
		for _, want := range []string{
			ReviewStatusOpen, ReviewStatusClosed, ReviewStatusFailed,
			ReviewStatusReady, ReviewStatusMerge, ReviewStatusMerged,
		} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("NormalizeReviewStatus(%q) error %q does not name the accepted value %s", in, err, want)
			}
		}
	}
}

// The refusal must happen before the platform call, so a mistyped filter never
// resolves an alias or reports a real-looking empty result.
func TestRunReviewListRefusesAnUnknownStatusBeforeAnyPlatformCall(t *testing.T) {
	_, err := RunReviewList(Context{}, nil, "", ReviewListParams{Status: "bogus"}, CloudDependencies{})
	if err == nil {
		t.Fatalf("RunReviewList with status %q succeeded, want a bad-argument error", "bogus")
	}
	if !strings.Contains(err.Error(), "unsupported review status") {
		t.Fatalf("RunReviewList error = %q, want the unsupported-status refusal", err)
	}
}
