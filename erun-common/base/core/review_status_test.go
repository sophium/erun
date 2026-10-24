package core

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
)

func TestNormalizeReviewStatus(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty means no status filter", "", ""},
		{"blank means no status filter", "   ", ""},
		{"open", "OPEN", model.ReviewStatusOpen},
		{"closed", "CLOSED", model.ReviewStatusClosed},
		{"failed", "FAILED", model.ReviewStatusFailed},
		{"ready", "READY", model.ReviewStatusReady},
		{"merge", "MERGE", model.ReviewStatusMerge},
		{"merged", "MERGED", model.ReviewStatusMerged},
		{"lower case resolves to the stored spelling", "ready", model.ReviewStatusReady},
		{"surrounding space is ignored", "  merged  ", model.ReviewStatusMerged},
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
			model.ReviewStatusOpen, model.ReviewStatusClosed, model.ReviewStatusFailed,
			model.ReviewStatusReady, model.ReviewStatusMerge, model.ReviewStatusMerged,
		} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("NormalizeReviewStatus(%q) error %q does not name the accepted value %s", in, err, want)
			}
		}
	}
}

func TestReviewStatusVocabularyIsClosed(t *testing.T) {
	got := map[string]bool{}
	for _, status := range ReviewStatuses {
		got[status] = true
	}
	want := []string{
		model.ReviewStatusOpen, model.ReviewStatusClosed, model.ReviewStatusFailed,
		model.ReviewStatusReady, model.ReviewStatusMerge, model.ReviewStatusMerged,
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
