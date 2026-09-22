package eruncommon

import (
	"strings"
	"testing"
)

// A status filter is operator input, and the listing it narrows is the review
// queue an operator checks to decide whether anything is waiting on them: a
// mistyped value must be refused as a bad argument rather than return an empty
// listing they read as "no reviews".
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
		{"lower case resolves to the stored spelling", "ready", ReviewStatusReady},
		{"surrounding space is ignored", "  merged  ", ReviewStatusMerged},
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
	for _, in := range []string{"BOGUS", "ZZZZ", "MERGEDD", "SUCCEEDED"} {
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
	_, err := RunReviewList(Context{}, nil, "", ReviewListParams{Status: "BOGUS"}, CloudDependencies{})
	if err == nil {
		t.Fatalf("RunReviewList with status %q succeeded, want a bad-argument error", "BOGUS")
	}
	if !strings.Contains(err.Error(), "unsupported review status") {
		t.Fatalf("RunReviewList error = %q, want the unsupported-status refusal", err)
	}
	// The alias lookup is what a mistyped filter used to reach; naming it here
	// pins that the refusal is a bad-argument error rather than a resolution
	// failure that happens to look like one.
	if strings.Contains(err.Error(), "alias") {
		t.Fatalf("RunReviewList error = %q, want the status refusal rather than an alias-resolution failure", err)
	}
}

// The accepted set is one list, not a copy: the backend's stored spellings and
// the filter's accepted spellings have to be the same vocabulary, or a caller
// could filter on a status no row can hold (or be refused one that rows do).
func TestReviewStatusVocabularyIsClosed(t *testing.T) {
	got := map[string]bool{}
	for _, status := range reviewStatuses {
		got[status] = true
	}
	want := []string{
		ReviewStatusOpen, ReviewStatusClosed, ReviewStatusFailed,
		ReviewStatusReady, ReviewStatusMerge, ReviewStatusMerged,
	}
	if len(got) != len(want) {
		t.Fatalf("reviewStatuses holds %d distinct values, want %d", len(got), len(want))
	}
	for _, status := range want {
		if !got[status] {
			t.Fatalf("reviewStatuses is missing %s", status)
		}
	}
}
