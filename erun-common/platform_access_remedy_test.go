package eruncommon

import (
	"strings"
	"testing"
)

// reviewerRole grants exactly the review read a caller who cannot open a
// review is missing, through a pattern pair the way a real tenant's role does.
func reviewerRole() PlatformRole {
	return PlatformRole{
		RoleID: "role-reviewer",
		Name:   "Reviewer",
		Permissions: []PlatformRolePermission{
			{APIMethodPattern: "^GET$", APIPathPattern: `^/v1/reviews/[^/]+$`},
		},
	}
}

func TestPlatformAccessRemedyForNamesTheRoleThatCoversTheRead(t *testing.T) {
	remedy, ok := PlatformAccessRemedyFor(
		"user-1",
		[]PlatformRole{
			{RoleID: "role-audit", Name: "Auditor", Permissions: []PlatformRolePermission{
				{APIMethod: "GET", APIPath: "/v1/audit-events"},
			}},
			reviewerRole(),
		},
		"GET", "/v1/reviews/review-1",
	)
	if !ok {
		t.Fatal("expected the reviewer role to cover the review read")
	}
	// The whole point of the hand-over is that the operator pastes it: the
	// caller's own user id and the covering role's id are both already in it.
	if want := "erun platform user grant-role --user-id user-1 --role-id role-reviewer"; remedy.Command != want {
		t.Fatalf("expected %q, got %q", want, remedy.Command)
	}
	if remedy.RoleName != "Reviewer" {
		t.Fatalf("expected the role's own name, got %q", remedy.RoleName)
	}
}

func TestPlatformAccessRemedyForMatchesAnExactPermissionPair(t *testing.T) {
	remedy, ok := PlatformAccessRemedyFor(
		"user-1",
		[]PlatformRole{{
			RoleID: "role-read",
			Name:   "Reader",
			Permissions: []PlatformRolePermission{
				{APIMethod: "GET", APIPath: "/v1/reviews"},
			},
		}},
		"GET", "/v1/reviews",
	)
	if !ok || !strings.Contains(remedy.Command, "--role-id role-read") {
		t.Fatalf("expected an exact pair to cover its own route, got %+v (ok=%v)", remedy, ok)
	}
}

func TestPlatformAccessRemedyForReportsNoRemedyWithoutACoveringRole(t *testing.T) {
	// A role exists, but it grants something else entirely: handing over a
	// command that grants it would be a promise the grant does not keep.
	if remedy, ok := PlatformAccessRemedyFor(
		"user-1",
		[]PlatformRole{{RoleID: "role-audit", Name: "Auditor", Permissions: []PlatformRolePermission{
			{APIMethod: "GET", APIPath: "/v1/audit-events"},
		}}},
		"GET", "/v1/reviews/review-1",
	); ok {
		t.Fatalf("expected no remedy, got %+v", remedy)
	}
}

func TestPlatformAccessRemedyForReportsNoRemedyWithoutAUserID(t *testing.T) {
	// Without the caller's own id there is no command to hand over, and a
	// half-filled one would be a placeholder dressed as an answer.
	if remedy, ok := PlatformAccessRemedyFor("", []PlatformRole{reviewerRole()}, "GET", "/v1/reviews/review-1"); ok {
		t.Fatalf("expected no remedy without a user id, got %+v", remedy)
	}
}

func TestPlatformRoleCoversAnUnparseablePatternNothing(t *testing.T) {
	role := PlatformRole{
		RoleID: "role-broken",
		Name:   "Broken",
		Permissions: []PlatformRolePermission{
			{APIMethodPattern: "^GET$(", APIPathPattern: "^/v1/reviews/.*$"},
		},
	}
	if role.Covers("GET", "/v1/reviews/review-1") {
		t.Fatal("an uncompilable pattern must not be read as covering everything")
	}
}

func TestPlatformRoleRequiresBothHalvesOfAPermission(t *testing.T) {
	role := PlatformRole{
		RoleID:      "role-half",
		Name:        "Half",
		Permissions: []PlatformRolePermission{{APIMethod: "GET"}},
	}
	if role.Covers("GET", "/v1/reviews/review-1") {
		t.Fatal("a permission missing its path must not cover anything")
	}
}

func TestSplitCapabilityReadRejectsAValueThatIsNotARoute(t *testing.T) {
	if _, _, ok := SplitCapabilityRead("the reviewer role"); ok {
		t.Fatal("a denial phrased in prose must not be read as a route")
	}
	method, apiPath, ok := SplitCapabilityRead("GET /v1/reviews/{review_id}")
	if !ok || method != "GET" || apiPath != "/v1/reviews/{review_id}" {
		t.Fatalf("expected the route's halves, got %q %q (ok=%v)", method, apiPath, ok)
	}
}
