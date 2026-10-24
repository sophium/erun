package model

import (
	"fmt"
	"strings"
)

// Review statuses: OPEN while being worked on, then exactly one of the others.
// erun-backend-api's reviews table constrains status to this set.
const (
	ReviewStatusOpen   = "OPEN"
	ReviewStatusClosed = "CLOSED"
	ReviewStatusFailed = "FAILED"
	ReviewStatusReady  = "READY"
	ReviewStatusMerge  = "MERGE"
	ReviewStatusMerged = "MERGED"
)

// ReviewStatuses is the closed vocabulary in display order.
var ReviewStatuses = []string{
	ReviewStatusOpen, ReviewStatusClosed, ReviewStatusFailed,
	ReviewStatusReady, ReviewStatusMerge, ReviewStatusMerged,
}

// NormalizeReviewStatus validates a status filter case-insensitively; empty means
// no filter. A typo must fail rather than read as an empty queue.
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
