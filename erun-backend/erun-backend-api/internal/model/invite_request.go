package model

import (
	"time"

	"github.com/uptrace/bun"
)

// InviteRequestKind names what a request asks for. Approving a JOIN_TENANT
// request enrols the verified subject into an existing tenant; approving a
// CREATE_TENANT request registers a new tenant with the requester's issuer as
// its mapping and enrols them as its first user. The two need different
// authority (see internal/routes/invite_requests.go), so they share one
// table rather than two.
type InviteRequestKind string

const (
	InviteRequestKindJoinTenant   InviteRequestKind = "JOIN_TENANT"
	InviteRequestKindCreateTenant InviteRequestKind = "CREATE_TENANT"
)

type InviteRequestStatus string

const (
	InviteRequestStatusPending  InviteRequestStatus = "PENDING"
	InviteRequestStatusApproved InviteRequestStatus = "APPROVED"
	InviteRequestStatusDeclined InviteRequestStatus = "DECLINED"
)

// InviteRequest is a verified-issuer/subject request to join an existing
// tenant or have a new one registered: a caller who has proven who they are
// at their own IdP, but is enrolled nowhere, asking a platform operator (or,
// for a join, a tenant admin) to let them in.
type InviteRequest struct {
	bun.BaseModel   `bun:"table:invite_requests,alias:ir"`
	InviteRequestID string `json:"inviteRequestId" bun:"invite_request_id,pk,scanonly"`
	// Issuer/Subject are the verified security context's own (iss, sub) —
	// never a caller-supplied value. See routes.submitInviteRequest.
	Issuer      string            `json:"issuer" bun:"issuer,scanonly"`
	Subject     string            `json:"subject" bun:"subject,scanonly"`
	Email       string            `json:"email,omitempty" bun:"email,scanonly"`
	DisplayName string            `json:"displayName,omitempty" bun:"display_name,scanonly"`
	Kind        InviteRequestKind `json:"kind" bun:"kind,scanonly"`
	// TenantName is free text, not a tenant_id: the submitter is
	// unauthenticated-to-the-platform, so the target tenant may not exist yet
	// (CREATE_TENANT) or must not be probeable by name (JOIN_TENANT). It
	// resolves against a real tenant only at approval time.
	TenantName string `json:"tenantName" bun:"tenant_name,scanonly"`
	// EnvironmentName is the requester's local environment, carried only so
	// an approval can prefill it.
	EnvironmentName string              `json:"environmentName,omitempty" bun:"environment_name,scanonly"`
	Note            string              `json:"note,omitempty" bun:"note,scanonly"`
	Status          InviteRequestStatus `json:"status" bun:"status,scanonly"`
	// DecidedByUserID is a read-only field set only by approve/decline; it
	// references the global users.user_id, not the tenant-scoped composite,
	// mirroring Invite.CreatedByUserID (a CREATE_TENANT decision is made by
	// an operations caller who is not a member of the tenant being created).
	DecidedByUserID string `json:"decidedByUserId,omitempty" bun:"decided_by_user_id,scanonly"`
	DeclineReason   string `json:"declineReason,omitempty" bun:"decline_reason,scanonly"`
	// MintedInviteID/Token/ExpiresAt are read-only fields populated once
	// approval mints an invite through the existing invites path (see
	// service.InviteRequestService) — the same link an operator can hand
	// over and the requester's own unauthenticated status read gets back.
	MintedInviteID        string     `json:"mintedInviteId,omitempty" bun:"minted_invite_id,scanonly"`
	MintedInviteToken     string     `json:"mintedInviteToken,omitempty" bun:"minted_invite_token,scanonly"`
	MintedInviteExpiresAt *time.Time `json:"mintedInviteExpiresAt,omitempty" bun:"minted_invite_expires_at,scanonly"`
	CreatedAt             time.Time  `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt             time.Time  `json:"updatedAt" bun:"updated_at,scanonly"`
}
