package model

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

// EnvironmentDefinition is the stored copy of an environment's portable
// settings, keyed by the environment.
type EnvironmentDefinition struct {
	bun.BaseModel `bun:"table:environment_definitions,alias:ed"`
	TenantID      string `json:"tenantId" bun:"tenant_id,scanonly"`
	EnvironmentID string `json:"environmentId" bun:"environment_id"`
	Revision      int    `json:"revision" bun:"revision,scanonly"`
	// Definition is stored and returned verbatim; the client owns its allowlist.
	Definition json.RawMessage `json:"definition" bun:"definition"`
	// WrittenByUserID is database-defaulted, never taken from the request.
	WrittenByUserID string    `json:"writtenByUserId,omitempty" bun:"written_by_user_id,scanonly,nullzero"`
	CreatedAt       time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt       time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
