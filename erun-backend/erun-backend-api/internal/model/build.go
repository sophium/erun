package model

import (
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/uptrace/bun"
)

// BuildKind is the kind a build row was written with.
type BuildKind string

const (
	// BuildKindRecorded is the only kind the API writes; it always carries a version.
	BuildKindRecorded BuildKind = "RECORDED"
	// BuildKindGate is retired: refused on write but still present on historical
	// rows, which a MERGED review's last_merged_build_id may reference.
	BuildKindGate BuildKind = "GATE"
)

type Build struct {
	bun.BaseModel `bun:"table:builds,alias:b"`
	BuildID       string `json:"buildId" bun:"build_id,pk,scanonly"`
	TenantID      string `json:"tenantId" bun:"tenant_id,scanonly"`
	ReviewID      string `json:"reviewId,omitempty" bun:"review_id,nullzero"`
	// ReviewName is read-only, populated by build read queries.
	ReviewName    string `json:"reviewName,omitempty" bun:"review_name,scanonly"`
	EnvironmentID string `json:"environmentId,omitempty" bun:"environment_id,nullzero"`
	// EnvironmentName is read-only, populated by build read queries.
	EnvironmentName string `json:"environmentName,omitempty" bun:"environment_name,scanonly"`
	// Kind defaults to BuildKindRecorded; any other kind is refused on write.
	Kind       BuildKind `json:"kind" bun:"kind"`
	Successful bool      `json:"successful" bun:"successful"`
	CommitID   string    `json:"commitId" bun:"commit_id"`
	// Version is empty only on a historical GATE row.
	Version       string `json:"version,omitempty" bun:"version,nullzero"`
	FailureDetail string `json:"failureDetail,omitempty" bun:"failure_detail,nullzero"`
	// Profile is a bounded summary plus the costliest steps, never the full step tree.
	Profile   *core.BuildProfileSummary `json:"profile,omitempty" bun:"profile,type:jsonb,nullzero"`
	CreatedAt time.Time                 `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt time.Time                 `json:"updatedAt" bun:"updated_at,scanonly"`
}
