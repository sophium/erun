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
	CreatedAt         time.Time       `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt         time.Time       `json:"updatedAt" bun:"updated_at,scanonly"`
}
