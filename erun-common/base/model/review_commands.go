package model

import (
	"fmt"
	"strings"
)

// The statuses a review can hold: a review is OPEN while it is being worked on
// and settles on exactly one of the others. This is the platform's own closed
// vocabulary -- erun-backend-api's model derives its stored spellings from
// these constants and its reviews table constrains status to exactly this set,
// so a value outside it names no row anywhere.
const (
	ReviewStatusOpen   = "OPEN"
	ReviewStatusClosed = "CLOSED"
	ReviewStatusFailed = "FAILED"
	ReviewStatusReady  = "READY"
	ReviewStatusMerge  = "MERGE"
	ReviewStatusMerged = "MERGED"
)

// ReviewStatuses is the closed vocabulary in the order the help text and the
// refusal both present it.
var ReviewStatuses = []string{
	ReviewStatusOpen, ReviewStatusClosed, ReviewStatusFailed,
	ReviewStatusReady, ReviewStatusMerge, ReviewStatusMerged,
}

// NormalizeReviewStatus validates a review status filter and resolves it to
// the spelling the platform stores, accepting any casing. The empty value
// means "no status filter". The error names the accepted values, since this is
// operator input from a flag or a tool argument: a mistyped filter that reached
// the platform would come back as an empty listing, indistinguishable from a
// review queue that genuinely has nothing in that state -- and an operator
// reads "no reviews" as a conclusion they act on.
func NormalizeReviewStatus(status string) (string, error) {
	trimmed := strings.TrimSpace(status)
	if trimmed == "" {
		return "", nil
	}
	normalized := strings.ToUpper(trimmed)
	for _, candidate := range ReviewStatuses {
		if normalized == candidate {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("unsupported review status %q: expected one of %s", trimmed, strings.Join(ReviewStatuses, ", "))
}
