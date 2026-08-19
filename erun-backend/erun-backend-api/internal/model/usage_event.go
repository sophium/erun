package model

import (
	"time"

	"github.com/uptrace/bun"
)

// UsageEventType classifies a metered environment lifecycle event.
type UsageEventType string

const (
	UsageEventEnvironmentProvisioned UsageEventType = "environment_provisioned"
	UsageEventEnvironmentStopped     UsageEventType = "environment_stopped"
	UsageEventEnvironmentDeleted     UsageEventType = "environment_deleted"
)

// UsageEvent is one append-only per-tenant metering record: a resource-affecting
// environment lifecycle transition, snapshotting the resource caps that applied
// at the time. It is the metering hook for #605 — a small, honest usage trail,
// not a billing engine.
type UsageEvent struct {
	bun.BaseModel `bun:"table:usage_events,alias:ue"`
	UsageEventID  string    `json:"usageEventId" bun:"usage_event_id,pk,scanonly"`
	TenantID      string    `json:"tenantId" bun:"tenant_id,scanonly"`
	EnvironmentID string    `json:"environmentId,omitempty" bun:"environment_id"`
	EventType     string    `json:"eventType" bun:"event_type"`
	CPUMillicores int       `json:"cpuMillicores,omitempty" bun:"cpu_millicores"`
	MemoryMB      int       `json:"memoryMb,omitempty" bun:"memory_mb"`
	StorageGB     int       `json:"storageGb,omitempty" bun:"storage_gb"`
	CreatedAt     time.Time `json:"createdAt" bun:"created_at,scanonly"`
}
