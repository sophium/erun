package repository

import (
	"context"
	"errors"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

// DefaultInviteRequestWindowSeconds applies when no platform_rate_limits row exists.
const DefaultInviteRequestWindowSeconds = 60

// PlatformRateLimitRepository has no RLS: platform_rate_limits is a platform
// singleton, written only by operations callers (enforced by the route).
type PlatformRateLimitRepository struct {
	txs *TxManager
}

func NewPlatformRateLimitRepository(txs *TxManager) *PlatformRateLimitRepository {
	return &PlatformRateLimitRepository{txs: txs}
}

// Get returns the rate-limit configuration, defaulted when no row exists. It
// runs under WithinSystemTx because unauthenticated invite requests read it.
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

// SetInviteRequestWindow upserts the invite-request rate-limit window.
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
