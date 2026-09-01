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

// RolePermissionInput is a caller-supplied permission grant for role creation.
// Exactly one of the exact pair (APIMethod/APIPath) or the pattern pair
// (APIMethodPattern/APIPathPattern) must be set, mirroring
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
		// Both an exact and a pattern field set, or neither: exactly one form
		// is required, matching role_permissions_exact_or_pattern_check.
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

// List returns the caller's tenant's roles, each with its permissions.
// Ensures TenantUser/TenantAdmin exist (and carry every route routeroles
// currently classifies for them) before reading, so a tenant that bootstrapped
// before these roles shipped — or before a later route reclassification —
// still sees them here without a migration backfill. Both SELECTs filter
// explicitly by the security context's TenantID: erun_operations' RLS policy
// is unconditional, so an OPERATIONS caller would otherwise see every
// tenant's roles and permissions even though this method already reads the
// security context (to bootstrap the narrower roles) and looks scoped.
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

// Create makes a new tenant-owned role with the given permissions. At least
// one permission is required — a role with no permissions could never be
// granted meaningfully.
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

// Grant assigns a role to a user. A role_id or user_id from another tenant is
// invisible under RLS, so the insert's foreign keys report it as not found
// rather than forbidden.
func (r *RoleRepository) Grant(ctx context.Context, userID string, roleID string) (model.UserRole, error) {
	userID = strings.TrimSpace(userID)
	roleID = strings.TrimSpace(roleID)
	grant := model.UserRole{UserID: userID, RoleID: roleID}
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewInsert().
			Model(&grant).
			Column("user_id", "role_id").
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

// Revoke removes a role assignment, refusing when doing so would leave the
// tenant with no user able to grant roles — the one failure this feature must
// make impossible rather than merely recoverable. The check runs inside the
// same transaction as the delete and rolls the delete back on refusal.
func (r *RoleRepository) Revoke(ctx context.Context, userID string, roleID string) error {
	return r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewRaw(`
			DELETE FROM user_roles
			 WHERE user_id = ?
			   AND role_id = ?
		`, userID, roleID).Exec(ctx)
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
		stillGrantCapable, err := tenantHasGrantCapableUser(ctx, tx)
		if err != nil {
			return err
		}
		if !stillGrantCapable {
			return ErrLastGrantCapableRole
		}
		return nil
	})
}

// grantRoleMethod and grantRolePath are the canonical route that lets an
// operator recover from a bad role assignment: as long as one user can reach
// it, the tenant can always grant its way back to a working state.
const (
	grantRoleMethod = "POST"
	grantRolePath   = "/v1/users/{user_id}/roles"
)

// tenantHasGrantCapableUser reports whether any user in the tenant currently
// holds a permission that would authorize granting a role to a user. It reuses
// the same rule matcher Authorize enforces with, so this invariant and request
// enforcement cannot disagree about what "grant-capable" means.
func tenantHasGrantCapableUser(ctx context.Context, tx bun.Tx) (bool, error) {
	var rules []permissionRule
	if err := tx.NewRaw(`
		SELECT rp.api_method, rp.api_path, rp.api_method_pattern, rp.api_path_pattern
		  FROM user_roles ur
		  JOIN role_permissions rp
		    ON rp.tenant_id = ur.tenant_id
		   AND rp.role_id = ur.role_id
	`).Scan(ctx, &rules); err != nil {
		return false, err
	}
	return rulesAllow(rules, grantRoleMethod, grantRolePath)
}
