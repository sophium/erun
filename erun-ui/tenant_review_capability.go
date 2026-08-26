package main

import (
	"context"
	"fmt"
	"strings"
)

// uiTenantReviewCreateCapability reports whether the signed-in user may open
// a review for tenant, resolved independently of the full tenant dashboard
// load so a caller who has not opened the Reviews tab — the diff panel's own
// "Start a review" affordance — can still degrade by permission instead of
// rendering a control that would only fail on submit.
type uiTenantReviewCreateCapability struct {
	CanCreate bool `json:"canCreate"`
	// Restricted names why the caller may not create a review here, in
	// operator language, so the diff panel can show it directly instead of a
	// dead control. Empty means CanCreate is true.
	Restricted string `json:"restricted,omitempty"`
}

// TenantReviewCreateCapability mirrors tenant_dashboard.go's own
// CanCreateReview derivation (resolveTenantPlatform, then Whoami, then
// restrictedTenantDashboardRead against the same write route), without
// loading reviews, the merge queue, builds, or audit events the dashboard
// also reads — this call exists only to gate one button.
func (a *App) TenantReviewCreateCapability(tenant string) (uiTenantReviewCreateCapability, error) {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return uiTenantReviewCreateCapability{}, fmt.Errorf("tenant is required")
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
	if restrictedTenantDashboardRead(whoami.Capabilities, tenantDashboardWriteCreateReview) != "" {
		return uiTenantReviewCreateCapability{Restricted: "You do not have access to create reviews."}, nil
	}
	return uiTenantReviewCreateCapability{CanCreate: true}, nil
}
