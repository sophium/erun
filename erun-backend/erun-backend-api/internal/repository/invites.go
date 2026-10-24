package repository

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

const inviteColumns = `invite_id, tenant_id, created_by_user_id, issuer, token, email, expires_at, consumed_at, created_at, updated_at`

type InviteRepository struct {
	txs *TxManager
}

func NewInviteRepository(txs *TxManager) *InviteRepository {
	return &InviteRepository{txs: txs}
}

// InviteFilter scopes List; TenantID is required for operations callers, whose
// RLS is unconditional.
type InviteFilter struct {
	TenantID string
}

// CreateInviteParams is the invite-creation input. TenantID overrides the target
// tenant (operations only, enforced by the route); Issuer is kept so the
// sessionless accept flow can link the external identity.
type CreateInviteParams struct {
	TenantID string
	Issuer   string
	Email    string
	TTL      time.Duration
}

// Create mints the token itself so a caller can never choose it.
func (r *InviteRepository) Create(ctx context.Context, params CreateInviteParams) (model.Invite, error) {
	tenantID := strings.TrimSpace(params.TenantID)
	issuer := strings.TrimSpace(params.Issuer)
	email := strings.TrimSpace(params.Email)
	token, err := generateInviteToken()
	if err != nil {
		return model.Invite{}, fmt.Errorf("generate invite token: %w", err)
	}
	expiresAt := time.Now().Add(params.TTL)

	var invite model.Invite
	err = r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var scanErr error
		if tenantID != "" {
			scanErr = tx.NewRaw(`
				INSERT INTO invites (tenant_id, issuer, token, email, expires_at)
				VALUES (?, ?, ?, ?, ?)
				RETURNING `+inviteColumns, tenantID, issuer, token, nullIfEmpty(email), expiresAt).Scan(ctx, &invite)
		} else {
			scanErr = tx.NewRaw(`
				INSERT INTO invites (issuer, token, email, expires_at)
				VALUES (?, ?, ?, ?)
				RETURNING `+inviteColumns, issuer, token, nullIfEmpty(email), expiresAt).Scan(ctx, &invite)
		}
		return normalizeNoRows(scanErr)
	})
	if err != nil {
		return model.Invite{}, err
	}
	return invite, nil
}

// List returns the target tenant's unconsumed invites.
func (r *InviteRepository) List(ctx context.Context, filter InviteFilter) ([]model.Invite, error) {
	tenantID := strings.TrimSpace(filter.TenantID)
	var invites []model.Invite
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			SELECT `+inviteColumns+`
			  FROM invites
			 WHERE tenant_id = ?
			   AND consumed_at IS NULL
			 ORDER BY created_at DESC
		`, tenantID).Scan(ctx, &invites)
	})
	return invites, err
}

// Revoke deletes an outstanding invite; RLS scopes which row is reachable.
func (r *InviteRepository) Revoke(ctx context.Context, inviteID string) error {
	return r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewRaw(`DELETE FROM invites WHERE invite_id = ?`, inviteID).Exec(ctx)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrNotFound
		}
		return nil
	})
}

var (
	// ErrInviteExpired reports an unconsumed invite past expires_at.
	ErrInviteExpired = errors.New("invite has expired")
	// ErrInviteConsumed reports an already-used single-use invite.
	ErrInviteConsumed = errors.New("invite has already been used")
)

// ConsumedInvite carries the tenant type so the caller binds the right database role.
type ConsumedInvite struct {
	Invite     model.Invite
	TenantType string
}

// ConsumeByToken atomically validates and consumes an invite under
// WithinSystemTx, since the accept endpoint has no caller identity.
func (r *InviteRepository) ConsumeByToken(ctx context.Context, token string) (ConsumedInvite, error) {
	token = strings.TrimSpace(token)
	var result ConsumedInvite
	err := r.txs.WithinSystemTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var invite model.Invite
		// FOR UPDATE prevents two concurrent accepts both succeeding.
		scanErr := tx.NewRaw(`
			SELECT `+inviteColumns+`
			  FROM invites
			 WHERE token = ?
			 FOR UPDATE
		`, token).Scan(ctx, &invite)
		if err := normalizeNoRows(scanErr); err != nil {
			return err
		}
		if invite.ConsumedAt != nil {
			return ErrInviteConsumed
		}
		if time.Now().After(invite.ExpiresAt) {
			return ErrInviteExpired
		}
		var tenantType string
		if err := tx.NewRaw(`SELECT type FROM tenants WHERE tenant_id = ?`, invite.TenantID).Scan(ctx, &tenantType); err != nil {
			return normalizeNoRows(err)
		}
		if _, err := tx.NewRaw(`UPDATE invites SET consumed_at = now() WHERE invite_id = ?`, invite.InviteID).Exec(ctx); err != nil {
			return err
		}
		invite.Token = "" // consumed; no longer meaningful to hand back
		result = ConsumedInvite{Invite: invite, TenantType: tenantType}
		return nil
	})
	return result, err
}

const inviteTokenBytes = 32

// generateInviteToken mints an unguessable token; it is the credential itself.
func generateInviteToken() (string, error) {
	raw := make([]byte, inviteTokenBytes)
	if _, err := cryptorand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
