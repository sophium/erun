package model

import (
	"time"

	"github.com/uptrace/bun"
)

// InviteRequestKind names what a request asks for; the kinds need different
// approval authority.
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

// InviteRequest is a verified but unenrolled caller asking to join or create a tenant.
type InviteRequest struct {
	bun.BaseModel   `bun:"table:invite_requests,alias:ir"`
	InviteRequestID string `json:"inviteRequestId" bun:"invite_request_id,pk,scanonly"`
	// Issuer and Subject come from the verified security context, never the caller.
	Issuer      string            `json:"issuer" bun:"issuer,scanonly"`
	Subject     string            `json:"subject" bun:"subject,scanonly"`
	Email       string            `json:"email,omitempty" bun:"email,scanonly"`
	DisplayName string            `json:"displayName,omitempty" bun:"display_name,scanonly"`
	Kind        InviteRequestKind `json:"kind" bun:"kind,scanonly"`
	// TenantName is free text: the tenant may not exist yet, and must not be probeable by name.
	TenantName      string              `json:"tenantName" bun:"tenant_name,scanonly"`
	EnvironmentName string              `json:"environmentName,omitempty" bun:"environment_name,scanonly"`
	Note            string              `json:"note,omitempty" bun:"note,scanonly"`
	Status          InviteRequestStatus `json:"status" bun:"status,scanonly"`
	// DecidedByUserID is read-only, set by approve/decline. It references the global
	// users.user_id: a CREATE_TENANT decider is not a member of the tenant.
	DecidedByUserID string `json:"decidedByUserId,omitempty" bun:"decided_by_user_id,scanonly"`
	DeclineReason   string `json:"declineReason,omitempty" bun:"decline_reason,scanonly"`
	// MintedInviteID/Token/ExpiresAt are read-only, populated once approval mints an invite.
	MintedInviteID        string     `json:"mintedInviteId,omitempty" bun:"minted_invite_id,scanonly"`
	MintedInviteToken     string     `json:"mintedInviteToken,omitempty" bun:"minted_invite_token,scanonly"`
	MintedInviteExpiresAt *time.Time `json:"mintedInviteExpiresAt,omitempty" bun:"minted_invite_expires_at,scanonly"`
	CreatedAt             time.Time  `json:"createdAt" bun:"created_at,scanonly"`
	UpdatedAt             time.Time  `json:"updatedAt" bun:"updated_at,scanonly"`
}
