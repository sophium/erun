package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type IdentityRepository struct {
	db      *sql.DB
	dialect Dialect
}

func NewIdentityRepository(db *sql.DB, dialect Dialect) *IdentityRepository {
	return &IdentityRepository{db: db, dialect: dialect}
}

func (r *IdentityRepository) ResolveIdentity(ctx context.Context, claims security.Claims) (model.Tenant, model.User, error) {
	tenant, err := r.ResolveTenantByIssuer(ctx, claims.Issuer)
	if err == nil {
		user, err := r.ResolveUserByExternalID(ctx, tenant.TenantID, claims.Issuer, claims.Subject)
		return tenant, user, err
	}
	if !errors.Is(err, ErrNotFound) {
		return model.Tenant{}, model.User{}, err
	}
	return r.bootstrapFirstIdentity(ctx, claims)
}

func (r *IdentityRepository) ResolveTenantByIssuer(ctx context.Context, issuer string) (model.Tenant, error) {
	query := `
		SELECT t.tenant_id, t.name, t.type, t.created_at, t.updated_at
		  FROM tenant_issuers ti
		  JOIN tenants t
		    ON t.tenant_id = ti.tenant_id
		 WHERE ti.issuer = ?
	`
	if r.dialect == DialectPostgres {
		query = NewTxManager(r.db, r.dialect).rebind(query)
	}
	var tenant model.Tenant
	err := r.db.QueryRowContext(ctx, query, issuer).Scan(
		&tenant.TenantID,
		&tenant.Name,
		&tenant.Type,
		&tenant.CreatedAt,
		&tenant.UpdatedAt,
	)
	if err != nil {
		return model.Tenant{}, normalizeNoRows(err)
	}
	return tenant, nil
}

func (r *IdentityRepository) bootstrapFirstIdentity(ctx context.Context, claims security.Claims) (model.Tenant, model.User, error) {
	tenantID, err := newUUIDv7()
	if err != nil {
		return model.Tenant{}, model.User{}, err
	}
	userID, err := newUUIDv7()
	if err != nil {
		return model.Tenant{}, model.User{}, err
	}
	readRoleID, err := newUUIDv7()
	if err != nil {
		return model.Tenant{}, model.User{}, err
	}
	writeRoleID, err := newUUIDv7()
	if err != nil {
		return model.Tenant{}, model.User{}, err
	}
	readPermissionID, err := newUUIDv7()
	if err != nil {
		return model.Tenant{}, model.User{}, err
	}
	writePermissionID, err := newUUIDv7()
	if err != nil {
		return model.Tenant{}, model.User{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Tenant{}, model.User{}, err
	}
	defer tx.Rollback()

	var tenantCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tenants`).Scan(&tenantCount); err != nil {
		return model.Tenant{}, model.User{}, err
	}
	if tenantCount != 0 {
		return model.Tenant{}, model.User{}, ErrNotFound
	}

	if r.dialect == DialectPostgres {
		if err := NewTxManager(r.db, r.dialect).setPostgresSecurityContext(ctx, tx, security.Context{
			TenantID:   tenantID,
			TenantType: string(model.TenantTypeOperations),
			ErunUserID: userID,
		}); err != nil {
			return model.Tenant{}, model.User{}, err
		}
	}

	username := strings.TrimSpace(claims.Username)
	if username == "" {
		username = claims.Subject
	}

	statements := []struct {
		query string
		args  []any
	}{
		{
			query: `INSERT INTO tenants (tenant_id, name, type) VALUES (?, ?, ?)`,
			args:  []any{tenantID, "operations", model.TenantTypeOperations},
		},
		{
			query: `INSERT INTO tenant_issuers (tenant_id, issuer) VALUES (?, ?)`,
			args:  []any{tenantID, claims.Issuer},
		},
		{
			query: `INSERT INTO users (user_id, tenant_id, username) VALUES (?, ?, ?)`,
			args:  []any{userID, tenantID, username},
		},
		{
			query: `INSERT INTO user_external_ids (tenant_id, user_id, issuer, external_id) VALUES (?, ?, ?, ?)`,
			args:  []any{tenantID, userID, claims.Issuer, claims.Subject},
		},
		{
			query: `INSERT INTO roles (role_id, tenant_id, name) VALUES (?, ?, ?)`,
			args:  []any{readRoleID, tenantID, "ReadAll"},
		},
		{
			query: `INSERT INTO roles (role_id, tenant_id, name) VALUES (?, ?, ?)`,
			args:  []any{writeRoleID, tenantID, "WriteAll"},
		},
		{
			query: `INSERT INTO role_permissions (role_permission_id, tenant_id, role_id, api_method_pattern, api_path_pattern) VALUES (?, ?, ?, ?, ?)`,
			args:  []any{readPermissionID, tenantID, readRoleID, "^(GET|HEAD|OPTIONS)$", "^/.*$"},
		},
		{
			query: `INSERT INTO role_permissions (role_permission_id, tenant_id, role_id, api_method_pattern, api_path_pattern) VALUES (?, ?, ?, ?, ?)`,
			args:  []any{writePermissionID, tenantID, writeRoleID, "^(POST|PUT|PATCH|DELETE)$", "^/.*$"},
		},
		{
			query: `INSERT INTO user_roles (tenant_id, user_id, role_id) VALUES (?, ?, ?)`,
			args:  []any{tenantID, userID, readRoleID},
		},
		{
			query: `INSERT INTO user_roles (tenant_id, user_id, role_id) VALUES (?, ?, ?)`,
			args:  []any{tenantID, userID, writeRoleID},
		},
	}
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, r.rebind(stmt.query), stmt.args...); err != nil {
			return model.Tenant{}, model.User{}, err
		}
	}

	tenant, err := r.tenantByID(ctx, tx, tenantID)
	if err != nil {
		return model.Tenant{}, model.User{}, err
	}
	user, err := r.userByID(ctx, tx, tenantID, userID)
	if err != nil {
		return model.Tenant{}, model.User{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Tenant{}, model.User{}, err
	}
	return tenant, user, nil
}

func (r *IdentityRepository) tenantByID(ctx context.Context, tx *sql.Tx, tenantID string) (model.Tenant, error) {
	var tenant model.Tenant
	err := tx.QueryRowContext(ctx, r.rebind(`
		SELECT tenant_id, name, type, created_at, updated_at
		  FROM tenants
		 WHERE tenant_id = ?
	`), tenantID).Scan(
		&tenant.TenantID,
		&tenant.Name,
		&tenant.Type,
		&tenant.CreatedAt,
		&tenant.UpdatedAt,
	)
	if err != nil {
		return model.Tenant{}, normalizeNoRows(err)
	}
	return tenant, nil
}

func (r *IdentityRepository) userByID(ctx context.Context, tx *sql.Tx, tenantID string, userID string) (model.User, error) {
	var user model.User
	err := tx.QueryRowContext(ctx, r.rebind(`
		SELECT user_id, tenant_id, username, created_at, updated_at
		  FROM users
		 WHERE tenant_id = ?
		   AND user_id = ?
	`), tenantID, userID).Scan(
		&user.UserID,
		&user.TenantID,
		&user.Username,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		return model.User{}, normalizeNoRows(err)
	}
	return user, nil
}

func (r *IdentityRepository) rebind(query string) string {
	return NewTxManager(r.db, r.dialect).rebind(query)
}

func newUUIDv7() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func (r *IdentityRepository) ResolveUserByExternalID(ctx context.Context, tenantID string, issuer string, externalID string) (model.User, error) {
	query := `
		SELECT u.user_id, u.tenant_id, u.username, u.created_at, u.updated_at
		  FROM user_external_ids uei
		  JOIN users u
		    ON u.tenant_id = uei.tenant_id
		   AND u.user_id = uei.user_id
		 WHERE uei.tenant_id = ?
		   AND uei.issuer = ?
		   AND uei.external_id = ?
	`
	if r.dialect == DialectPostgres {
		return r.resolveUserByExternalIDPostgres(ctx, query, tenantID, issuer, externalID)
	}
	var user model.User
	err := r.db.QueryRowContext(ctx, query, tenantID, issuer, externalID).Scan(
		&user.UserID,
		&user.TenantID,
		&user.Username,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		return model.User{}, normalizeNoRows(err)
	}
	return user, nil
}

func (r *IdentityRepository) resolveUserByExternalIDPostgres(ctx context.Context, query string, tenantID string, issuer string, externalID string) (model.User, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return model.User{}, err
	}
	defer tx.Rollback()

	if err := NewTxManager(r.db, r.dialect).setPostgresSecurityContext(ctx, tx, security.Context{
		TenantID: tenantID,
	}); err != nil {
		return model.User{}, err
	}

	var user model.User
	err = tx.QueryRowContext(ctx, NewTxManager(r.db, r.dialect).rebind(query), tenantID, issuer, externalID).Scan(
		&user.UserID,
		&user.TenantID,
		&user.Username,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		return model.User{}, normalizeNoRows(err)
	}
	if err := tx.Commit(); err != nil {
		return model.User{}, err
	}
	return user, nil
}
