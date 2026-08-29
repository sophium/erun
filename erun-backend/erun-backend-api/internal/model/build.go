package model

import (
	"time"

	"github.com/uptrace/bun"
)

// BuildKind distinguishes a reported build from the merge queue's own gate
// build, since only one of the two ever mints a version.
type BuildKind string

const (
	// BuildKindRecorded is a client-reported build or a release's own build,
	// either of which mints a version and requires it non-empty.
	BuildKindRecorded BuildKind = "RECORDED"
	// BuildKindGate is the merge queue's own build of the prospective merge. It
	// publishes nothing, so it never carries a version.
	BuildKindGate BuildKind = "GATE"
)

type Build struct {
	bun.BaseModel `bun:"table:builds,alias:b"`
	BuildID       string `json:"buildId" bun:"build_id,pk,scanonly"`
	TenantID      string `json:"tenantId" bun:"tenant_id,scanonly"`
	ReviewID      string `json:"reviewId" bun:"review_id"`
	// ReviewName is read-only display data populated by build read queries.
	ReviewName string `json:"reviewName,omitempty" bun:"review_name,scanonly"`
	// Kind defaults to BuildKindRecorded when the caller omits it, but the
	// client-facing route also accepts BuildKindGate: the environment the merge
	// queue promotes reports its own gate build this way (see AGENTS.md "Merge
	// Queue"). Only a successful GATE build's commit can later reach MERGED.
	Kind       BuildKind `json:"kind" bun:"kind"`
	Successful bool      `json:"successful" bun:"successful"`
	CommitID   string    `json:"commitId" bun:"commit_id"`
	// Version is empty for a GATE build, which publishes nothing.
	Version string `json:"version,omitempty" bun:"version,nullzero"`
	// FailureDetail is the gate's own account of why a GATE build failed; a
	// RECORDED failure's reason lives wherever the reporting caller's own CI
	// recorded it, not here.
	FailureDetail string    `json:"failureDetail,omitempty" bun:"failure_detail,nullzero"`
	CreatedAt     time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt     time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
