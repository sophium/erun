package routeroles

import (
	"sort"
	"strings"
	"testing"
)

func permissionSet(permissions []RoutePermission) map[string]bool {
	set := make(map[string]bool, len(permissions))
	for _, permission := range permissions {
		set[permission.Method+" "+permission.Path] = true
	}
	return set
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// permissionsFor silently skips a malformed key, so it must be caught here.
func TestEveryRouteKeyIsWellFormed(t *testing.T) {
	t.Parallel()
	for key := range Routes {
		method, path, ok := splitRouteKey(key)
		if !ok || method == "" || path == "" || !strings.HasPrefix(path, "/") {
			t.Errorf("route key %q does not split into a method and an absolute path", key)
		}
	}
	if _, _, ok := splitRouteKey("GET/v1/reviews"); ok {
		t.Error("expected a key with no separating space to be rejected")
	}
}

func TestTenantAgentIsAStrictSubsetOfTenantUser(t *testing.T) {
	t.Parallel()
	agent := permissionSet(TenantAgentPermissions())
	user := permissionSet(TenantUserPermissions())

	for _, route := range sortedKeys(agent) {
		if !user[route] {
			t.Errorf("%s is granted to TenantAgent but not to TenantUser: the machine role must be a subset of TenantUser's reach", route)
		}
	}
	if len(agent) == 0 {
		t.Fatal("TenantAgent grants nothing -- the derivation is broken, not the classification")
	}
	if len(agent) == len(user) {
		t.Fatal("TenantAgent grants exactly TenantUser's set -- the machine role is meant to be strictly narrower")
	}
}

func TestTenantAdminSupersetOfTenantUser(t *testing.T) {
	t.Parallel()
	admin := permissionSet(TenantAdminPermissions())
	for _, route := range sortedKeys(permissionSet(TenantUserPermissions())) {
		if !admin[route] {
			t.Errorf("%s is granted to TenantUser but not to TenantAdmin", route)
		}
	}
}

// A closed list: widening the machine role must change this test.
func TestTenantAgentPermissionsAreExactlyTheMergeQueueAndSelfReportRoutes(t *testing.T) {
	t.Parallel()
	want := map[string]bool{
		"GET /v1/whoami":                       true,
		"GET /v1/reviews":                      true,
		"GET /v1/reviews/merge-queue":          true,
		"GET /v1/reviews/{review_id}":          true,
		"GET /v1/reviews/{review_id}/comments": true,
		"GET /v1/reviews/{review_id}/builds":   true,
		"POST /v1/reviews/{review_id}/builds":  true,
		"PATCH /v1/reviews/{review_id}/status": true,
		"POST /v1/builds":                      true,
	}
	got := permissionSet(TenantAgentPermissions())

	for _, route := range sortedKeys(want) {
		if !got[route] {
			t.Errorf("%s is not granted to TenantAgent, but the environment's own flow performs it", route)
		}
	}
	for _, route := range sortedKeys(got) {
		if !want[route] {
			t.Errorf("%s is granted to TenantAgent without being part of the machine role's flow", route)
		}
	}
}

func TestTenantAgentIsRefusedTheRoutesThatBreakOrBroadenIt(t *testing.T) {
	t.Parallel()
	agent := permissionSet(TenantAgentPermissions())
	refused := []string{
		// The tenant-wide build history has no environment caller.
		"GET /v1/builds",
		"POST /v1/reviews",
		"POST /v1/reviews/merge-queue/advance",
		"POST /v1/reviews/merge-queue/override-advance",
		"POST /v1/reviews/{review_id}/comments",
		"PATCH /v1/reviews/{review_id}/comments/{comment_id}/status",
		"POST /v1/reviews/{review_id}/reviewers",
		"GET /v1/reviews/{review_id}/reviewers",
		"POST /v1/releases",
		"GET /v1/releases",
		"POST /v1/environments",
		"DELETE /v1/environments/{environment_id}",
		"POST /v1/environments/{environment_id}/deploy",
		"POST /v1/users",
		"GET /v1/users",
		"POST /v1/roles",
		"GET /v1/tenants",
		"GET /v1/config",
		"GET /v1/audit-events",
	}
	for _, route := range refused {
		if agent[route] {
			t.Errorf("%s is granted to TenantAgent but is not part of the machine role", route)
		}
	}
}

func TestOperationsOnlyRoutesGrantNothingToAnyNarrowerRole(t *testing.T) {
	t.Parallel()
	derived := map[string]map[string]bool{
		"TenantUser":  permissionSet(TenantUserPermissions()),
		"TenantAdmin": permissionSet(TenantAdminPermissions()),
		"TenantAgent": permissionSet(TenantAgentPermissions()),
	}
	operationsOnly := 0
	for route, roles := range Routes {
		if roles != OperationsOnly {
			continue
		}
		operationsOnly++
		for role, permitted := range derived {
			if permitted[route] {
				t.Errorf("%s is classified OperationsOnly yet appears in %s's derived grants", route, role)
			}
		}
	}
	if operationsOnly == 0 {
		t.Fatal("found no OperationsOnly route -- the classification changed shape, not just its contents")
	}
}
