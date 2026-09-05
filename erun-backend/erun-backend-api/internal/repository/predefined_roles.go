package repository

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/routeroles"
	"github.com/uptrace/bun"
)

// tenantUserRoleName and tenantAdminRoleName are the two narrower predefined
// roles that ship alongside the wildcard ReadAll/WriteAll: TenantUser uses
// erun without administering it, TenantAdmin administers the tenant without
// the platform-operator reach ReadAll/WriteAll carry inside an OPERATIONS
// tenant. Their permissions are exact (method, path) grants taken directly
// from routeroles, never a hand-authored regex.
const (
	tenantUserRoleName  = "TenantUser"
	tenantAdminRoleName = "TenantAdmin"
)

// ensureNarrowerRolesExist creates TenantUser/TenantAdmin for the tenant if
// they do not already exist, and (re)grants each its current exact-route
// permission set from routeroles. The grant loop always runs, even when the
// role rows already exist: every insert is ON CONFLICT DO NOTHING, so this is
// what lets an already-bootstrapped tenant pick up a route that gets
// reclassified into one of these roles later, the next time anything calls
// this — never backfilled by a migration, the same lazy, idempotent pattern
// grantPredefinedRoles already uses for ReadAll/WriteAll.
func ensureNarrowerRolesExist(ctx context.Context, tx bun.Tx, tenantID string) error {
	userRoleID, err := findOrCreateRole(ctx, tx, tenantID, tenantUserRoleName)
	if err != nil {
		return err
	}
	for _, permission := range routeroles.TenantUserPermissions() {
		if err := grantRolePermissionExact(ctx, tx, tenantID, userRoleID, permission.Method, permission.Path); err != nil {
			return err
		}
	}

	adminRoleID, err := findOrCreateRole(ctx, tx, tenantID, tenantAdminRoleName)
	if err != nil {
		return err
	}
	for _, permission := range routeroles.TenantAdminPermissions() {
		if err := grantRolePermissionExact(ctx, tx, tenantID, adminRoleID, permission.Method, permission.Path); err != nil {
			return err
		}
	}
	return nil
}

// grantFirstTenantUserRole ensures TenantUser/TenantAdmin exist for the
// tenant and grants TenantAdmin to userID — the "a tenant needs an admin"
// case (see insertTenantFirstUserAccess and assignEnrollmentRoles). TenantAdmin
// carries POST /v1/users/{user_id}/roles, so this user stays grant-capable
// the same way a ReadAll/WriteAll first user always was.
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

// grantDefaultEnrollmentRole is the deliberate default for an enrollment that
// supplies no roleIDs and is not the tenant's first user: TenantUser, not a
// zero-role default. An invited colleague can read the tenant, drive
// reviews/comments/builds/the merge queue, and operate environments that
// already exist the moment they accept, rather than sitting fully
// capability-less (unable even to read GET /v1/whoami) until someone
// remembers to grant a role by hand.
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

// grantRolePermissionExact grants one exact (method, path) permission,
// mirroring grantRolePermissionPattern's tenantID-branching and ON CONFLICT
// DO NOTHING idempotency for the exact-pair form role_permissions also
// supports.
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
