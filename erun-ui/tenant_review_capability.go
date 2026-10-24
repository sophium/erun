package main

import (
	"context"

	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// uiTenantReviewCreateCapability gates the diff panel's Start a review
// without loading the whole dashboard.
type uiTenantReviewCreateCapability struct {
	CanCreate bool `json:"canCreate"`
	// Restricted is empty when CanCreate is true.
	Restricted string `json:"restricted,omitempty"`
	// AccessRemedy is absent when no role covers the write or roles are unreadable.
	AccessRemedy *platformclient.PlatformAccessRemedy `json:"accessRemedy,omitempty"`
}

// TenantReviewCreateCapability mirrors the dashboard's CanCreateReview
// derivation without loading the dashboard's other reads.
func (a *App) TenantReviewCreateCapability(tenant string) (uiTenantReviewCreateCapability, error) {
	tenant, err := requireTenant("checking whether you can create a review", tenant)
	if err != nil {
		return uiTenantReviewCreateCapability{}, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	resolution, err := a.resolveTenantPlatform(tenant, "")
	if err != nil {
		return uiTenantReviewCreateCapability{}, err
	}
	if resolution.state != tenantPlatformStateReady {
		return uiTenantReviewCreateCapability{
			Restricted: "This tenant's platform connection isn't ready. Open the Reviews tab to connect or sign in.",
		}, nil
	}
	requestCtx, cancel := context.WithTimeout(ctx, tenantDashboardTimeout)
	defer cancel()
	whoami, err := resolution.client.Whoami(requestCtx)
	if err != nil {
		return uiTenantReviewCreateCapability{}, err
	}
	if restricted := restrictedTenantDashboardRead(whoami.Capabilities, tenantDashboardWriteCreateReview); restricted != "" {
		return uiTenantReviewCreateCapability{
			Restricted:   "You do not have access to create reviews.",
			AccessRemedy: loadAccessRemedy(requestCtx, resolution.client, whoami.Capabilities, whoami.UserID, restricted),
		}, nil
	}
	return uiTenantReviewCreateCapability{CanCreate: true}, nil
}
