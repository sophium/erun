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

// EnvironmentStatus is the provisioning lifecycle of a hosted environment
// (#605): a row is `registered` when created, then the server-side deploy
// executor moves it `provisioning` → `running`/`failed`.
type EnvironmentStatus string

const (
	EnvironmentStatusRegistered   EnvironmentStatus = "registered"
	EnvironmentStatusProvisioning EnvironmentStatus = "provisioning"
	EnvironmentStatusRunning      EnvironmentStatus = "running"
	EnvironmentStatusFailed       EnvironmentStatus = "failed"
)

// Environment is the per-tenant system of record for an erun environment, the
// DB-backed shape of eruncommon.EnvConfig. Its link to a context is a real
// tenant-scoped foreign key (ContextID), not a kubernetes-context string match.
type Environment struct {
	bun.BaseModel     `bun:"table:environments,alias:e"`
	EnvironmentID     string          `json:"environmentId" bun:"environment_id,pk,scanonly"`
	TenantID          string          `json:"tenantId" bun:"tenant_id,scanonly"`
	Name              string          `json:"name" bun:"name"`
	Type              EnvironmentType `json:"type" bun:"type"`
	KubernetesContext string          `json:"kubernetesContext,omitempty" bun:"kubernetes_context,nullzero"`
	ContextID         string          `json:"contextId,omitempty" bun:"context_id,nullzero"`
	RuntimeVersion    string          `json:"runtimeVersion,omitempty" bun:"runtime_version,nullzero"`
	// Status is DB-owned: it defaults to `registered` on insert and is moved by
	// the server-side deploy executor, so it is scan-only here (never inserted or
	// updated through the create path). ProvisionError carries the failure detail
	// when Status is `failed`.
	Status         EnvironmentStatus `json:"status" bun:"status,scanonly"`
	ProvisionError string            `json:"provisionError,omitempty" bun:"provision_error,scanonly,nullzero"`
	// DeployedVersion is the version the last successful deploy actually
	// installed, as opposed to RuntimeVersion, which is the declared pin. Owned
	// by the deploy executor, so it is scan-only; a failed deploy leaves it on
	// the version still running in the cluster.
	DeployedVersion string    `json:"deployedVersion,omitempty" bun:"deployed_version,scanonly,nullzero"`
	CreatedAt       time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt       time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
