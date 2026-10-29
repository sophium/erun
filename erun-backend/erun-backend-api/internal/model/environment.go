package model

import (
	"time"

	"github.com/uptrace/bun"
)

type EnvironmentType string

const (
	EnvironmentTypeLocalAgent  EnvironmentType = "local-agent"
	EnvironmentTypeRemoteAgent EnvironmentType = "remote-agent"
	EnvironmentTypeRuntime     EnvironmentType = "runtime"
)

// EnvironmentStatus is a hosted environment's lifecycle. `running` must never
// survive a delete attempt.
type EnvironmentStatus string

const (
	EnvironmentStatusRegistered      EnvironmentStatus = "registered"
	EnvironmentStatusProvisioning    EnvironmentStatus = "provisioning"
	EnvironmentStatusRunning         EnvironmentStatus = "running"
	EnvironmentStatusFailed          EnvironmentStatus = "failed"
	EnvironmentStatusDeleting        EnvironmentStatus = "deleting"
	EnvironmentStatusDeletionBlocked EnvironmentStatus = "deletion-blocked"
)

// Environment is a tenant's erun environment record.
type Environment struct {
	bun.BaseModel     `bun:"table:environments,alias:e"`
	EnvironmentID     string          `json:"environmentId" bun:"environment_id,pk,scanonly"`
	TenantID          string          `json:"tenantId" bun:"tenant_id,scanonly"`
	Name              string          `json:"name" bun:"name"`
	Type              EnvironmentType `json:"type" bun:"type"`
	KubernetesContext string          `json:"kubernetesContext,omitempty" bun:"kubernetes_context,nullzero"`
	ContextID         string          `json:"contextId,omitempty" bun:"context_id,nullzero"`
	RuntimeVersion    string          `json:"runtimeVersion,omitempty" bun:"runtime_version,nullzero"`
	// Status is moved by the deploy executor, never by the create path.
	Status         EnvironmentStatus `json:"status" bun:"status,scanonly"`
	ProvisionError string            `json:"provisionError,omitempty" bun:"provision_error,scanonly,nullzero"`
	// DeployedVersion is what the last successful deploy installed; RuntimeVersion is the declared pin.
	DeployedVersion string `json:"deployedVersion,omitempty" bun:"deployed_version,scanonly,nullzero"`
	// ExposeError never moves Status away from `running`: failed exposure does not
	// mean the workload is unhealthy.
	ExposeError string `json:"exposeError,omitempty" bun:"expose_error,scanonly,nullzero"`
	// ExposedHostname is empty only when not exposed, and survives later failed
	// re-exposes (see EnvironmentStatusUpdate).
	ExposedHostname string `json:"exposedHostname,omitempty" bun:"exposed_hostname,scanonly,nullzero"`
	// DeleteError is not cleared when a retry claims the row, so the blocker stays readable.
	DeleteError string `json:"deleteError,omitempty" bun:"delete_error,scanonly,nullzero"`
	// DeleteAttempts is incremented by the claim, so it bounds retries even when an attempt dies.
	DeleteAttempts int       `json:"deleteAttempts,omitempty" bun:"delete_attempts,scanonly"`
	CreatedAt      time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt      time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
