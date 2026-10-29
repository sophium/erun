// Package tenant is the desktop's view of a tenant's hosted platform: the
// dashboard, reviews and the merge queue, invite requests, and platform
// environment registration. Every call resolves the tenant's erun alias and
// talks to the platform API through it.
package tenant

import (
	"context"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/cloudstore"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// Store is the slice of the desktop's config store the platform calls read and write.
type Store interface {
	cloudstore.CloudStore
	LoadTenantConfig(string) (model.TenantConfig, string, error)
	SaveTenantConfig(model.TenantConfig) error
}

type Deps struct {
	Store Store
	Cloud cloud.CloudDependencies
	// Context is read per call: the Wails context arrives after the service is built.
	Context func() context.Context
	// MCPBearer signs the per-environment bearer the API-log read presents.
	MCPBearer  func(tenant, environment string) string
	LoadAPILog func(context.Context, DashboardInput) (string, error)
	// ConfiguredERunAliases lists this host's erun platform aliases.
	ConfiguredERunAliases func() ([]string, error)
}

type Service struct {
	d Deps
}

// New panics on a missing seam rather than defaulting it, so a test can never
// reach a real process or network call it did not wire.
func New(d Deps) *Service {
	if d.Store == nil || d.Context == nil || d.MCPBearer == nil || d.LoadAPILog == nil || d.ConfiguredERunAliases == nil {
		panic("tenant.New: Store, Context, MCPBearer, LoadAPILog and ConfiguredERunAliases are required")
	}
	return &Service{d: d}
}
