package core

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
)

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

// ReviewStatuses is the closed vocabulary in display order.
var ReviewStatuses = []string{
	model.ReviewStatusOpen, model.ReviewStatusClosed, model.ReviewStatusFailed,
	model.ReviewStatusReady, model.ReviewStatusMerge, model.ReviewStatusMerged,
}
