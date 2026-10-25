package tenant

import (
	"context"

	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// ReviewCreateCapability gates the diff panel's Start a review
// without loading the whole dashboard.
type ReviewCreateCapability struct {
	CanCreate bool `json:"canCreate"`
	// Restricted is empty when CanCreate is true.
	Restricted string `json:"restricted,omitempty"`
	// AccessRemedy is absent when no role covers the write or roles are unreadable.
	AccessRemedy *platformclient.PlatformAccessRemedy `json:"accessRemedy,omitempty"`
}

// TenantReviewCreateCapability mirrors the dashboard's CanCreateReview
// derivation without loading the dashboard's other reads.
func (s *Service) TenantReviewCreateCapability(tenant string) (ReviewCreateCapability, error) {
	tenant, err := uimodel.RequireTenant("checking whether you can create a review", tenant)
	if err != nil {
		return ReviewCreateCapability{}, err
	}
	ctx := s.d.Context()
	resolution, err := s.Platform(tenant, "")
	if err != nil {
		return ReviewCreateCapability{}, err
	}
	if resolution.State != StateReady {
		return ReviewCreateCapability{
			Restricted: "This tenant's platform connection isn't ready. Open the Reviews tab to connect or sign in.",
		}, nil
	}
	requestCtx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	whoami, err := resolution.Client.Whoami(requestCtx)
	if err != nil {
		return ReviewCreateCapability{}, err
	}
	if restricted := RestrictedRead(whoami.Capabilities, tenantDashboardWriteCreateReview); restricted != "" {
		return ReviewCreateCapability{
			Restricted:   "You do not have access to create reviews.",
			AccessRemedy: loadAccessRemedy(requestCtx, resolution.Client, whoami.Capabilities, whoami.UserID, restricted),
		}, nil
	}
	return ReviewCreateCapability{CanCreate: true}, nil
}
