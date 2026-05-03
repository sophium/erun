package repository

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

type UserRepository struct {
	txs *TxManager
}

type UserFilter struct{}

func NewUserRepository(txs *TxManager) *UserRepository {
	return &UserRepository{txs: txs}
}

func (r *UserRepository) Get(ctx context.Context, userID string) (model.User, error) {
	var user model.User
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`
			SELECT u.user_id,
			       u.tenant_id,
			       u.username,
			       u.created_at,
			       u.updated_at,
			       (
			         SELECT uei.issuer
			           FROM user_external_ids uei
			          WHERE uei.tenant_id = u.tenant_id
			            AND uei.user_id = u.user_id
			          ORDER BY uei.created_at, uei.issuer, uei.external_id
			          LIMIT 1
			       ) AS external_issuer,
			       (
			         SELECT uei.external_id
			           FROM user_external_ids uei
			          WHERE uei.tenant_id = u.tenant_id
			            AND uei.user_id = u.user_id
			          ORDER BY uei.created_at, uei.issuer, uei.external_id
			          LIMIT 1
			       ) AS external_user_id
			  FROM users u
			 WHERE u.user_id = ?
		`, userID).Scan(ctx, &user)
		return normalizeNoRows(err)
	})
	return user, err
}

func (r *UserRepository) List(ctx context.Context, _ UserFilter) ([]model.User, error) {
	var users []model.User
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			SELECT u.user_id,
			       u.tenant_id,
			       u.username,
			       u.created_at,
			       u.updated_at,
			       (
			         SELECT uei.issuer
			           FROM user_external_ids uei
			          WHERE uei.tenant_id = u.tenant_id
			            AND uei.user_id = u.user_id
			          ORDER BY uei.created_at, uei.issuer, uei.external_id
			          LIMIT 1
			       ) AS external_issuer,
			       (
			         SELECT uei.external_id
			           FROM user_external_ids uei
			          WHERE uei.tenant_id = u.tenant_id
			            AND uei.user_id = u.user_id
			          ORDER BY uei.created_at, uei.issuer, uei.external_id
			          LIMIT 1
			       ) AS external_user_id
			  FROM users u
			 ORDER BY u.username, u.user_id
		`).Scan(ctx, &users)
	})
	return users, err
}
