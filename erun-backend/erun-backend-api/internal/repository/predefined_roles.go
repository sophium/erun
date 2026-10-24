package repository

import (
	"context"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/routeroles"
	"github.com/uptrace/bun"
)

// Narrower predefined roles, granted exact routes from routeroles. TenantAgent
// is the environment machine role, a strict subset of TenantUser.
const (
	tenantUserRoleName  = "TenantUser"
	tenantAdminRoleName = "TenantAdmin"
)

// TenantAgentRoleName is the role granted to an environment's machine identity.
const TenantAgentRoleName = "TenantAgent"

// derivedRoles is reconciled in this order; hand-authored roles are never touched.
var derivedRoles = []struct {
	name        string
	permissions func() []routeroles.RoutePermission
}{
	{tenantUserRoleName, routeroles.TenantUserPermissions},
	{tenantAdminRoleName, routeroles.TenantAdminPermissions},
	{TenantAgentRoleName, routeroles.TenantAgentPermissions},
}

// ensureNarrowerRolesExist creates each derived role and replaces its grants
// with the current routeroles set on every call, so reclassifications reach
// already-seeded tenants.
func ensureNarrowerRolesExist(ctx context.Context, tx bun.Tx, tenantID string) error {
	for _, derived := range derivedRoles {
		roleID, err := findOrCreateRole(ctx, tx, tenantID, derived.name)
		if err != nil {
			return err
		}
		permissions := derived.permissions()
		if err := reconcileRolePermissions(ctx, tx, tenantID, roleID, permissions); err != nil {
			return err
		}
		for _, permission := range permissions {
			if err := grantRolePermissionExact(ctx, tx, tenantID, roleID, permission.Method, permission.Path); err != nil {
				return err
			}
		}
	}
	return nil
}

// reconcileRolePermissions deletes only grants the derived set omits, keeping
// surviving rows' created_at stable. Pattern rows are removed unconditionally:
// their NULL method/path makes a bare NOT IN evaluate to NULL.
func reconcileRolePermissions(ctx context.Context, tx bun.Tx, tenantID string, roleID string, derived []routeroles.RoutePermission) error {
	tenantPredicate := "tenant_id IS NULL"
	args := []any{}
	if tenantID != "" {
		tenantPredicate = "tenant_id = ?"
		args = append(args, tenantID)
	}
	args = append(args, roleID)

	if len(derived) == 0 {
		_, err := tx.NewRaw(`DELETE FROM role_permissions WHERE `+tenantPredicate+` AND role_id = ?`, args...).Exec(ctx)
		return err
	}

	// Only bound placeholders; the set is bounded by the route table, not caller input.
	pairs := make([]string, 0, len(derived))
	for _, permission := range derived {
		pairs = append(pairs, "(?, ?)")
		args = append(args, permission.Method, permission.Path)
	}
	_, err := tx.NewRaw(
		`DELETE FROM role_permissions WHERE `+tenantPredicate+` AND role_id = ? AND (api_method IS NULL OR (api_method, api_path) NOT IN (VALUES `+strings.Join(pairs, ", ")+`))`,
		args...,
	).Exec(ctx)
	return err
}

// grantFirstTenantUserRole ensures TenantUser/TenantAdmin exist and grants
// TenantAdmin, which can grant roles, to the tenant's first user.
func grantFirstTenantUserRole(ctx context.Context, tx bun.Tx, tenantID string, userID string) error {
	if err := ensureNarrowerRolesExist(ctx, tx, tenantID); err != nil {
		return err
	}
	roleID, err := findOrCreateRole(ctx, tx, tenantID, tenantAdminRoleName)
	if err != nil {
		return err
	}
	return grantUserRole(ctx, tx, tenantID, userID, roleID)
}

// grantDefaultEnrollmentRole grants TenantUser to an enrollment with no roleIDs
// that is not the tenant's first user.
func grantDefaultEnrollmentRole(ctx context.Context, tx bun.Tx, tenantID string, userID string) error {
	if err := ensureNarrowerRolesExist(ctx, tx, tenantID); err != nil {
		return err
	}
	roleID, err := findOrCreateRole(ctx, tx, tenantID, tenantUserRoleName)
	if err != nil {
		return err
	}
	return grantUserRole(ctx, tx, tenantID, userID, roleID)
}

// grantRolePermissionExact idempotently grants one exact (method, path) permission.
func grantRolePermissionExact(ctx context.Context, tx bun.Tx, tenantID string, roleID string, method string, path string) error {
	var err error
	if tenantID != "" {
		_, err = tx.NewRaw(
			`INSERT INTO role_permissions (tenant_id, role_id, api_method, api_path) VALUES (?, ?, ?, ?)
			 ON CONFLICT (tenant_id, role_id, api_method, api_path) DO NOTHING`,
			tenantID, roleID, method, path,
		).Exec(ctx)
	} else {
		_, err = tx.NewRaw(
			`INSERT INTO role_permissions (role_id, api_method, api_path) VALUES (?, ?, ?)
			 ON CONFLICT (tenant_id, role_id, api_method, api_path) DO NOTHING`,
			roleID, method, path,
		).Exec(ctx)
	}
	return err
}
