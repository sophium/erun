package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// tenant_platform_invite_requests.go is the desktop half of the
// invite-requests queue (erun-backend-api's model.InviteRequest): a
// newcomer's "request an invitation" submission, an operator/admin's queue,
// and the sidebar's per-tenant enrollment status glyph. The write path
// (approve/decline) requires TenantAdminOnly on the platform; submit and the
// caller's own status read need only a verified bearer, never tenant
// membership — the same resolveTenantPlatform client used everywhere else,
// since a bearer that mints successfully is exactly what "signed in, not yet
// enrolled" means (tenantPlatformStateNotEnrolled).

// Per-tenant platform-enrollment states the sidebar status icon renders.
// Unlike tenantPlatformState* above (which describes *why the dashboard
// cannot load*), these describe where a local tenant with no platform
// registration stands in the request/approve loop.
const (
	tenantEnrollmentLocalOnly = "local-only"
	tenantEnrollmentPending   = "pending"
	tenantEnrollmentDeclined  = "declined"
	tenantEnrollmentEnrolled  = "enrolled"
	// tenantEnrollmentUnknown is a genuine platform round-trip failure (not
	// "never requested"), so it must never collapse into the confident
	// local-only answer — the sidebar icon renders it as its own state.
	tenantEnrollmentUnknown = "unknown"
	// tenantEnrollmentTenantMismatch is the platform answering the row's own
	// credential with a tenant that is not the row's: the credential
	// authenticated and resolved, just somewhere else. It is deliberately the
	// same verdict, and the same value, the dashboard's own load reaches
	// (tenantPlatformStateTenantMismatch) — the row and the dashboard it opens
	// describe one situation and must not be able to drift apart.
	tenantEnrollmentTenantMismatch = tenantPlatformStateTenantMismatch
)

// uiInviteRequest is the JSON-safe mirror of eruncommon.PlatformInviteRequest
// the frontend renders (request dialog status, operator queue rows).
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

func inviteRequestToUI(request eruncommon.PlatformInviteRequest) uiInviteRequest {
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

// uiSubmitInviteRequestInput is "Request an invitation"'s input: names and
// note only, since the requester's identity comes from the verified bearer,
// never a form field (root AGENTS.md's onboarding rule).
type uiSubmitInviteRequestInput struct {
	Tenant          string `json:"tenant"`
	Kind            string `json:"kind"`
	TenantName      string `json:"tenantName"`
	EnvironmentName string `json:"environmentName,omitempty"`
	Note            string `json:"note,omitempty"`
}

// uiInviteRequestRateLimited is what SubmitTenantInviteRequest returns instead
// of a raw error when the platform's per-identity submission window has not
// elapsed yet — a state to render (disable submit, show the countdown), not
// a fault (root AGENTS.md's "blocked, not broken" distinction).
type uiInviteRequestRateLimited struct {
	RetryAfterSeconds int `json:"retryAfterSeconds"`
}

// uiSubmitInviteRequestOutcome carries exactly one of Request or RateLimited.
type uiSubmitInviteRequestOutcome struct {
	Request     *uiInviteRequest            `json:"request,omitempty"`
	RateLimited *uiInviteRequestRateLimited `json:"rateLimited,omitempty"`
}

// SubmitTenantInviteRequest submits (or updates, if one is already pending
// for this identity) a request to join or create a tenant.
func (a *App) SubmitTenantInviteRequest(input uiSubmitInviteRequestInput) (uiSubmitInviteRequestOutcome, error) {
	tenant, err := requireTenant("requesting an invitation", input.Tenant)
	if err != nil {
		return uiSubmitInviteRequestOutcome{}, err
	}
	kind := strings.TrimSpace(input.Kind)
	if kind != eruncommon.PlatformInviteRequestKindJoinTenant && kind != eruncommon.PlatformInviteRequestKindCreateTenant {
		return uiSubmitInviteRequestOutcome{}, fmt.Errorf("requesting an invitation requires a kind of %q or %q, got %q", eruncommon.PlatformInviteRequestKindJoinTenant, eruncommon.PlatformInviteRequestKindCreateTenant, kind)
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

	request, err := client.SubmitInviteRequest(ctx, eruncommon.PlatformSubmitInviteRequestParams{
		Kind:            kind,
		TenantName:      tenantName,
		EnvironmentName: strings.TrimSpace(input.EnvironmentName),
		Note:            strings.TrimSpace(input.Note),
	})
	if err != nil {
		var statusErr *eruncommon.PlatformStatusError
		if errors.Is(err, eruncommon.ErrPlatformRateLimited) && errors.As(err, &statusErr) {
			seconds := inviteRequestRetryAfterSeconds(ctx, client, statusErr)
			return uiSubmitInviteRequestOutcome{RateLimited: &uiInviteRequestRateLimited{RetryAfterSeconds: seconds}}, nil
		}
		return uiSubmitInviteRequestOutcome{}, err
	}
	ui := inviteRequestToUI(request)
	return uiSubmitInviteRequestOutcome{Request: &ui}, nil
}

// defaultInviteRequestRetryAfterSeconds is the fallback used when a 429's
// Retry-After cannot be resolved to a duration at all (missing header, or
// the RFC 9110 HTTP-date form this client does not parse). The caller was
// genuinely rate limited; reporting 0 seconds would read as "try again now"
// and hide that outcome entirely, which is worse than an approximate wait.
const defaultInviteRequestRetryAfterSeconds = 60

// inviteRequestRetryAfterSeconds resolves how long to tell the caller to
// wait: the header's own value when it parsed, otherwise the platform's
// configured submission window (best effort), otherwise the fallback above.
// A parsed value of exactly zero (the backend rounds a sub-second remainder
// down to "0") is trusted as-is -- that case is a real ok=true, not a
// missing header, and an almost-immediate retry is the correct answer.
func inviteRequestRetryAfterSeconds(ctx context.Context, client *eruncommon.PlatformClient, statusErr *eruncommon.PlatformStatusError) int {
	if retryAfter, ok := statusErr.RetryAfter(); ok {
		return int(retryAfter.Seconds())
	}
	if config, err := client.Config(ctx); err == nil && config.InviteRequestRateLimitWindowSeconds > 0 {
		return config.InviteRequestRateLimitWindowSeconds
	}
	return defaultInviteRequestRetryAfterSeconds
}

// uiTenantInput is the minimal shape shared by the read-only invite-request
// calls below: just which local tenant's resolved platform identity to use.
type uiTenantInput struct {
	Tenant string `json:"tenant"`
}

// GetMyTenantInviteRequest returns the caller's own most recent invite
// request, or a zero value when none has ever been submitted — the one
// status a requester with no tenant membership can check while waiting.
func (a *App) GetMyTenantInviteRequest(input uiTenantInput) (*uiInviteRequest, error) {
	tenant, err := requireTenant("checking your invitation request", input.Tenant)
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
		if errors.Is(err, eruncommon.ErrPlatformNotFound) {
			return nil, nil
		}
		return nil, err
	}
	ui := inviteRequestToUI(request)
	return &ui, nil
}

// uiDecideInviteRequestInput identifies which request an operator/admin is
// deciding on.
type uiDecideInviteRequestInput struct {
	Tenant          string `json:"tenant"`
	InviteRequestID string `json:"inviteRequestId"`
}

// ApproveTenantInviteRequest issues an invitation for a pending request:
// enrolls the requester (into the caller's tenant for JOIN_TENANT, into a
// newly registered tenant for CREATE_TENANT), and mints the invite the
// requester's own next dashboard load resolves against.
func (a *App) ApproveTenantInviteRequest(input uiDecideInviteRequestInput) (uiInviteRequest, error) {
	tenant, err := requireTenant("issuing an invitation", input.Tenant)
	if err != nil {
		return uiInviteRequest{}, err
	}
	inviteRequestID := strings.TrimSpace(input.InviteRequestID)
	if inviteRequestID == "" {
		return uiInviteRequest{}, errors.New("issuing an invitation requires an invite request id")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
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

// uiDeclineInviteRequestInput requires a non-empty reason: a decline with no
// reason reaches nobody, and root AGENTS.md forbids that dead end.
type uiDeclineInviteRequestInput struct {
	Tenant          string `json:"tenant"`
	InviteRequestID string `json:"inviteRequestId"`
	Reason          string `json:"reason"`
}

// DeclineTenantInviteRequest declines a pending request with a reason the
// requester will see on their own next status check.
func (a *App) DeclineTenantInviteRequest(input uiDeclineInviteRequestInput) (uiInviteRequest, error) {
	tenant, err := requireTenant("declining an invitation request", input.Tenant)
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
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client, ctx, cancel, err := a.tenantPlatformClient(ctx, tenant)
	if err != nil {
		return uiInviteRequest{}, err
	}
	defer cancel()
	request, err := client.DeclineInviteRequest(ctx, inviteRequestID, eruncommon.PlatformDeclineInviteRequestParams{Reason: reason})
	if err != nil {
		return uiInviteRequest{}, err
	}
	return inviteRequestToUI(request), nil
}

// uiTenantPlatformEnrollmentStatus is one sidebar tenant row's platform
// enrollment status: the single signal the row's status icon renders.
type uiTenantPlatformEnrollmentStatus struct {
	Tenant        string `json:"tenant"`
	State         string `json:"state"`
	DeclineReason string `json:"declineReason,omitempty"`
	// PlatformHost names the platform this row's credential actually reached,
	// empty when it reached none. The icon's copy speaks of whichever platform
	// answered rather than a fixed hostname: a machine can be configured
	// against more than one, and the one that did not answer is not the one to
	// name.
	PlatformHost string `json:"platformHost,omitempty"`
	// PlatformTenant names the tenant the platform resolved this row's
	// credential to, set only on tenantEnrollmentTenantMismatch. "This row is
	// not the tenant your credential reaches" is a dead end without it; the
	// name is what makes the state actionable.
	PlatformTenant string `json:"platformTenant,omitempty"`
}

// uiListTenantPlatformEnrollmentStatusesInput names which local tenants to
// resolve — the caller (the sidebar) already holds the tenant list and its
// per-tenant environment counts, so this does not re-enumerate local config;
// it only resolves each named tenant's platform state.
type uiListTenantPlatformEnrollmentStatusesInput struct {
	Tenants []string `json:"tenants"`
}

// ListTenantPlatformEnrollmentStatuses resolves each named tenant's platform
// enrollment state for the sidebar's status icon. Best effort per tenant: one
// tenant's resolution failure does not drop the others. A tenant with no
// platform connection at all, or one whose identity has not requested
// anything, reports tenantEnrollmentLocalOnly; a tenant whose platform round
// trip genuinely failed reports tenantEnrollmentUnknown instead — the two
// must never be conflated, or a real outage silently reads as "not on the
// platform yet". A tenant whose credential resolves on the platform to a
// *different* tenant reports tenantEnrollmentTenantMismatch: every answer the
// platform can give about this row would be that other tenant's.
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

// tenantPlatformHost names the host of a platform resolution's API URL, or ""
// when there is none to name. It exists so the enrollment copy can say which
// platform actually answered instead of asserting one: the fixed hostname it
// used to carry is only ever right on a machine talking to that platform, and
// a machine can be configured against several.
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
		// Not connected, choose-alias, or not-signed-in: no bearer to check an
		// invite request against, so there is no route onto the platform yet.
		return status
	}

	status.PlatformHost = tenantPlatformHost(resolution.apiURL)

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, tenantDashboardTimeout)
	defer cancel()

	whoami, whoamiErr := resolution.client.Whoami(requestCtx)
	if whoamiErr == nil {
		// The answer is the point, not the call's success: whoami carries the
		// platform's own statement of *which* tenant this credential resolved
		// to, and a credential that authenticates and resolves elsewhere
		// satisfies a nil error exactly as one resolving here does. Reading
		// only the error is how this row came to claim enrolment for a tenant
		// the platform was never asked about — the same correspondence check
		// the dashboard makes (tenantDashboardTenantMismatch), on the same
		// value, so the row and the dashboard it opens agree.
		if mismatched := tenantDashboardTenantMismatch(tenant, whoami.TenantName); mismatched != "" {
			status.State = tenantEnrollmentTenantMismatch
			status.PlatformTenant = mismatched
			return status
		}
		status.State = tenantEnrollmentEnrolled
		return status
	}
	// This is the same whoami read tenantDashboardIdentityFailure classifies
	// (tenant_dashboard.go): a 403 means the identity already resolved to a
	// tenant user and only the read itself was refused for lacking
	// permissions, which is enrolled, not "not yet" — collapsing it into the
	// invite-request check below would render the not-enrolled glyph for an
	// identity the platform already recognizes.
	//
	// It stays enrolled here, and not because the mismatch check above does
	// not apply to it: that check compares the name whoami answered with, and
	// a 403 answers with none. The 403 body is a bare status string --
	// PlatformAuthErrorCode decodes 401 envelopes only -- so while the platform
	// did resolve *a* tenant for this credential, it declines to say which,
	// and there is nothing to hold against this row. Refusing it would render
	// "not enrolled" for an identity the platform already recognizes, the
	// regression TestListTenantPlatformEnrollmentStatusesEnrolledOnWhoamiForbidden
	// pins. This branch is therefore a "no answer to compare", not a skipped
	// comparison; a platform that learns to name the tenant on 403 gets the
	// mismatch check for free by answering here instead of failing.
	if errors.Is(whoamiErr, eruncommon.ErrPlatformForbidden) {
		status.State = tenantEnrollmentEnrolled
		return status
	}
	if errors.Is(whoamiErr, eruncommon.ErrPlatformUnauthorized) {
		switch eruncommon.PlatformAuthErrorCode(whoamiErr) {
		case "TENANT_UNRESOLVED", "RESOLUTION_FAILED":
			// Neither is an enrollment answer (same line tenantDashboardIdentityFailure
			// and enrollERunPlatformUserError draw): the state genuinely could
			// not be determined, so it must not fall through to "never
			// requested" -> local-only.
			status.State = tenantEnrollmentUnknown
			return status
		}
		// NOT_ENROLLED, or an older platform's unclassified 401: the expected
		// shape of "not enrolled yet, go check the invite request".
		return tenantPlatformEnrollmentStatusFromInviteRequest(requestCtx, resolution.client, status)
	}
	// A network fault or 5xx: the state genuinely could not be determined.
	status.State = tenantEnrollmentUnknown
	return status
}

// tenantPlatformEnrollmentStatusFromInviteRequest fills in status.State from
// the caller's own invite request, once Whoami has already established that
// the identity is verified but not yet enrolled — split out of
// tenantPlatformEnrollmentStatus purely to keep that function's own branch
// count under the module's complexity budget.
func tenantPlatformEnrollmentStatusFromInviteRequest(ctx context.Context, client *eruncommon.PlatformClient, status uiTenantPlatformEnrollmentStatus) uiTenantPlatformEnrollmentStatus {
	request, err := client.MyInviteRequest(ctx)
	if err != nil {
		if errors.Is(err, eruncommon.ErrPlatformNotFound) {
			// Signed in, never requested: genuinely local-only.
			return status
		}
		status.State = tenantEnrollmentUnknown
		return status
	}
	switch request.Status {
	case eruncommon.PlatformInviteRequestStatusPending:
		status.State = tenantEnrollmentPending
	case eruncommon.PlatformInviteRequestStatusDeclined:
		status.State = tenantEnrollmentDeclined
		status.DeclineReason = request.DeclineReason
	case eruncommon.PlatformInviteRequestStatusApproved:
		// The service enrolls the requester's identity the moment it approves
		// (ApproveJoin/ApproveCreateTenant insert the users row directly), so
		// the Whoami call above will already have succeeded on the very next
		// poll — this branch is the brief window before that happens.
		status.State = tenantEnrollmentPending
	}
	return status
}

// loadTenantDashboardMyInviteRequest is LoadTenantDashboard's best-effort
// read of the caller's own invite request, plus the platform's current
// submission window (for the request dialog's pre-submit hint) — both need
// only the bearer already minted for this resolution, not tenant membership,
// so this runs regardless of what loadTenantDashboardData classified
// PlatformState as. A genuine round-trip failure is reported via
// MyInviteRequestError rather than left indistinguishable from "never
// submitted one" (both would otherwise collapse to a nil MyInviteRequest).
func loadTenantDashboardMyInviteRequest(ctx context.Context, client *eruncommon.PlatformClient, dashboard *uiTenantDashboard) {
	request, err := client.MyInviteRequest(ctx)
	switch {
	case err == nil:
		ui := inviteRequestToUI(request)
		dashboard.MyInviteRequest = &ui
	case errors.Is(err, eruncommon.ErrPlatformNotFound):
		// Genuinely never submitted one.
	default:
		dashboard.MyInviteRequestError = err.Error()
	}
	if config, err := client.Config(ctx); err == nil {
		dashboard.InviteRequestRateLimitWindowSeconds = config.InviteRequestRateLimitWindowSeconds
	}
}

// tenantPlatformClientForIdentityCall resolves tenant's platform client for a
// call that needs only a verified bearer, not tenant membership — invite
// request submit/mine. Unlike tenantPlatformClient, a not-enrolled or
// no-permission identity is not an error here: those are exactly the states
// this call exists to serve. Only a resolution that never got a bearer at
// all (not connected, choose-alias, not signed in) is refused.
func (a *App) tenantPlatformClientForIdentityCall(tenant string) (*eruncommon.PlatformClient, context.Context, context.CancelFunc, error) {
	resolution, err := a.resolveTenantPlatform(tenant, "")
	if err != nil {
		return nil, nil, nil, err
	}
	if resolution.client == nil {
		return nil, nil, nil, fmt.Errorf("tenant platform is not ready (%s); connect and sign in first", resolution.state)
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, tenantDashboardTimeout)
	return resolution.client, requestCtx, cancel, nil
}
