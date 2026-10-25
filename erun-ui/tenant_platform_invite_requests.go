package main

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

// uiInviteRequest mirrors platformclient.PlatformInviteRequest for the frontend.
type uiInviteRequest struct {
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

func inviteRequestToUI(request platformclient.PlatformInviteRequest) uiInviteRequest {
	return uiInviteRequest{
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

// uiSubmitInviteRequestInput omits identity: it comes from the verified bearer.
type uiSubmitInviteRequestInput struct {
	Tenant          string `json:"tenant"`
	Kind            string `json:"kind"`
	TenantName      string `json:"tenantName"`
	EnvironmentName string `json:"environmentName,omitempty"`
	Note            string `json:"note,omitempty"`
}

// uiInviteRequestRateLimited is a state to render, not a fault.
type uiInviteRequestRateLimited struct {
	RetryAfterSeconds int `json:"retryAfterSeconds"`
}

// uiSubmitInviteRequestOutcome carries exactly one of Request or RateLimited.
type uiSubmitInviteRequestOutcome struct {
	Request     *uiInviteRequest            `json:"request,omitempty"`
	RateLimited *uiInviteRequestRateLimited `json:"rateLimited,omitempty"`
}

// SubmitTenantInviteRequest submits, or updates a pending, invite request.
func (a *App) SubmitTenantInviteRequest(input uiSubmitInviteRequestInput) (uiSubmitInviteRequestOutcome, error) {
	tenant, err := uimodel.RequireTenant("requesting an invitation", input.Tenant)
	if err != nil {
		return uiSubmitInviteRequestOutcome{}, err
	}
	kind := strings.TrimSpace(input.Kind)
	if kind != platformclient.PlatformInviteRequestKindJoinTenant && kind != platformclient.PlatformInviteRequestKindCreateTenant {
		return uiSubmitInviteRequestOutcome{}, fmt.Errorf("requesting an invitation requires a kind of %q or %q, got %q", platformclient.PlatformInviteRequestKindJoinTenant, platformclient.PlatformInviteRequestKindCreateTenant, kind)
	}
	tenantName := strings.TrimSpace(input.TenantName)
	if tenantName == "" {
		return uiSubmitInviteRequestOutcome{}, errors.New("requesting an invitation requires a tenant name")
	}

	client, ctx, cancel, err := a.tenantPlatformClientForIdentityCall(tenant)
	if err != nil {
		return uiSubmitInviteRequestOutcome{}, err
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
			return uiSubmitInviteRequestOutcome{RateLimited: &uiInviteRequestRateLimited{RetryAfterSeconds: seconds}}, nil
		}
		return uiSubmitInviteRequestOutcome{}, err
	}
	ui := inviteRequestToUI(request)
	return uiSubmitInviteRequestOutcome{Request: &ui}, nil
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

// uiTenantInput selects which local tenant's platform identity to use.
type uiTenantInput struct {
	Tenant string `json:"tenant"`
}

// GetMyTenantInviteRequest returns the caller's latest invite request, or nil.
func (a *App) GetMyTenantInviteRequest(input uiTenantInput) (*uiInviteRequest, error) {
	tenant, err := uimodel.RequireTenant("checking your invitation request", input.Tenant)
	if err != nil {
		return nil, err
	}
	client, ctx, cancel, err := a.tenantPlatformClientForIdentityCall(tenant)
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

var noInviteRequest *uiInviteRequest

// uiDecideInviteRequestInput identifies the request being decided.
type uiDecideInviteRequestInput struct {
	Tenant          string `json:"tenant"`
	InviteRequestID string `json:"inviteRequestId"`
}

// ApproveTenantInviteRequest enrolls the requester and mints their invite.
func (a *App) ApproveTenantInviteRequest(input uiDecideInviteRequestInput) (uiInviteRequest, error) {
	tenant, err := uimodel.RequireTenant("issuing an invitation", input.Tenant)
	if err != nil {
		return uiInviteRequest{}, err
	}
	inviteRequestID := strings.TrimSpace(input.InviteRequestID)
	if inviteRequestID == "" {
		return uiInviteRequest{}, errors.New("issuing an invitation requires an invite request id")
	}
	ctx := a.context()
	client, ctx, cancel, err := a.tenantPlatformClient(ctx, tenant)
	if err != nil {
		return uiInviteRequest{}, err
	}
	defer cancel()
	request, err := client.ApproveInviteRequest(ctx, inviteRequestID)
	if err != nil {
		return uiInviteRequest{}, err
	}
	return inviteRequestToUI(request), nil
}

// uiDeclineInviteRequestInput requires a reason so the decline reaches the requester.
type uiDeclineInviteRequestInput struct {
	Tenant          string `json:"tenant"`
	InviteRequestID string `json:"inviteRequestId"`
	Reason          string `json:"reason"`
}

// DeclineTenantInviteRequest declines a pending request with a reason.
func (a *App) DeclineTenantInviteRequest(input uiDeclineInviteRequestInput) (uiInviteRequest, error) {
	tenant, err := uimodel.RequireTenant("declining an invitation request", input.Tenant)
	if err != nil {
		return uiInviteRequest{}, err
	}
	inviteRequestID := strings.TrimSpace(input.InviteRequestID)
	if inviteRequestID == "" {
		return uiInviteRequest{}, errors.New("declining an invitation request requires an invite request id")
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		return uiInviteRequest{}, errors.New("declining an invitation request requires a reason")
	}
	ctx := a.context()
	client, ctx, cancel, err := a.tenantPlatformClient(ctx, tenant)
	if err != nil {
		return uiInviteRequest{}, err
	}
	defer cancel()
	request, err := client.DeclineInviteRequest(ctx, inviteRequestID, platformclient.PlatformDeclineInviteRequestParams{Reason: reason})
	if err != nil {
		return uiInviteRequest{}, err
	}
	return inviteRequestToUI(request), nil
}

// uiTenantPlatformEnrollmentStatus drives one sidebar row's status icon.
type uiTenantPlatformEnrollmentStatus struct {
	Tenant        string `json:"tenant"`
	State         string `json:"state"`
	DeclineReason string `json:"declineReason,omitempty"`
	// PlatformHost is the platform that answered; a machine may have several.
	PlatformHost string `json:"platformHost,omitempty"`
	// PlatformTenant is set only on tenantEnrollmentTenantMismatch.
	PlatformTenant string `json:"platformTenant,omitempty"`
}

// uiListTenantPlatformEnrollmentStatusesInput names the tenants to resolve.
type uiListTenantPlatformEnrollmentStatusesInput struct {
	Tenants []string `json:"tenants"`
}

// ListTenantPlatformEnrollmentStatuses resolves each tenant best effort; a
// failed round trip reports unknown, never local-only.
func (a *App) ListTenantPlatformEnrollmentStatuses(input uiListTenantPlatformEnrollmentStatusesInput) []uiTenantPlatformEnrollmentStatus {
	statuses := make([]uiTenantPlatformEnrollmentStatus, 0, len(input.Tenants))
	for _, tenant := range input.Tenants {
		tenant = strings.TrimSpace(tenant)
		if tenant == "" {
			continue
		}
		statuses = append(statuses, a.tenantPlatformEnrollmentStatus(tenant))
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

func (a *App) tenantPlatformEnrollmentStatus(tenant string) uiTenantPlatformEnrollmentStatus {
	status := uiTenantPlatformEnrollmentStatus{Tenant: tenant, State: tenantEnrollmentLocalOnly}
	resolution, err := a.resolveTenantPlatform(tenant, "")
	if err != nil || resolution.state != tenantPlatformStateReady {
		return status
	}

	status.PlatformHost = tenantPlatformHost(resolution.apiURL)

	ctx := a.context()
	requestCtx, cancel := context.WithTimeout(ctx, tenantDashboardTimeout)
	defer cancel()

	whoami, whoamiErr := resolution.client.Whoami(requestCtx)
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
		return tenantPlatformEnrollmentStatusFromInviteRequest(requestCtx, resolution.client, status)
	}
	status.State = tenantEnrollmentUnknown
	return status
}

// tenantPlatformEnrollmentStatusFromInviteRequest runs once whoami showed the
// identity is verified but not enrolled.
func tenantPlatformEnrollmentStatusFromInviteRequest(ctx context.Context, client *platformclient.PlatformClient, status uiTenantPlatformEnrollmentStatus) uiTenantPlatformEnrollmentStatus {
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
func loadTenantDashboardMyInviteRequest(ctx context.Context, client *platformclient.PlatformClient, dashboard *uiTenantDashboard) {
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
func (a *App) tenantPlatformClientForIdentityCall(tenant string) (*platformclient.PlatformClient, context.Context, context.CancelFunc, error) {
	resolution, err := a.resolveTenantPlatform(tenant, "")
	if err != nil {
		return nil, nil, nil, err
	}
	if resolution.client == nil {
		return nil, nil, nil, fmt.Errorf("tenant platform is not ready (%s); connect and sign in first", resolution.state)
	}
	ctx := a.context()
	requestCtx, cancel := context.WithTimeout(ctx, tenantDashboardTimeout)
	return resolution.client, requestCtx, cancel, nil
}
