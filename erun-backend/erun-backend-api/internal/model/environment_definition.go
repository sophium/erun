package model

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

// EnvironmentDefinition is the platform's stored copy of an environment's
// portable settings — the DB shape of eruncommon.PlatformEnvDefinition,
// which is what produced it.
//
// It is keyed by the environment rather than by an id of its own: a definition
// is addressed by the environment it describes and nothing ever looks one up
// any other way, so a separate identity would be one no caller could name.
type EnvironmentDefinition struct {
	bun.BaseModel `bun:"table:environment_definitions,alias:ed"`
	TenantID      string `json:"tenantId" bun:"tenant_id,scanonly"`
	EnvironmentID string `json:"environmentId" bun:"environment_id"`
	// Revision increases on every upload, from 1 for the definition written
	// alongside registration. Owned by the upsert's own SQL, so scan-only here.
	Revision int `json:"revision" bun:"revision,scanonly"`
	// Definition is the serialized portable subset, passed through verbatim:
	// the platform stores it and hands it back without interpreting it, which
	// is what keeps the allowlist that decides its contents living on the
	// client that authored it.
	Definition json.RawMessage `json:"definition" bun:"definition"`
	// WrittenByUserID records who uploaded this revision. Database-defaulted
	// from erun_current_user_id(), never taken from the request body.
	WrittenByUserID string    `json:"writtenByUserId,omitempty" bun:"written_by_user_id,scanonly,nullzero"`
	CreatedAt       time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt       time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
