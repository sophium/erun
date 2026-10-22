package routeroles

import (
	"sort"
	"strings"
	"testing"
)

// permissionSet turns a derived grant list into the "METHOD path" keys the
// Routes map is keyed by, so a set of grants can be compared with a set of
// route keys directly.
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

// TestEveryRouteKeyIsWellFormed guards the derivation itself: permissionsFor
// silently skips a key it cannot split, so a malformed entry would drop out
// of every derived role's grants with no other symptom.
func TestEveryRouteKeyIsWellFormed(t *testing.T) {
	t.Parallel()
	for key := range Routes {
		method, path, ok := splitRouteKey(key)
		if !ok || method == "" || path == "" || !strings.HasPrefix(path, "/") {
			t.Errorf("route key %q does not split into a method and an absolute path", key)
		}
	}
	// A key the splitter cannot handle must be reported, not skipped.
	if _, _, ok := splitRouteKey("GET/v1/reviews"); ok {
		t.Error("expected a key with no separating space to be rejected")
	}
}

// TestTenantAgentIsAStrictSubsetOfTenantUser is the property the membership
// set exists to make expressible: the machine role must never reach a route
// TenantUser cannot. It is the invariant a fourth single-valued class could
// not have held, because the machine role is deliberately narrower than
// TenantUser rather than a peer of it.
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

// TestTenantAdminSupersetOfTenantUser pins the relationship the three roles
// have carried since TenantAdmin was introduced, now expressed through the
// same masks: everything TenantUser grants, TenantAdmin grants too.
func TestTenantAdminSupersetOfTenantUser(t *testing.T) {
	t.Parallel()
	admin := permissionSet(TenantAdminPermissions())
	for _, route := range sortedKeys(permissionSet(TenantUserPermissions())) {
		if !admin[route] {
			t.Errorf("%s is granted to TenantUser but not to TenantAdmin", route)
		}
	}
}

// TestTenantAgentPermissionsAreExactlyTheGateAndSelfReportRoutes is the
// closed list of what the machine role may do, and it is deliberately closed:
// adding a route to the machine role forces a change here, so the widening is
// a reviewed decision rather than a side effect of a route's classification.
// The two boundaries worth naming are asserted on both sides -- the
// environment's own `erun build` self-report is granted while the tenant-wide
// build history is not, and the reads that make up reading a review are
// granted while writing a comment or assigning a reviewer is not.
func TestTenantAgentPermissionsAreExactlyTheGateAndSelfReportRoutes(t *testing.T) {
	t.Parallel()
	want := map[string]bool{
		// Its own identity and capability set.
		"GET /v1/whoami": true,
		// Reading the reviews it is gated against, and the queue it is
		// promoted from -- including the reads `erun review show` makes.
		"GET /v1/reviews":                      true,
		"GET /v1/reviews/merge-queue":          true,
		"GET /v1/reviews/{review_id}":          true,
		"GET /v1/reviews/{review_id}/comments": true,
		"GET /v1/reviews/{review_id}/builds":   true,
		// Reporting the GATE build, starting and reporting the gate run,
		// reporting the merge, and the unattached build self-report.
		"POST /v1/reviews/{review_id}/builds":  true,
		"PATCH /v1/reviews/{review_id}/status": true,
		"POST /v1/gate-runs":                   true,
		"PATCH /v1/gate-runs/{gate_run_id}":    true,
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

// TestTenantAgentIsRefusedTheRoutesThatBreakOrBroadenIt names the refusals
// the machine role's design turns on, so a later reclassification cannot hand
// any of them back silently.
func TestTenantAgentIsRefusedTheRoutesThatBreakOrBroadenIt(t *testing.T) {
	t.Parallel()
	agent := permissionSet(TenantAgentPermissions())
	refused := []string{
		// The tenant-wide build history: an operator surface with no
		// environment caller. The self-report only needs the POST half.
		"GET /v1/builds",
		// Widening the machine role past the merge-queue workflow.
		"POST /v1/reviews",
		"POST /v1/reviews/merge-queue/advance",
		"POST /v1/reviews/merge-queue/override-advance",
		"POST /v1/reviews/{review_id}/comments",
		"PATCH /v1/reviews/{review_id}/comments/{comment_id}/status",
		"POST /v1/reviews/{review_id}/reviewers",
		"GET /v1/reviews/{review_id}/reviewers",
		"POST /v1/releases",
		"GET /v1/releases",
		// Tenant administration and the operator surface.
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

// TestOperationsOnlyRoutesGrantNothingToAnyNarrowerRole pins the zero value's
// meaning through all three derivations: a route no narrower role grants must
// stay out of every derived set, which is what keeps the wildcard
// ReadAll/WriteAll the only way to reach an operations surface.
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
