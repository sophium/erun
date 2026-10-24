package model

import (
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/uptrace/bun"
)

// BuildKind is the kind a build row was written with. Every build written
// today is RECORDED; GATE survives only on historical rows.
type BuildKind string

const (
	// BuildKindRecorded is every build the API writes: a client-reported
	// build, the merge queue's build of a prepared merge commit, or a
	// release's own build. It always carries a version.
	BuildKindRecorded BuildKind = "RECORDED"
	// BuildKindGate is a retired kind the API refuses to write. Historical
	// rows still carry it (with no version), so it stays readable, and a
	// MERGED review's last_merged_build_id may point at one.
	BuildKindGate BuildKind = "GATE"
)

type Build struct {
	bun.BaseModel `bun:"table:builds,alias:b"`
	BuildID       string `json:"buildId" bun:"build_id,pk,scanonly"`
	TenantID      string `json:"tenantId" bun:"tenant_id,scanonly"`
	// ReviewID is empty for an ordinary `erun build` that reports itself with
	// no review attached -- a review association is one reason a
	// build happened, not its identity.
	ReviewID string `json:"reviewId,omitempty" bun:"review_id,nullzero"`
	// ReviewName is read-only display data populated by build read queries.
	ReviewName string `json:"reviewName,omitempty" bun:"review_name,scanonly"`
	// EnvironmentID is the environment the build ran in, when the caller
	// reports one -- empty for a review-linked build.
	EnvironmentID string `json:"environmentId,omitempty" bun:"environment_id,nullzero"`
	// EnvironmentName is read-only display data populated by build read
	// queries.
	EnvironmentName string `json:"environmentName,omitempty" bun:"environment_name,scanonly"`
	// Kind defaults to BuildKindRecorded when the caller omits it, and any
	// other kind is refused on write. The environment the merge queue
	// promotes records its build of the prepared merge commit on the review
	// like any other build (see AGENTS.md "Merge Queue").
	Kind       BuildKind `json:"kind" bun:"kind"`
	Successful bool      `json:"successful" bun:"successful"`
	CommitID   string    `json:"commitId" bun:"commit_id"`
	// Version is empty only on a historical GATE row.
	Version string `json:"version,omitempty" bun:"version,nullzero"`
	// FailureDetail is an optional account of why a failed build failed, in
	// the reporting build's own words.
	FailureDetail string `json:"failureDetail,omitempty" bun:"failure_detail,nullzero"`
	// Profile is the bounded per-step profile the caller self-reported
	// alongside this build's outcome -- a duration/CPU/throttle/IO summary
	// plus the top costliest steps, never the full step tree. NULL when the
	// caller collected none.
	Profile   *core.BuildProfileSummary `json:"profile,omitempty" bun:"profile,type:jsonb,nullzero"`
	CreatedAt time.Time                 `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt time.Time                 `json:"updatedAt" bun:"updated_at,scanonly"`
}
