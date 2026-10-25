package tenant

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Invite requests need only a verified bearer, never tenant membership;
// approve/decline require TenantAdminOnly on the platform.

// Enrollment states for a local tenant the sidebar status icon renders.
const (
	tenantEnrollmentLocalOnly = "local-only"
	tenantEnrollmentPending   = "pending"
	tenantEnrollmentDeclined  = "declined"
	tenantEnrollmentEnrolled  = "enrolled"
	// tenantEnrollmentUnknown is a failed round trip and must never collapse into
	// local-only.
	tenantEnrollmentUnknown = "unknown"
	// tenantEnrollmentTenantMismatch shares the dashboard's value so the row and
	// the dashboard cannot drift apart.
	tenantEnrollmentTenantMismatch = tenantPlatformStateTenantMismatch
)

// InviteRequest mirrors platformclient.PlatformInviteRequest for the frontend.
type InviteRequest struct {
	InviteRequestID   string `json:"inviteRequestId"`
	Issuer            string `json:"issuer"`
	Subject           string `json:"subject"`
	Email             string `json:"email,omitempty"`
	DisplayName       string `json:"displayName,omitempty"`
	Kind              string `json:"kind"`
	TenantName        string `json:"tenantName"`
	EnvironmentName   string `json:"environmentName,omitempty"`
	Note              string `json:"note,omitempty"`
	Status            string `json:"status"`
	DeclineReason     string `json:"declineReason,omitempty"`
	MintedInviteToken string `json:"mintedInviteToken,omitempty"`
	CreatedAt         string `json:"createdAt,omitempty"`
	UpdatedAt         string `json:"updatedAt,omitempty"`
}

func inviteRequestToUI(request platformclient.PlatformInviteRequest) InviteRequest {
	return InviteRequest{
		InviteRequestID:   request.InviteRequestID,
		Issuer:            request.Issuer,
		Subject:           request.Subject,
		Email:             request.Email,
		DisplayName:       request.DisplayName,
		Kind:              request.Kind,
		TenantName:        request.TenantName,
		EnvironmentName:   request.EnvironmentName,
		Note:              request.Note,
		Status:            request.Status,
		DeclineReason:     request.DeclineReason,
		MintedInviteToken: request.MintedInviteToken,
		CreatedAt:         tenantDashboardTime(request.CreatedAt),
		UpdatedAt:         tenantDashboardTime(request.UpdatedAt),
	}
}

// SubmitInviteRequestInput omits identity: it comes from the verified bearer.
type SubmitInviteRequestInput struct {
	Tenant          string `json:"tenant"`
	Kind            string `json:"kind"`
	TenantName      string `json:"tenantName"`
	EnvironmentName string `json:"environmentName,omitempty"`
	Note            string `json:"note,omitempty"`
}

// InviteRequestRateLimited is a state to render, not a fault.
type InviteRequestRateLimited struct {
	RetryAfterSeconds int `json:"retryAfterSeconds"`
}

// SubmitInviteRequestOutcome carries exactly one of Request or RateLimited.
type SubmitInviteRequestOutcome struct {
	Request     *InviteRequest            `json:"request,omitempty"`
	RateLimited *InviteRequestRateLimited `json:"rateLimited,omitempty"`
}

// SubmitTenantInviteRequest submits, or updates a pending, invite request.
func (s *Service) SubmitTenantInviteRequest(input SubmitInviteRequestInput) (SubmitInviteRequestOutcome, error) {
	tenant, err := uimodel.RequireTenant("requesting an invitation", input.Tenant)
	if err != nil {
		return SubmitInviteRequestOutcome{}, err
	}
	kind := strings.TrimSpace(input.Kind)
	if kind != platformclient.PlatformInviteRequestKindJoinTenant && kind != platformclient.PlatformInviteRequestKindCreateTenant {
		return SubmitInviteRequestOutcome{}, fmt.Errorf("requesting an invitation requires a kind of %q or %q, got %q", platformclient.PlatformInviteRequestKindJoinTenant, platformclient.PlatformInviteRequestKindCreateTenant, kind)
	}
	tenantName := strings.TrimSpace(input.TenantName)
	if tenantName == "" {
		return SubmitInviteRequestOutcome{}, errors.New("requesting an invitation requires a tenant name")
	}

	client, ctx, cancel, err := s.tenantPlatformClientForIdentityCall(tenant)
	if err != nil {
		return SubmitInviteRequestOutcome{}, err
	}
	defer cancel()

	request, err := client.SubmitInviteRequest(ctx, platformclient.PlatformSubmitInviteRequestParams{
		Kind:            kind,
		TenantName:      tenantName,
		EnvironmentName: strings.TrimSpace(input.EnvironmentName),
		Note:            strings.TrimSpace(input.Note),
	})
	if err != nil {
		var statusErr *platformclient.PlatformStatusError
		if errors.Is(err, platformclient.ErrPlatformRateLimited) && errors.As(err, &statusErr) {
			seconds := inviteRequestRetryAfterSeconds(ctx, client, statusErr)
			return SubmitInviteRequestOutcome{RateLimited: &InviteRequestRateLimited{RetryAfterSeconds: seconds}}, nil
		}
		return SubmitInviteRequestOutcome{}, err
	}
	ui := inviteRequestToUI(request)
	return SubmitInviteRequestOutcome{Request: &ui}, nil
}

// defaultInviteRequestRetryAfterSeconds applies when Retry-After is unparseable;
// reporting 0 would hide that the caller was rate limited.
const defaultInviteRequestRetryAfterSeconds = 60

// inviteRequestRetryAfterSeconds trusts a parsed zero: the backend rounds a
// sub-second remainder down.
func inviteRequestRetryAfterSeconds(ctx context.Context, client *platformclient.PlatformClient, statusErr *platformclient.PlatformStatusError) int {
	if retryAfter, ok := statusErr.RetryAfter(); ok {
		return int(retryAfter.Seconds())
	}
	if config, err := client.Config(ctx); err == nil && config.InviteRequestRateLimitWindowSeconds > 0 {
		return config.InviteRequestRateLimitWindowSeconds
	}
	return defaultInviteRequestRetryAfterSeconds
}

// TenantInput selects which local tenant's platform identity to use.
type TenantInput struct {
	Tenant string `json:"tenant"`
}

// GetMyTenantInviteRequest returns the caller's latest invite request, or nil.
func (s *Service) GetMyTenantInviteRequest(input TenantInput) (*InviteRequest, error) {
	tenant, err := uimodel.RequireTenant("checking your invitation request", input.Tenant)
	if err != nil {
		return nil, err
	}
	client, ctx, cancel, err := s.tenantPlatformClientForIdentityCall(tenant)
	if err != nil {
		return nil, err
	}
	defer cancel()

	request, err := client.MyInviteRequest(ctx)
	if err != nil {
		if errors.Is(err, platformclient.ErrPlatformNotFound) {
			return noInviteRequest, nil
		}
		return nil, err
	}
	ui := inviteRequestToUI(request)
	return &ui, nil
}

var noInviteRequest *InviteRequest

// DecideInviteRequestInput identifies the request being decided.
type DecideInviteRequestInput struct {
	Tenant          string `json:"tenant"`
	InviteRequestID string `json:"inviteRequestId"`
}

// ApproveTenantInviteRequest enrolls the requester and mints their invite.
func (s *Service) ApproveTenantInviteRequest(input DecideInviteRequestInput) (InviteRequest, error) {
	tenant, err := uimodel.RequireTenant("issuing an invitation", input.Tenant)
	if err != nil {
		return InviteRequest{}, err
	}
	inviteRequestID := strings.TrimSpace(input.InviteRequestID)
	if inviteRequestID == "" {
		return InviteRequest{}, errors.New("issuing an invitation requires an invite request id")
	}
	ctx := s.d.Context()
	client, ctx, cancel, err := s.Client(ctx, tenant)
	if err != nil {
		return InviteRequest{}, err
	}
	defer cancel()
	request, err := client.ApproveInviteRequest(ctx, inviteRequestID)
	if err != nil {
		return InviteRequest{}, err
	}
	return inviteRequestToUI(request), nil
}

// DeclineInviteRequestInput requires a reason so the decline reaches the requester.
type DeclineInviteRequestInput struct {
	Tenant          string `json:"tenant"`
	InviteRequestID string `json:"inviteRequestId"`
	Reason          string `json:"reason"`
}

// DeclineTenantInviteRequest declines a pending request with a reason.
func (s *Service) DeclineTenantInviteRequest(input DeclineInviteRequestInput) (InviteRequest, error) {
	tenant, err := uimodel.RequireTenant("declining an invitation request", input.Tenant)
	if err != nil {
		return InviteRequest{}, err
	}
	inviteRequestID := strings.TrimSpace(input.InviteRequestID)
	if inviteRequestID == "" {
		return InviteRequest{}, errors.New("declining an invitation request requires an invite request id")
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		return InviteRequest{}, errors.New("declining an invitation request requires a reason")
	}
	ctx := s.d.Context()
	client, ctx, cancel, err := s.Client(ctx, tenant)
	if err != nil {
		return InviteRequest{}, err
	}
	defer cancel()
	request, err := client.DeclineInviteRequest(ctx, inviteRequestID, platformclient.PlatformDeclineInviteRequestParams{Reason: reason})
	if err != nil {
		return InviteRequest{}, err
	}
	return inviteRequestToUI(request), nil
}

// EnrollmentStatus drives one sidebar row's status icon.
type EnrollmentStatus struct {
	Tenant        string `json:"tenant"`
	State         string `json:"state"`
	DeclineReason string `json:"declineReason,omitempty"`
	// PlatformHost is the platform that answered; a machine may have several.
	PlatformHost string `json:"platformHost,omitempty"`
	// PlatformTenant is set only on tenantEnrollmentTenantMismatch.
	PlatformTenant string `json:"platformTenant,omitempty"`
}

// ListEnrollmentStatusesInput names the tenants to resolve.
type ListEnrollmentStatusesInput struct {
	Tenants []string `json:"tenants"`
}

// ListTenantPlatformEnrollmentStatuses resolves each tenant best effort; a
// failed round trip reports unknown, never local-only.
func (s *Service) ListTenantPlatformEnrollmentStatuses(input ListEnrollmentStatusesInput) []EnrollmentStatus {
	statuses := make([]EnrollmentStatus, 0, len(input.Tenants))
	for _, tenant := range input.Tenants {
		tenant = strings.TrimSpace(tenant)
		if tenant == "" {
			continue
		}
		statuses = append(statuses, s.tenantPlatformEnrollmentStatus(tenant))
	}
	return statuses
}

// tenantPlatformHost returns the API URL's host, or "".
func tenantPlatformHost(apiURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(apiURL))
	if err != nil {
		return ""
	}
	return parsed.Host
}

func (s *Service) tenantPlatformEnrollmentStatus(tenant string) EnrollmentStatus {
	status := EnrollmentStatus{Tenant: tenant, State: tenantEnrollmentLocalOnly}
	resolution, err := s.Platform(tenant, "")
	if err != nil || resolution.State != StateReady {
		return status
	}

	status.PlatformHost = tenantPlatformHost(resolution.APIURL)

	ctx := s.d.Context()
	requestCtx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()

	whoami, whoamiErr := resolution.Client.Whoami(requestCtx)
	if whoamiErr == nil {
		// A nil error is not enough: the credential may resolve to another tenant.
		if mismatched := tenantDashboardTenantMismatch(tenant, whoami.TenantName); mismatched != "" {
			status.State = tenantEnrollmentTenantMismatch
			status.PlatformTenant = mismatched
			return status
		}
		status.State = tenantEnrollmentEnrolled
		return status
	}
	// A 403 means the identity resolved but whoami was refused, so it is enrolled.
	// The 403 body names no tenant, so there is nothing to mismatch against.
	if errors.Is(whoamiErr, platformclient.ErrPlatformForbidden) {
		status.State = tenantEnrollmentEnrolled
		return status
	}
	if errors.Is(whoamiErr, platformclient.ErrPlatformUnauthorized) {
		switch platformclient.PlatformAuthErrorCode(whoamiErr) {
		case "TENANT_UNRESOLVED", "RESOLUTION_FAILED":
			// Not an enrollment answer: report unknown, not local-only.
			status.State = tenantEnrollmentUnknown
			return status
		}
		// NOT_ENROLLED, or an older platform's unclassified 401.
		return tenantPlatformEnrollmentStatusFromInviteRequest(requestCtx, resolution.Client, status)
	}
	status.State = tenantEnrollmentUnknown
	return status
}

// tenantPlatformEnrollmentStatusFromInviteRequest runs once whoami showed the
// identity is verified but not enrolled.
func tenantPlatformEnrollmentStatusFromInviteRequest(ctx context.Context, client *platformclient.PlatformClient, status EnrollmentStatus) EnrollmentStatus {
	request, err := client.MyInviteRequest(ctx)
	if err != nil {
		if errors.Is(err, platformclient.ErrPlatformNotFound) {
			return status
		}
		status.State = tenantEnrollmentUnknown
		return status
	}
	switch request.Status {
	case platformclient.PlatformInviteRequestStatusPending:
		status.State = tenantEnrollmentPending
	case platformclient.PlatformInviteRequestStatusDeclined:
		status.State = tenantEnrollmentDeclined
		status.DeclineReason = request.DeclineReason
	case platformclient.PlatformInviteRequestStatusApproved:
		// Approval enrolls immediately; this is the window before the next poll.
		status.State = tenantEnrollmentPending
	}
	return status
}

// loadTenantDashboardMyInviteRequest needs only the bearer, so it runs
// regardless of PlatformState; failures go to MyInviteRequestError rather than
// reading as never submitted.
func loadTenantDashboardMyInviteRequest(ctx context.Context, client *platformclient.PlatformClient, dashboard *Dashboard) {
	request, err := client.MyInviteRequest(ctx)
	switch {
	case err == nil:
		ui := inviteRequestToUI(request)
		dashboard.MyInviteRequest = &ui
	case errors.Is(err, platformclient.ErrPlatformNotFound):
	default:
		dashboard.MyInviteRequestError = err.Error()
	}
	if config, err := client.Config(ctx); err == nil {
		dashboard.InviteRequestRateLimitWindowSeconds = config.InviteRequestRateLimitWindowSeconds
	}
}

// tenantPlatformClientForIdentityCall accepts not-enrolled and no-permission
// identities; only a resolution without a bearer is refused.
func (s *Service) tenantPlatformClientForIdentityCall(tenant string) (*platformclient.PlatformClient, context.Context, context.CancelFunc, error) {
	resolution, err := s.Platform(tenant, "")
	if err != nil {
		return nil, nil, nil, err
	}
	if resolution.Client == nil {
		return nil, nil, nil, fmt.Errorf("tenant platform is not ready (%s); connect and sign in first", resolution.State)
	}
	ctx := s.d.Context()
	requestCtx, cancel := context.WithTimeout(ctx, RequestTimeout)
	return resolution.Client, requestCtx, cancel, nil
}
