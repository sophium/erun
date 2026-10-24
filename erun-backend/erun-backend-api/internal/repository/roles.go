package repository

import (
	"context"
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
)

type RoleRepository struct {
	txs *TxManager
}

func NewRoleRepository(txs *TxManager) *RoleRepository {
	return &RoleRepository{txs: txs}
}

// RolePermissionInput sets exactly one of the exact or pattern pair, mirroring
// role_permissions_exact_or_pattern_check.
type RolePermissionInput struct {
	APIMethod        string
	APIPath          string
	APIMethodPattern string
	APIPathPattern   string
}

var validAPIMethods = map[string]bool{
	"GET": true, "HEAD": true, "OPTIONS": true,
	"POST": true, "PUT": true, "PATCH": true, "DELETE": true,
}

func (p RolePermissionInput) validate() error {
	exact := p.APIMethod != "" || p.APIPath != ""
	pattern := p.APIMethodPattern != "" || p.APIPathPattern != ""
	if exact == pattern {
		return ErrInvalidInput
	}
	if exact {
		return p.validateExact()
	}
	return p.validatePattern()
}

func (p RolePermissionInput) validateExact() error {
	if p.APIMethod == "" || p.APIPath == "" || !validAPIMethods[p.APIMethod] {
		return ErrInvalidInput
	}
	return nil
}

func (p RolePermissionInput) validatePattern() error {
	if p.APIMethodPattern == "" || p.APIPathPattern == "" {
		return ErrInvalidInput
	}
	if _, err := regexp.Compile(p.APIMethodPattern); err != nil {
		return ErrInvalidInput
	}
	if _, err := regexp.Compile(p.APIPathPattern); err != nil {
		return ErrInvalidInput
	}
	return nil
}

// List returns the caller's tenant's roles with permissions, first ensuring the
// narrower predefined roles exist. Queries filter tenant_id explicitly because
// erun_operations' RLS policy is unconditional.
func (r *RoleRepository) List(ctx context.Context) ([]model.Role, error) {
	var roles []model.Role
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		if err := ensureNarrowerRolesExist(ctx, tx, securityContext.TenantID); err != nil {
			return err
		}
		if err := tx.NewRaw(`
			SELECT role_id, tenant_id, name, created_at, updated_at
			  FROM roles
			 WHERE tenant_id = ?
			 ORDER BY name
		`, securityContext.TenantID).Scan(ctx, &roles); err != nil {
			return err
		}
		if len(roles) == 0 {
			return nil
		}
		var permissions []model.RolePermission
		if err := tx.NewRaw(`
			SELECT role_permission_id, tenant_id, role_id, api_method, api_path, api_method_pattern, api_path_pattern, created_at, updated_at
			  FROM role_permissions
			 WHERE tenant_id = ?
			 ORDER BY role_id, created_at
		`, securityContext.TenantID).Scan(ctx, &permissions); err != nil {
			return err
		}
		byRole := make(map[string][]model.RolePermission, len(roles))
		for _, permission := range permissions {
			byRole[permission.RoleID] = append(byRole[permission.RoleID], permission)
		}
		for i := range roles {
			roles[i].Permissions = byRole[roles[i].RoleID]
		}
		return nil
	})
	return roles, err
}

// ForUser returns the roles assigned to a user in the caller's tenant.
func (r *RoleRepository) ForUser(ctx context.Context, userID string) ([]model.Role, error) {
	var roles []model.Role
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			SELECT ro.role_id, ro.tenant_id, ro.name, ro.created_at, ro.updated_at
			  FROM user_roles ur
			  JOIN roles ro
			    ON ro.tenant_id = ur.tenant_id
			   AND ro.role_id = ur.role_id
			 WHERE ur.user_id = ?
			 ORDER BY ro.name
		`, userID).Scan(ctx, &roles)
	})
	return roles, err
}

// Create makes a tenant-owned role; at least one permission is required.
func (r *RoleRepository) Create(ctx context.Context, name string, permissions []RolePermissionInput) (model.Role, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(permissions) == 0 {
		return model.Role{}, ErrInvalidInput
	}
	for _, permission := range permissions {
		if err := permission.validate(); err != nil {
			return model.Role{}, err
		}
	}

	var role model.Role
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		role = model.Role{Name: name}
		if err := tx.NewInsert().Model(&role).Column("name").Returning("*").Scan(ctx); err != nil {
			if isUniqueViolation(err) {
				return ErrConflict
			}
			return err
		}
		for _, permission := range permissions {
			rolePermission, err := insertRolePermission(ctx, tx, role.RoleID, permission)
			if err != nil {
				return err
			}
			role.Permissions = append(role.Permissions, rolePermission)
		}
		return nil
	})
	if err != nil {
		return model.Role{}, err
	}
	return role, nil
}

func insertRolePermission(ctx context.Context, tx bun.Tx, roleID string, permission RolePermissionInput) (model.RolePermission, error) {
	rolePermission := model.RolePermission{
		RoleID:           roleID,
		APIMethod:        permission.APIMethod,
		APIPath:          permission.APIPath,
		APIMethodPattern: permission.APIMethodPattern,
		APIPathPattern:   permission.APIPathPattern,
	}
	err := tx.NewInsert().
		Model(&rolePermission).
		Column("role_id", "api_method", "api_path", "api_method_pattern", "api_path_pattern").
		Returning("*").
		Scan(ctx)
	if err != nil {
		if isUniqueViolation(err) {
			return model.RolePermission{}, ErrConflict
		}
		return model.RolePermission{}, err
	}
	return rolePermission, nil
}

// Grant assigns a role to a user. An empty tenantID uses the caller's tenant;
// an operations caller passes another tenant explicitly because the column
// default would fill in the operations tenant and the composite FKs would fail.
func (r *RoleRepository) Grant(ctx context.Context, userID string, roleID string, tenantID string) (model.UserRole, error) {
	userID = strings.TrimSpace(userID)
	roleID = strings.TrimSpace(roleID)
	tenantID = strings.TrimSpace(tenantID)
	grant := model.UserRole{UserID: userID, RoleID: roleID}
	columns := []string{"user_id", "role_id"}
	if tenantID != "" {
		grant.TenantID = tenantID
		columns = append(columns, "tenant_id")
	}
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewInsert().
			Model(&grant).
			Column(columns...).
			Returning("*").
			Scan(ctx)
		if err != nil {
			if isUniqueViolation(err) {
				return ErrConflict
			}
			if isForeignKeyViolation(err) {
				return ErrNotFound
			}
			return err
		}
		return nil
	})
	if err != nil {
		return model.UserRole{}, err
	}
	return grant, nil
}

// Revoke removes a role assignment, refusing (in the same transaction) when the
// tenant would be left with no grant-capable user. Both statements filter on
// the resolved tenant because an operations session bypasses RLS.
func (r *RoleRepository) Revoke(ctx context.Context, userID string, roleID string, tenantID string) error {
	return r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		targetTenantID, err := resolveRoleTenant(ctx, tenantID)
		if err != nil {
			return err
		}
		result, err := tx.NewRaw(`
			DELETE FROM user_roles
			 WHERE tenant_id = ?
			   AND user_id = ?
			   AND role_id = ?
		`, targetTenantID, userID, roleID).Exec(ctx)
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
		stillGrantCapable, err := tenantHasGrantCapableUser(ctx, tx, targetTenantID)
		if err != nil {
			return err
		}
		if !stillGrantCapable {
			return ErrLastGrantCapableRole
		}
		return nil
	})
}

// resolveRoleTenant returns tenantID, or the caller's tenant when empty. The
// route has already authorized an override.
func resolveRoleTenant(ctx context.Context, tenantID string) (string, error) {
	if tenantID = strings.TrimSpace(tenantID); tenantID != "" {
		return tenantID, nil
	}
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return "", ErrMissingSecurityContext
	}
	return securityContext.TenantID, nil
}

// The grant route: while one user can reach it, a tenant can recover its roles.
const (
	grantRoleMethod = "POST"
	grantRolePath   = "/v1/users/{user_id}/roles"
)

// tenantHasGrantCapableUser reports whether any user in tenantID can reach the
// grant route, using Authorize's matcher. tenantID is explicit because
// operations sessions bypass RLS.
func tenantHasGrantCapableUser(ctx context.Context, tx bun.Tx, tenantID string) (bool, error) {
	var rules []permissionRule
	if err := tx.NewRaw(`
		SELECT rp.api_method, rp.api_path, rp.api_method_pattern, rp.api_path_pattern
		  FROM user_roles ur
		  JOIN role_permissions rp
		    ON rp.tenant_id = ur.tenant_id
		   AND rp.role_id = ur.role_id
		 WHERE ur.tenant_id = ?
	`, tenantID).Scan(ctx, &rules); err != nil {
		return false, err
	}
	return rulesAllow(rules, grantRoleMethod, grantRolePath)
}
