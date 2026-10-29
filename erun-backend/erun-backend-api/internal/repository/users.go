package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

type UserRepository struct {
	txs *TxManager
}

// UserFilter scopes List; TenantID is required for an operations caller.
type UserFilter struct {
	TenantID string
}

func NewUserRepository(txs *TxManager) *UserRepository {
	return &UserRepository{txs: txs}
}

// CreateUserParams is the enrollment input. TenantID overrides the target
// tenant for operations callers (enforced by the route); RoleIDs replace the
// TenantUser default.
type CreateUserParams struct {
	Username string
	Issuer   string
	Subject  string
	TenantID string
	RoleIDs  []string
}

// errIdentityAlreadyEnrolledRace aborts Create's transaction when a concurrent
// enrollment wins the identity; Create then reports the winner as a no-op.
var errIdentityAlreadyEnrolledRace = errors.New("identity already enrolled (race)")

// Create enrolls a user and links the external identity when Issuer/Subject
// are set. Re-enrolling an identity already mapped in the tenant is a no-op
// (alreadyEnrolled=true) and does not reassign roles.
func (r *UserRepository) Create(ctx context.Context, params CreateUserParams) (model.User, bool, error) {
	username := strings.TrimSpace(params.Username)
	issuer := strings.TrimSpace(params.Issuer)
	subject := strings.TrimSpace(params.Subject)
	tenantID := strings.TrimSpace(params.TenantID)
	roleIDs := trimmedRoleIDs(params.RoleIDs)

	if issuer != "" && subject != "" {
		if existing, found, err := r.findUserByExternalID(ctx, tenantID, issuer, subject); err != nil || found {
			return existing, found, err
		}
	}

	user, err := r.insertUserAndRoles(ctx, tenantID, username, issuer, subject, roleIDs)
	if err == nil {
		return user, false, nil
	}
	if errors.Is(err, errIdentityAlreadyEnrolledRace) {
		return r.resolveIdentityRaceWinner(ctx, tenantID, issuer, subject)
	}
	return model.User{}, false, err
}

func trimmedRoleIDs(roleIDs []string) []string {
	trimmed := make([]string, 0, len(roleIDs))
	for _, roleID := range roleIDs {
		if roleID = strings.TrimSpace(roleID); roleID != "" {
			trimmed = append(trimmed, roleID)
		}
	}
	return trimmed
}

func (r *UserRepository) insertUserAndRoles(ctx context.Context, tenantID string, username string, issuer string, subject string, roleIDs []string) (model.User, error) {
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
			// An unresolvable mapping would leave the new user unable to sign in.
			if err := assertTenantIssuerMappingResolvable(ctx, tx, user.TenantID, issuer); err != nil {
				return err
			}
			if err := insertUserExternalID(ctx, tx, tenantID, user.UserID, issuer, subject); err != nil {
				return err
			}
		}
		return assignEnrollmentRoles(ctx, tx, tenantID, user.UserID, roleIDs, isFirstUser)
	})
	return user, err
}

// resolveIdentityRaceWinner re-resolves the winner of errIdentityAlreadyEnrolledRace.
func (r *UserRepository) resolveIdentityRaceWinner(ctx context.Context, tenantID string, issuer string, subject string) (model.User, bool, error) {
	if existing, found, err := r.findUserByExternalID(ctx, tenantID, issuer, subject); err == nil && found {
		return existing, true, nil
	}
	return model.User{}, false, ErrUnrecognizedConflict
}

// findUserByExternalID looks up the user holding tenantID's (issuer,
// externalID) mapping, falling back to RLS scoping when tenantID is empty.
func (r *UserRepository) findUserByExternalID(ctx context.Context, tenantID string, issuer string, externalID string) (model.User, bool, error) {
	var user model.User
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var scanErr error
		if tenantID != "" {
			scanErr = tx.NewRaw(`
				SELECT u.user_id, u.tenant_id, u.username, u.created_at, u.updated_at
				  FROM user_external_ids uei
				  JOIN users u ON u.tenant_id = uei.tenant_id AND u.user_id = uei.user_id
				 WHERE uei.tenant_id = ? AND uei.issuer = ? AND uei.external_id = ?
			`, tenantID, issuer, externalID).Scan(ctx, &user)
		} else {
			scanErr = tx.NewRaw(`
				SELECT u.user_id, u.tenant_id, u.username, u.created_at, u.updated_at
				  FROM user_external_ids uei
				  JOIN users u ON u.tenant_id = uei.tenant_id AND u.user_id = uei.user_id
				 WHERE uei.issuer = ? AND uei.external_id = ?
			`, issuer, externalID).Scan(ctx, &user)
		}
		return normalizeNoRows(scanErr)
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return model.User{}, false, nil
		}
		return model.User{}, false, err
	}
	return user, true, nil
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
			if name, ok := pgConstraintName(err); ok && name == "users_tenant_username_key" {
				return model.User{}, ErrUsernameConflict
			}
			return model.User{}, ErrUnrecognizedConflict
		}
		return model.User{}, normalizeNoRows(err)
	}
	return user, nil
}

// The primary key is the only unique constraint, so a violation means a
// concurrent enrollment won (see errIdentityAlreadyEnrolledRace).
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
			return errIdentityAlreadyEnrolledRace
		}
		return err
	}
	return nil
}

// assignEnrollmentRoles grants explicit roleIDs, else TenantAdmin to the first
// user, else TenantUser.
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

// tenantHasNoUsers reports whether the tenant has no users yet. tenantID is
// explicit because operations sessions bypass RLS.
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

// DeleteByUsername removes a machine identity's user row, external-identity
// mapping and role grants, reporting whether it existed. It is idempotent for
// workflow retries. The FKs do not cascade, so children are deleted explicitly.
// tenantID is explicit because an operations retry would otherwise be unscoped.
func (r *UserRepository) DeleteByUsername(ctx context.Context, tenantID string, username string) (bool, error) {
	tenantID = strings.TrimSpace(tenantID)
	username = strings.TrimSpace(username)
	if username == "" {
		return false, nil
	}
	// An empty tenant_id matches every tenant under erun_operations.
	if tenantID == "" {
		return false, ErrMissingSecurityContext
	}
	removed := false
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var user model.User
		if err := normalizeNoRows(tx.NewRaw(`
			SELECT user_id, tenant_id, username, created_at, updated_at
			  FROM users
			 WHERE tenant_id = ? AND username = ?
		`, tenantID, username).Scan(ctx, &user)); err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil
			}
			return err
		}
		for _, statement := range []string{
			`DELETE FROM user_external_ids WHERE tenant_id = ? AND user_id = ?`,
			`DELETE FROM user_roles WHERE tenant_id = ? AND user_id = ?`,
			`DELETE FROM users WHERE tenant_id = ? AND user_id = ?`,
		} {
			if _, err := tx.NewRaw(statement, tenantID, user.UserID).Exec(ctx); err != nil {
				return err
			}
		}
		removed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return removed, nil
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

// List returns the filter's target tenant's users. TenantID must be set
// because operations sessions bypass RLS.
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
