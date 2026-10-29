package model

import (
	"fmt"
)

// PinBaseFreshness is how the checkout a plan was read from stands against its tracked ref.
// A plan from a stale checkout looks consistent but silently reverts the missing commits.
type PinBaseFreshness struct {
	// Branch is "HEAD" when detached.
	Branch string `json:"branch,omitempty"`
	Ref    string `json:"ref"`
	// Behind counts commits on Ref missing from the checkout.
	Behind int `json:"behind"`
	// RemoteRead is false when Ref was not refreshed from the remote before counting.
	RemoteRead bool `json:"remoteRead"`
}

// Stale reports whether the checkout is behind its tracked ref.
func (f PinBaseFreshness) Stale() bool { return f.Behind > 0 }

// BaseFreshnessNote is empty when the checkout is current or untracked.
func (p PinPlan) BaseFreshnessNote() string {
	if p.BaseFreshness == nil || !p.BaseFreshness.Stale() {
		return ""
	}
	freshness := *p.BaseFreshness
	note := fmt.Sprintf("the checkout this plan was resolved from is %d commit(s) behind %s", freshness.Behind, freshness.Ref)
	if !freshness.RemoteRead {
		note += " as this checkout last saw it"
	}
	return note + fmt.Sprintf(", so the plan describes that older base rather than what %s holds now — applying it would also revert anything those commits changed elsewhere in the tree", freshness.Ref)
}
