package repository

import (
	"context"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

type UserRepository struct {
	txs *TxManager
}

// UserFilter scopes List. TenantID, when set, is the explicit target tenant —
// required for an operations-scoped caller, since erun_operations bypasses RLS
// and would otherwise return every tenant's users.
type UserFilter struct {
	TenantID string
}

func NewUserRepository(txs *TxManager) *UserRepository {
	return &UserRepository{txs: txs}
}

// CreateUserParams is the enrollment input. TenantID, when set, targets a
// tenant other than the caller's own resolved tenant — honored only for an
// operations-scoped caller (enforced by the route, not here).
type CreateUserParams struct {
	Username string
	Issuer   string
	Subject  string
	TenantID string
}

// Create enrolls a user and, when Issuer/Subject are given, links the external
// identity that lets them actually sign in — otherwise the row exists but no
// token can ever resolve to it. It grants the same predefined ReadAll/WriteAll
// roles every bootstrapped user gets, since no finer-grained role assignment
// exists yet. TenantID is written explicitly (never relying on the tenant_id
// column default) whenever the caller is targeting another tenant, because the
// session's own tenant_id default would otherwise write the caller's tenant.
func (r *UserRepository) Create(ctx context.Context, params CreateUserParams) (model.User, error) {
	username := strings.TrimSpace(params.Username)
	issuer := strings.TrimSpace(params.Issuer)
	subject := strings.TrimSpace(params.Subject)
	tenantID := strings.TrimSpace(params.TenantID)

	var user model.User
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var err error
		if tenantID != "" {
			err = tx.NewRaw(`
				INSERT INTO users (tenant_id, username)
				VALUES (?, ?)
				RETURNING user_id, tenant_id, username, created_at, updated_at
			`, tenantID, username).Scan(ctx, &user)
		} else {
			err = tx.NewRaw(`
				INSERT INTO users (username)
				VALUES (?)
				RETURNING user_id, tenant_id, username, created_at, updated_at
			`, username).Scan(ctx, &user)
		}
		if err != nil {
			if isUniqueViolation(err) {
				return ErrConflict
			}
			return normalizeNoRows(err)
		}
		if issuer != "" && subject != "" {
			if tenantID != "" {
				_, err = tx.NewRaw(
					`INSERT INTO user_external_ids (tenant_id, user_id, issuer, external_id) VALUES (?, ?, ?, ?)`,
					tenantID, user.UserID, issuer, subject,
				).Exec(ctx)
			} else {
				_, err = tx.NewRaw(
					`INSERT INTO user_external_ids (user_id, issuer, external_id) VALUES (?, ?, ?)`,
					user.UserID, issuer, subject,
				).Exec(ctx)
			}
			if err != nil {
				if isUniqueViolation(err) {
					return ErrConflict
				}
				return err
			}
		}
		return grantPredefinedRoles(ctx, tx, tenantID, user.UserID)
	})
	if err != nil {
		return model.User{}, err
	}
	return user, nil
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

func (r *UserRepository) RoleNames(ctx context.Context, userID string) ([]string, error) {
	var rows []struct {
		Name string `bun:"name"`
	}
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			SELECT ro.name
			  FROM user_roles ur
			  JOIN roles ro
			    ON ro.tenant_id = ur.tenant_id
			   AND ro.role_id = ur.role_id
			 WHERE ur.user_id = ?
			 ORDER BY ro.name
		`, userID).Scan(ctx, &rows)
	})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}
	return names, nil
}

// List returns the filter's target tenant's users. TenantID is required to be
// meaningful: an operations-scoped caller's session bypasses RLS, so an
// unfiltered query would return every tenant's users rather than being scoped
// implicitly. The route always resolves TenantID explicitly (the caller's own
// tenant by default, an override only for an operations caller).
func (r *UserRepository) List(ctx context.Context, filter UserFilter) ([]model.User, error) {
	tenantID := strings.TrimSpace(filter.TenantID)
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
			 WHERE u.tenant_id = ?
			 ORDER BY u.username, u.user_id
		`, tenantID).Scan(ctx, &users)
	})
	return users, err
}
