package platformclient

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// PlatformSubmitInviteRequestParams is the unauthenticated-to-tenant
// invite-request submission input (POST /v1/invite-requests). The caller
// still authenticates with a bearer token verified against a trusted issuer
// — it just has no tenant membership yet, which is the whole point of the
// request. Issuer/Subject are never sent: the platform reads them from the
// verified token itself.
type PlatformSubmitInviteRequestParams struct {
	Email           string `json:"email,omitempty"`
	DisplayName     string `json:"displayName,omitempty"`
	Kind            string `json:"kind"`
	TenantName      string `json:"tenantName"`
	EnvironmentName string `json:"environmentName,omitempty"`
	Note            string `json:"note,omitempty"`
}

// SubmitInviteRequest submits (or, for a caller with an existing PENDING
// request, updates in place) a request to join or create a tenant. Requires
// only a verified bearer, never tenant resolution — errors
// ErrPlatformRateLimited (with Retry-After on the returned
// *PlatformStatusError) when the caller's identity is inside the
// platform-configured submission window.
func (c *PlatformClient) SubmitInviteRequest(ctx context.Context, params PlatformSubmitInviteRequestParams) (PlatformInviteRequest, error) {
	var request PlatformInviteRequest
	err := c.do(ctx, http.MethodPost, "/v1/invite-requests", params, true, &request)
	return request, err
}

// MyInviteRequest returns the caller's own most recent invite request (any
// status), resolved from the verified bearer's (issuer, subject) — the one
// thing a requester with no tenant membership can check while waiting.
// Errors ErrPlatformNotFound when the caller has never submitted one.
func (c *PlatformClient) MyInviteRequest(ctx context.Context) (PlatformInviteRequest, error) {
	var request PlatformInviteRequest
	err := c.do(ctx, http.MethodGet, "/v1/invite-requests/mine", nil, true, &request)
	return request, err
}

// PlatformListInviteRequestsParams filters the operator/admin queue. Kind is
// honored only for an operations-scoped caller; a tenant-scoped caller
// always sees only JOIN_TENANT requests naming their own tenant.
type PlatformListInviteRequestsParams struct {
	Status string
	Kind   string
}

// ListInviteRequests lists invite requests visible to the caller, oldest
// first. Requires TenantUserClass.
func (c *PlatformClient) ListInviteRequests(ctx context.Context, params PlatformListInviteRequestsParams) ([]PlatformInviteRequest, error) {
	query := url.Values{}
	if strings.TrimSpace(params.Status) != "" {
		query.Set("status", params.Status)
	}
	if strings.TrimSpace(params.Kind) != "" {
		query.Set("kind", params.Kind)
	}
	path := "/v1/invite-requests"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var requests []PlatformInviteRequest
	err := c.do(ctx, http.MethodGet, path, nil, true, &requests)
	return requests, err
}

// ApproveInviteRequest approves a pending invite request: for a JOIN_TENANT
// request, enrolls the requester into the caller's own tenant; for a
// CREATE_TENANT request, registers the tenant (requires an operations-scoped
// caller) and enrolls the requester as its first user. Either way, mints an
// invite through the same path POST /v1/invites uses. Requires
// TenantAdminOnly. Errors ErrPlatformConflict when the request was already
// decided, ErrPlatformForbidden when the caller lacks authority over it.
func (c *PlatformClient) ApproveInviteRequest(ctx context.Context, inviteRequestID string) (PlatformInviteRequest, error) {
	var request PlatformInviteRequest
	err := c.do(ctx, http.MethodPost, "/v1/invite-requests/"+url.PathEscape(inviteRequestID)+"/approve", nil, true, &request)
	return request, err
}

// PlatformDeclineInviteRequestParams carries the required decline reason: a
// decline with no reason reaches nobody, so the API refuses an empty one.
type PlatformDeclineInviteRequestParams struct {
	Reason string `json:"reason"`
}

// DeclineInviteRequest declines a pending invite request with a reason the
// requester will see. Requires TenantAdminOnly. Errors ErrPlatformConflict
// when the request was already decided, ErrPlatformForbidden when the caller
// lacks authority over it.
func (c *PlatformClient) DeclineInviteRequest(ctx context.Context, inviteRequestID string, params PlatformDeclineInviteRequestParams) (PlatformInviteRequest, error) {
	var request PlatformInviteRequest
	err := c.do(ctx, http.MethodPost, "/v1/invite-requests/"+url.PathEscape(inviteRequestID)+"/decline", params, true, &request)
	return request, err
}
