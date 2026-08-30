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
// operations-scoped caller (enforced by the route, not here). RoleIDs, when
// given, are granted to the new user instead of the TenantUser default; each
// must already exist in the target tenant.
type CreateUserParams struct {
	Username string
	Issuer   string
	Subject  string
	TenantID string
	RoleIDs  []string
}

// Create enrolls a user and, when Issuer/Subject are given, links the external
// identity that lets them actually sign in — otherwise the row exists but no
// token can ever resolve to it.
//
// Role assignment: a caller-named RoleIDs list is granted as given.
// Otherwise, the tenant's first user gets TenantAdmin — without a
// grant-capable role nobody could ever grant a role at all, since granting is
// itself permission-gated — and every later enrollment defaults to
// TenantUser, so an invited colleague can use erun immediately rather than
// sitting fully capability-less until someone grants a role by hand.
func (r *UserRepository) Create(ctx context.Context, params CreateUserParams) (model.User, error) {
	username := strings.TrimSpace(params.Username)
	issuer := strings.TrimSpace(params.Issuer)
	subject := strings.TrimSpace(params.Subject)
	tenantID := strings.TrimSpace(params.TenantID)
	roleIDs := make([]string, 0, len(params.RoleIDs))
	for _, roleID := range params.RoleIDs {
		if roleID = strings.TrimSpace(roleID); roleID != "" {
			roleIDs = append(roleIDs, roleID)
		}
	}

	var user model.User
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		isFirstUser, err := tenantHasNoUsers(ctx, tx, tenantID)
		if err != nil {
			return err
		}
		user, err = insertUserRow(ctx, tx, tenantID, username)
		if err != nil {
			return err
		}
		if issuer != "" && subject != "" {
			if err := insertUserExternalID(ctx, tx, tenantID, user.UserID, issuer, subject); err != nil {
				return err
			}
		}
		return assignEnrollmentRoles(ctx, tx, tenantID, user.UserID, roleIDs, isFirstUser)
	})
	if err != nil {
		return model.User{}, err
	}
	return user, nil
}

func insertUserRow(ctx context.Context, tx bun.Tx, tenantID string, username string) (model.User, error) {
	var user model.User
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
			return model.User{}, ErrConflict
		}
		return model.User{}, normalizeNoRows(err)
	}
	return user, nil
}

func insertUserExternalID(ctx context.Context, tx bun.Tx, tenantID string, userID string, issuer string, subject string) error {
	var err error
	if tenantID != "" {
		_, err = tx.NewRaw(
			`INSERT INTO user_external_ids (tenant_id, user_id, issuer, external_id) VALUES (?, ?, ?, ?)`,
			tenantID, userID, issuer, subject,
		).Exec(ctx)
	} else {
		_, err = tx.NewRaw(
			`INSERT INTO user_external_ids (user_id, issuer, external_id) VALUES (?, ?, ?)`,
			userID, issuer, subject,
		).Exec(ctx)
	}
	if err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return err
	}
	return nil
}

// assignEnrollmentRoles is Create's role-assignment decision: an explicit
// roleIDs list wins outright, the tenant's first user falls back to
// TenantAdmin (grantFirstTenantUserRole), and everyone else defaults to
// TenantUser (grantDefaultEnrollmentRole).
func assignEnrollmentRoles(ctx context.Context, tx bun.Tx, tenantID string, userID string, roleIDs []string, isFirstUser bool) error {
	if len(roleIDs) > 0 {
		for _, roleID := range roleIDs {
			if err := grantUserRole(ctx, tx, tenantID, userID, roleID); err != nil {
				if isForeignKeyViolation(err) {
					return ErrNotFound
				}
				return err
			}
		}
		return nil
	}
	if isFirstUser {
		return grantFirstTenantUserRole(ctx, tx, tenantID, userID)
	}
	return grantDefaultEnrollmentRole(ctx, tx, tenantID, userID)
}

// tenantHasNoUsers reports whether the target tenant currently has zero
// users, which makes the user about to be created its first — the one case
// that gets TenantAdmin rather than the ordinary TenantUser default.
// TenantID, when set, is an operations-scoped caller's explicit override: its
// session bypasses RLS, so an unfiltered count would see every tenant's users
// rather than being scoped implicitly.
func tenantHasNoUsers(ctx context.Context, tx bun.Tx, tenantID string) (bool, error) {
	var count int
	var err error
	if tenantID != "" {
		err = tx.NewRaw(`SELECT count(*) FROM users WHERE tenant_id = ?`, tenantID).Scan(ctx, &count)
	} else {
		err = tx.NewRaw(`SELECT count(*) FROM users`).Scan(ctx, &count)
	}
	if err != nil {
		return false, err
	}
	return count == 0, nil
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
