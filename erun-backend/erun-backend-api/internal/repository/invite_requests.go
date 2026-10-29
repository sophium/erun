package repository

import (
	"context"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

const inviteRequestColumns = `ir.invite_request_id, ir.issuer, ir.subject, ir.email, ir.display_name, ir.kind, ` +
	`ir.tenant_name, ir.environment_name, ir.note, ir.status, ir.decided_by_user_id, ir.decline_reason, ` +
	`ir.minted_invite_id, inv.token AS minted_invite_token, inv.expires_at AS minted_invite_expires_at, ` +
	`ir.created_at, ir.updated_at`

// inviteRequestFrom joins the minted invite so reads return its token/expiry.
const inviteRequestFrom = `FROM invite_requests ir LEFT JOIN invites inv ON inv.invite_id = ir.minted_invite_id`

// InviteRequestRepository runs every method under WithinSystemTx and filters in
// code: invite_requests is a root table whose submitters have no tenant yet.
type InviteRequestRepository struct {
	txs *TxManager
}

func NewInviteRequestRepository(txs *TxManager) *InviteRequestRepository {
	return &InviteRequestRepository{txs: txs}
}

// SubmitInviteRequestParams is the submit input; Issuer/Subject come from the
// verified token, never the body.
type SubmitInviteRequestParams struct {
	Issuer          string
	Subject         string
	Email           string
	DisplayName     string
	Kind            model.InviteRequestKind
	TenantName      string
	EnvironmentName string
	Note            string
}

// Submit inserts a pending request or updates the caller's existing one; the
// partial unique index is the ON CONFLICT target, so races cannot duplicate.
func (r *InviteRequestRepository) Submit(ctx context.Context, params SubmitInviteRequestParams) (model.InviteRequest, error) {
	var request model.InviteRequest
	err := r.txs.WithinSystemTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			INSERT INTO invite_requests (issuer, subject, email, display_name, kind, tenant_name, environment_name, note)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (issuer, subject) WHERE status = 'PENDING' DO UPDATE SET
				email = EXCLUDED.email,
				display_name = EXCLUDED.display_name,
				kind = EXCLUDED.kind,
				tenant_name = EXCLUDED.tenant_name,
				environment_name = EXCLUDED.environment_name,
				note = EXCLUDED.note
			RETURNING invite_request_id, issuer, subject, email, display_name, kind, tenant_name,
				environment_name, note, status, decided_by_user_id, decline_reason, minted_invite_id,
				NULL::text AS minted_invite_token, NULL::timestamptz AS minted_invite_expires_at,
				created_at, updated_at
		`,
			params.Issuer, params.Subject, nullIfEmpty(strings.TrimSpace(params.Email)),
			nullIfEmpty(strings.TrimSpace(params.DisplayName)), params.Kind, strings.TrimSpace(params.TenantName),
			nullIfEmpty(strings.TrimSpace(params.EnvironmentName)), nullIfEmpty(strings.TrimSpace(params.Note)),
		).Scan(ctx, &request)
	})
	if err != nil {
		return model.InviteRequest{}, err
	}
	return request, nil
}

// Get returns one request by ID; authorization is the caller's responsibility.
func (r *InviteRequestRepository) Get(ctx context.Context, id string) (model.InviteRequest, error) {
	var request model.InviteRequest
	err := r.txs.WithinSystemTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		scanErr := tx.NewRaw(`SELECT `+inviteRequestColumns+` `+inviteRequestFrom+` WHERE ir.invite_request_id = ?`, id).Scan(ctx, &request)
		return normalizeNoRows(scanErr)
	})
	if err != nil {
		return model.InviteRequest{}, err
	}
	return request, nil
}

// GetByIdentity returns the most recent request of any status for (issuer, subject).
func (r *InviteRequestRepository) GetByIdentity(ctx context.Context, issuer string, subject string) (model.InviteRequest, error) {
	var request model.InviteRequest
	err := r.txs.WithinSystemTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		scanErr := tx.NewRaw(`
			SELECT `+inviteRequestColumns+` `+inviteRequestFrom+`
			 WHERE ir.issuer = ? AND ir.subject = ?
			 ORDER BY ir.created_at DESC
			 LIMIT 1
		`, issuer, subject).Scan(ctx, &request)
		return normalizeNoRows(scanErr)
	})
	if err != nil {
		return model.InviteRequest{}, err
	}
	return request, nil
}

// InviteRequestFilter scopes List; TenantName scopes a tenant admin's queue
// since invite_requests has no tenant_id.
type InviteRequestFilter struct {
	Status     model.InviteRequestStatus
	Kind       model.InviteRequestKind
	TenantName string
}

// List returns requests matching filter, oldest first.
func (r *InviteRequestRepository) List(ctx context.Context, filter InviteRequestFilter) ([]model.InviteRequest, error) {
	query := `SELECT ` + inviteRequestColumns + ` ` + inviteRequestFrom + ` WHERE 1 = 1`
	var args []any
	if filter.Status != "" {
		query += ` AND ir.status = ?`
		args = append(args, filter.Status)
	}
	if filter.Kind != "" {
		query += ` AND ir.kind = ?`
		args = append(args, filter.Kind)
	}
	if filter.TenantName != "" {
		query += ` AND ir.tenant_name = ?`
		args = append(args, filter.TenantName)
	}
	query += ` ORDER BY ir.created_at ASC`

	var requests []model.InviteRequest
	err := r.txs.WithinSystemTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(query, args...).Scan(ctx, &requests)
	})
	return requests, err
}

// MarkApproved moves a PENDING request to APPROVED; an already-decided one
// yields ErrConflict.
func (r *InviteRequestRepository) MarkApproved(ctx context.Context, id string, decidedByUserID string, mintedInviteID string) (model.InviteRequest, error) {
	if err := r.markDecided(ctx, `
		UPDATE invite_requests
		   SET status = 'APPROVED', decided_by_user_id = ?, minted_invite_id = ?
		 WHERE invite_request_id = ? AND status = 'PENDING'
	`, nullIfEmpty(decidedByUserID), nullIfEmpty(mintedInviteID), id); err != nil {
		return model.InviteRequest{}, err
	}
	return r.Get(ctx, id)
}

// MarkDeclined moves a PENDING request to DECLINED; a CHECK refuses an empty reason.
func (r *InviteRequestRepository) MarkDeclined(ctx context.Context, id string, decidedByUserID string, reason string) (model.InviteRequest, error) {
	if err := r.markDecided(ctx, `
		UPDATE invite_requests
		   SET status = 'DECLINED', decided_by_user_id = ?, decline_reason = ?
		 WHERE invite_request_id = ? AND status = 'PENDING'
	`, nullIfEmpty(decidedByUserID), reason, id); err != nil {
		return model.InviteRequest{}, err
	}
	return r.Get(ctx, id)
}

func (r *InviteRequestRepository) markDecided(ctx context.Context, query string, args ...any) error {
	return r.txs.WithinSystemTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewRaw(query, args...).Exec(ctx)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrConflict
		}
		return nil
	})
}
