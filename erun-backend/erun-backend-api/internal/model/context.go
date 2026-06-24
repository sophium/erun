package model

import (
	"time"

	"github.com/uptrace/bun"
)

// Context mirrors the contexts table — the per-tenant system of record for a
// managed cloud context (cluster), the DB-backed shape of
// eruncommon.CloudContextConfig. The k3s admin token is a server secret and is
// deliberately not part of this read model.
type Context struct {
	bun.BaseModel      `bun:"table:contexts,alias:c"`
	ContextID          string    `json:"contextId" bun:"context_id,pk,scanonly"`
	TenantID           string    `json:"tenantId" bun:"tenant_id,scanonly"`
	Name               string    `json:"name" bun:"name"`
	Provider           string    `json:"provider" bun:"provider"`
	CloudProviderAlias string    `json:"cloudProviderAlias,omitempty" bun:"cloud_provider_alias,nullzero"`
	Region             string    `json:"region,omitempty" bun:"region,nullzero"`
	InstanceID         string    `json:"instanceId,omitempty" bun:"instance_id,nullzero"`
	PublicIP           string    `json:"publicIp,omitempty" bun:"public_ip,nullzero"`
	InstanceType       string    `json:"instanceType,omitempty" bun:"instance_type,nullzero"`
	DiskType           string    `json:"diskType,omitempty" bun:"disk_type,nullzero"`
	DiskSizeGB         int       `json:"diskSizeGb,omitempty" bun:"disk_size_gb,nullzero"`
	KubernetesContext  string    `json:"kubernetesContext,omitempty" bun:"kubernetes_context,nullzero"`
	CreatedAt          time.Time `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt          time.Time `json:"updatedAt" bun:"updated_at,scanonly"`
}
