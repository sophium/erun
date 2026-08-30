package repository

import (
	"context"
	"errors"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

// DefaultInviteRequestWindowSeconds is the invite-request rate-limit window
// for a platform that has no platform_rate_limits row yet — the same
// "defaults that work when no row exists" shape tenant_quotas already uses.
const DefaultInviteRequestWindowSeconds = 60

// PlatformRateLimitRepository has no RLS and no tenant_id: platform_rate_limits
// is a platform-scoped singleton, like tenants, read by any caller and
// written only by an operations caller (enforced by the route, not here).
type PlatformRateLimitRepository struct {
	txs *TxManager
}

func NewPlatformRateLimitRepository(txs *TxManager) *PlatformRateLimitRepository {
	return &PlatformRateLimitRepository{txs: txs}
}

// Get returns the platform's rate-limit configuration, defaulted when no row
// exists yet. Runs under WithinSystemTx: this is read on every unauthenticated
// POST /v1/invite-requests call, which has no tenant security context to bind.
func (r *PlatformRateLimitRepository) Get(ctx context.Context) (model.PlatformRateLimit, error) {
	var limit model.PlatformRateLimit
	err := r.txs.WithinSystemTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`SELECT invite_request_window_seconds, created_at, updated_at FROM platform_rate_limits`).Scan(ctx, &limit)
		return normalizeNoRows(err)
	})
	if errors.Is(err, ErrNotFound) {
		return model.PlatformRateLimit{InviteRequestWindowSeconds: DefaultInviteRequestWindowSeconds}, nil
	}
	if err != nil {
		return model.PlatformRateLimit{}, err
	}
	return limit, nil
}

// SetInviteRequestWindow upserts the platform's invite-request rate-limit
// window. Operations-only (enforced by the route): read per request, so an
// operator's change governs the very next call with no redeploy.
func (r *PlatformRateLimitRepository) SetInviteRequestWindow(ctx context.Context, windowSeconds int) (model.PlatformRateLimit, error) {
	var limit model.PlatformRateLimit
	err := r.txs.WithinSystemTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			INSERT INTO platform_rate_limits (singleton, invite_request_window_seconds)
			VALUES (TRUE, ?)
			ON CONFLICT (singleton) DO UPDATE SET invite_request_window_seconds = EXCLUDED.invite_request_window_seconds
			RETURNING invite_request_window_seconds, created_at, updated_at
		`, windowSeconds).Scan(ctx, &limit)
	})
	if err != nil {
		return model.PlatformRateLimit{}, err
	}
	return limit, nil
}
