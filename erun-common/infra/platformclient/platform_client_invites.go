package platformclient

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// PlatformSubmitInviteRequestParams is the invite-request input for a verified
// caller with no tenant yet; the platform takes issuer and subject from the token.
type PlatformSubmitInviteRequestParams struct {
	Email           string `json:"email,omitempty"`
	DisplayName     string `json:"displayName,omitempty"`
	Kind            string `json:"kind"`
	TenantName      string `json:"tenantName"`
	EnvironmentName string `json:"environmentName,omitempty"`
	Note            string `json:"note,omitempty"`
}

// SubmitInviteRequest submits, or updates a PENDING, request to join or create a tenant.
// It returns ErrPlatformRateLimited (with Retry-After) inside the submission window.
func (c *PlatformClient) SubmitInviteRequest(ctx context.Context, params PlatformSubmitInviteRequestParams) (PlatformInviteRequest, error) {
	var request PlatformInviteRequest
	err := c.do(ctx, http.MethodPost, "/v1/invite-requests", params, true, &request)
	return request, err
}

// MyInviteRequest returns the caller's latest invite request, or ErrPlatformNotFound.
func (c *PlatformClient) MyInviteRequest(ctx context.Context) (PlatformInviteRequest, error) {
	var request PlatformInviteRequest
	err := c.do(ctx, http.MethodGet, "/v1/invite-requests/mine", nil, true, &request)
	return request, err
}

// PlatformListInviteRequestsParams filters the queue; Kind applies only to
// operations-scoped callers.
type PlatformListInviteRequestsParams struct {
	Status string
	Kind   string
}

// ListInviteRequests lists visible invite requests, oldest first.
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

// ApproveInviteRequest enrolls the requester (creating the tenant for
// CREATE_TENANT) and mints an invite.
func (c *PlatformClient) ApproveInviteRequest(ctx context.Context, inviteRequestID string) (PlatformInviteRequest, error) {
	var request PlatformInviteRequest
	err := c.do(ctx, http.MethodPost, "/v1/invite-requests/"+url.PathEscape(inviteRequestID)+"/approve", nil, true, &request)
	return request, err
}

// PlatformDeclineInviteRequestParams requires a non-empty reason.
type PlatformDeclineInviteRequestParams struct {
	Reason string `json:"reason"`
}

// DeclineInviteRequest declines a pending invite request with a reason the requester sees.
func (c *PlatformClient) DeclineInviteRequest(ctx context.Context, inviteRequestID string, params PlatformDeclineInviteRequestParams) (PlatformInviteRequest, error) {
	var request PlatformInviteRequest
	err := c.do(ctx, http.MethodPost, "/v1/invite-requests/"+url.PathEscape(inviteRequestID)+"/decline", params, true, &request)
	return request, err
}
