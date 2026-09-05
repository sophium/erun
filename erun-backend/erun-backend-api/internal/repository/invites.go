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

// InviteFilter scopes List. TenantID, when set, is the explicit target
// tenant — required for an operations-scoped caller, since erun_operations
// bypasses RLS and would otherwise return every tenant's invites, mirroring
// UserFilter.
type InviteFilter struct {
	TenantID string
}

// CreateInviteParams is the invite-creation input. TenantID, when set,
// targets a tenant other than the caller's own resolved tenant — honored
// only for an operations-scoped caller (enforced by the route, not here),
// mirroring CreateUserParams. Issuer is the inviter's own authenticated
// issuer, captured so the accept flow (which has no caller session) can
// link the new user's external identity correctly.
type CreateInviteParams struct {
	TenantID string
	Issuer   string
	Email    string
	TTL      time.Duration
}

// Create mints a new invite token and persists its record. The token is
// generated here, not accepted from a caller, so an invite can never be
// created for a token of the caller's choosing.
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

// List returns the filter's target tenant's outstanding (unconsumed)
// invites — the ones an operator can still revoke or hand out again.
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

// Revoke deletes an outstanding invite by its globally unique ID. RLS scopes
// which row a caller can reach the same way Get(ctx, id) does elsewhere in
// this package, so no separate tenant filter is needed here.
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
	// ErrInviteExpired reports a token that resolved to a real, unconsumed
	// invite whose expires_at has already passed.
	ErrInviteExpired = errors.New("invite has expired")
	// ErrInviteConsumed reports a token that has already been used —
	// invites are single-use by design.
	ErrInviteConsumed = errors.New("invite has already been used")
)

// ConsumedInvite is what a successful ConsumeByToken hands back: the invite
// record plus the target tenant's own type, which the caller needs to bind
// the correct database role (erun_tenant vs erun_operations) for the
// enrollment that follows.
type ConsumedInvite struct {
	Invite     model.Invite
	TenantType string
}

// ConsumeByToken atomically validates and marks an invite consumed, for the
// unauthenticated accept endpoint: there is no caller identity yet, so this
// runs under WithinSystemTx (erun_operations, no tenant scoping) rather than
// the normal authenticated WithinTx. ErrNotFound/ErrInviteExpired/
// ErrInviteConsumed are distinguished so the accept flow can tell an
// invitee exactly why their link doesn't work, per #1483's own requirement
// that a stale link says so plainly instead of failing generically.
func (r *InviteRepository) ConsumeByToken(ctx context.Context, token string) (ConsumedInvite, error) {
	token = strings.TrimSpace(token)
	var result ConsumedInvite
	err := r.txs.WithinSystemTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var invite model.Invite
		// FOR UPDATE serializes concurrent accept attempts against the same
		// token so two requests cannot both observe it unconsumed and both
		// succeed.
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

// generateInviteToken mints a high-entropy, URL-safe opaque token — the
// credential is the token itself (the accept endpoint is unauthenticated),
// so it must not be guessable the way a sequential or short ID would be.
func generateInviteToken() (string, error) {
	raw := make([]byte, inviteTokenBytes)
	if _, err := cryptorand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
