package repository

import (
	"context"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/routeroles"
	"github.com/uptrace/bun"
)

// tenantUserRoleName, tenantAdminRoleName and TenantAgentRoleName are the
// narrower predefined roles that ship alongside the wildcard
// ReadAll/WriteAll: TenantUser uses erun without administering it, TenantAdmin
// administers the tenant without the platform-operator reach ReadAll/WriteAll
// carry inside an OPERATIONS tenant, and TenantAgent is the machine role an
// environment's own identity holds — a strict subset of TenantUser's reach,
// because an unattended environment drives the merge-queue gate and reports
// its own build and nothing else. Their permissions are exact (method, path)
// grants taken directly from routeroles, never a hand-authored regex.
const (
	tenantUserRoleName  = "TenantUser"
	tenantAdminRoleName = "TenantAdmin"
)

// TenantAgentRoleName is exported because something outside this package has
// to name it: provisioning an environment's machine identity grants the
// identity this role, and that caller resolves it by name from the roles this
// repository seeds.
const TenantAgentRoleName = "TenantAgent"

// derivedRoles is every narrower predefined role this repository owns,
// paired with the routeroles set its grants are derived from. The order is
// the order they are created in, and nothing outside this list is ever
// reconciled — a role an operator authored by hand keeps whatever it was
// given.
var derivedRoles = []struct {
	name        string
	permissions func() []routeroles.RoutePermission
}{
	{tenantUserRoleName, routeroles.TenantUserPermissions},
	{tenantAdminRoleName, routeroles.TenantAdminPermissions},
	{TenantAgentRoleName, routeroles.TenantAgentPermissions},
}

// ensureNarrowerRolesExist creates every role in derivedRoles for the tenant
// if it does not already exist, and reconciles each one's exact-route
// permission set to the current routeroles set. It runs on every call, even
// when the role rows already exist, and the reconciliation replaces the set
// rather than adding to it: inserts alone (ON CONFLICT DO NOTHING) already let
// an already-bootstrapped tenant pick up a route reclassified *into* one of
// these roles, but nothing ever removed a route reclassified back *out* of it,
// so the narrowing a reclassification is for never reached a tenant that had
// already been seeded — which is every tenant that exists. These roles are
// derived from routeroles and never hand-authored, so replacing their grants
// outright is their whole contract. Never backfilled by a migration; the same
// lazy, idempotent pattern grantPredefinedRoles uses for ReadAll/WriteAll.
//
// TenantAgent is created here rather than by whatever provisions a machine
// user: the role is a property of the tenant's authorization model, and an
// operator can grant it to an identity they enrolled themselves today, before
// anything mints one automatically.
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

// reconcileRolePermissions removes roleID's grants that the derived set no
// longer names, leaving the current set for the idempotent insert loop that
// follows. Deleting only what the set omits — rather than clearing the role
// and re-inserting it whole — keeps every surviving row's created_at stable,
// which matters because this runs on every RoleRepository.List read and not
// only on a role's first creation.
//
// A pattern-form row is removed unconditionally, never by the pair comparison
// below. The derived set is exact-only, so no pattern row can ever be one of
// its members — but a pattern row stores its method and path as NULL
// (role_permissions_exact_or_pattern_check), and a NULL row comparison makes
// `(api_method, api_path) NOT IN (...)` evaluate to NULL rather than TRUE, so
// the row comparison alone silently leaves every pattern grant in place. That
// is the same "the narrowing never reaches a seeded role" failure this whole
// function exists to prevent, one form over, and it is why this predicate
// cannot be a bare NOT IN.
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

	// The row-constructor form of "not one of the derived pairs". Every
	// placeholder is a bound parameter, and the set is bounded by the route
	// table rather than by caller input.
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
