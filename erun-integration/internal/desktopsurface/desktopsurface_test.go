package desktopsurface

import (
	"strings"
	"testing"
)

func TestFindMissingDesktopSurfaceFlagsAnUndeclaredCapabilityWithNoFrontendReference(t *testing.T) {
	capabilities := []Capability{
		{Name: "whip", Source: "MCP tool", Token: "whip", AgentFacing: false},
	}
	frontend := FrontendSource("export function ReviewDialog() { return null }")

	missing := FindMissingDesktopSurface(capabilities, frontend)

	if len(missing) != 1 || missing[0].Capability.Name != "whip" {
		t.Fatalf("want whip flagged, got %+v", missing)
	}
	if msg := missing[0].Message(); msg == "" {
		t.Fatal("Message() must not be empty; the gate must name the gap and where to fix it")
	}
}

func TestFindMissingDesktopSurfaceClearsACapabilityWithAFrontendReference(t *testing.T) {
	capabilities := []Capability{
		{Name: "deploy", Source: "MCP tool", Token: "deploy", AgentFacing: false},
	}
	frontend := FrontendSource("export function DeployButton() { return null }")

	missing := FindMissingDesktopSurface(capabilities, frontend)

	if len(missing) != 0 {
		t.Fatalf("want deploy cleared by a case-insensitive substring match, got %+v", missing)
	}
}

func TestFindMissingDesktopSurfaceSkipsADeclaredInternalCapability(t *testing.T) {
	capabilities := []Capability{
		{Name: "exec_raw", Source: "MCP tool", Token: "exec_raw", AgentFacing: true},
	}
	frontend := FrontendSource("")

	missing := FindMissingDesktopSurface(capabilities, frontend)

	if len(missing) != 0 {
		t.Fatalf("want an AgentFacing capability skipped even with zero frontend references, got %+v", missing)
	}
}

func TestFindMissingDesktopSurfaceMatchesInsideCamelCaseIdentifiers(t *testing.T) {
	capabilities := []Capability{
		{Name: "whip", Source: "MCP tool", Token: "whip", AgentFacing: false},
	}
	// A plain \bwhip\b word-boundary regex would miss this: there is no
	// non-word character between "whip" and "Button".
	frontend := FrontendSource("export function WhipButton() { return null }")

	missing := FindMissingDesktopSurface(capabilities, frontend)

	if len(missing) != 0 {
		t.Fatalf("want whip cleared by a match embedded in a camelCase identifier, got %+v", missing)
	}
}

func TestAPIRoutePatternMatchesALiteralPathWithNoParameters(t *testing.T) {
	pattern := APIRoutePattern("/v1/roles")
	source := FrontendSource(`fetch("/v1/roles")`)

	if !source.ContainsPattern(pattern) {
		t.Fatalf("want pattern %q to match a literal reference, got no match", pattern)
	}
}

func TestAPIRoutePatternMatchesAParameterizedPathWithAnInterpolatedSegment(t *testing.T) {
	pattern := APIRoutePattern("/v1/users/{user_id}/roles")
	source := FrontendSource("url: `/v1/users/${encodeURIComponent(userId)}/roles`,")

	if !source.ContainsPattern(pattern) {
		t.Fatalf("want pattern %q to match an interpolated path segment, got no match", pattern)
	}
}

// TestAPIRoutePatternDoesNotMatchAnUnrelatedWordSharingTheLastSegment guards
// against the exact trap that motivated using a path pattern instead of a
// plain Token substring for API routes: erun-ui/frontend/src genuinely
// contains a "Roles" table column and a `user.roles` display field with no
// connection to the /v1/roles or /v1/users/{user_id}/roles endpoints, so a
// bare "roles" substring match would have wrongly cleared both.
func TestAPIRoutePatternDoesNotMatchAnUnrelatedWordSharingTheLastSegment(t *testing.T) {
	pattern := APIRoutePattern("/v1/roles")
	source := FrontendSource(`
		<DataTable headers={['Username', 'Roles']}>
		function formatRoles(roles) { return roles.join(', ') }
	`)

	if source.ContainsPattern(pattern) {
		t.Fatalf("want pattern %q to ignore an unrelated 'roles' word with no leading /v1/ path, but it matched", pattern)
	}
}

func TestFindMissingDesktopSurfaceUsesPatternOverTokenWhenBothCouldMatch(t *testing.T) {
	capabilities := []Capability{
		{
			Name:    "GET /v1/roles",
			Source:  "API route",
			Token:   "roles",
			Pattern: APIRoutePattern("/v1/roles"),
		},
	}
	// Contains "roles" as a bare word (would clear a Token match) but not the
	// full "/v1/roles" path (must not clear a Pattern match).
	frontend := FrontendSource(`<DataCell>{formatRoles(user.roles)}</DataCell>`)

	missing := FindMissingDesktopSurface(capabilities, frontend)

	if len(missing) != 1 {
		t.Fatalf("want the Pattern match to win over the looser Token match and flag the route, got %+v", missing)
	}
}

func TestFindMissingDesktopSurfaceClearsAParameterizedAPIRouteReferencedByThePath(t *testing.T) {
	capabilities := []Capability{
		{
			Name:    "DELETE /v1/invites/{invite_id}",
			Source:  "API route",
			Pattern: APIRoutePattern("/v1/invites/{invite_id}"),
		},
	}
	frontend := FrontendSource("url: `/v1/invites/${encodeURIComponent(inviteId)}`,")

	missing := FindMissingDesktopSurface(capabilities, frontend)

	if len(missing) != 0 {
		t.Fatalf("want a parameterized route cleared by an interpolated reference, got %+v", missing)
	}
}

func TestMissingMessageNamesTheCapabilitysOwnDeclarationHint(t *testing.T) {
	capabilities := []Capability{
		{
			Name:            "GET /v1/roles",
			Source:          "API route",
			Pattern:         APIRoutePattern("/v1/roles"),
			DeclarationHint: `erun-backend-api/internal/routes/route_audit.go's InternalAPIRoutes map (add "GET /v1/roles")`,
		},
	}
	frontend := FrontendSource("")

	missing := FindMissingDesktopSurface(capabilities, frontend)

	if len(missing) != 1 {
		t.Fatalf("want the undeclared route flagged, got %+v", missing)
	}
	msg := missing[0].Message()
	if !strings.Contains(msg, "route_audit.go") {
		t.Fatalf("want Message() to name the capability's own DeclarationHint, got %q", msg)
	}
}

// TestFindMissingDesktopSurfaceSkipsAKnownGapCapability locks rule 1 of the
// baseline: a route recorded as a known gap does not fail the gate, the same
// as an AgentFacing one, even with zero frontend references.
func TestFindMissingDesktopSurfaceSkipsAKnownGapCapability(t *testing.T) {
	capabilities := []Capability{
		{Name: "GET /v1/roles", Source: "API route", Pattern: APIRoutePattern("/v1/roles"), KnownGap: true},
	}
	frontend := FrontendSource("")

	missing := FindMissingDesktopSurface(capabilities, frontend)

	if len(missing) != 0 {
		t.Fatalf("want a KnownGap capability skipped even with zero frontend references, got %+v", missing)
	}
}

// TestFindMissingDesktopSurfaceFlagsAnUndeclaredCapabilityNotInTheBaseline
// locks rule 2: a capability that is neither declared internal nor recorded
// in the baseline still fails, so a 34th unsurfaced route cannot land
// silently just because the baseline mechanism exists.
func TestFindMissingDesktopSurfaceFlagsAnUndeclaredCapabilityNotInTheBaseline(t *testing.T) {
	capabilities := []Capability{
		{Name: "GET /v1/widgets", Source: "API route", Pattern: APIRoutePattern("/v1/widgets")},
	}
	frontend := FrontendSource("")

	missing := FindMissingDesktopSurface(capabilities, frontend)

	if len(missing) != 1 || missing[0].Capability.Name != "GET /v1/widgets" {
		t.Fatalf("want the undeclared, unbaselined route flagged, got %+v", missing)
	}
}

// TestFindStaleBaselineEntriesFlagsAKnownGapThatGainedASurface locks rule 3:
// the baseline can only shrink. A capability recorded as a known gap that has
// since gained a real frontend reference must fail, not silently pass --
// otherwise nobody notices the baseline could be shorter.
func TestFindStaleBaselineEntriesFlagsAKnownGapThatGainedASurface(t *testing.T) {
	capabilities := []Capability{
		{Name: "GET /v1/roles", Source: "API route", Pattern: APIRoutePattern("/v1/roles"), KnownGap: true},
	}
	frontend := FrontendSource(`fetch("/v1/roles")`)

	stale := FindStaleBaselineEntries(capabilities, frontend)

	if len(stale) != 1 || stale[0].Capability.Name != "GET /v1/roles" {
		t.Fatalf("want the now-surfaced baseline entry flagged as stale, got %+v", stale)
	}
	if msg := stale[0].Message(); msg == "" {
		t.Fatal("Message() must not be empty; the gate must name the removal fix")
	}
}

// TestFindStaleBaselineEntriesClearsAKnownGapStillMissingItsSurface is the
// negative case for rule 3: a baselined route that still has no frontend
// reference is not stale -- it is exactly the gap the baseline was meant to
// record, and rule 1 (not rule 3) governs it.
func TestFindStaleBaselineEntriesClearsAKnownGapStillMissingItsSurface(t *testing.T) {
	capabilities := []Capability{
		{Name: "GET /v1/roles", Source: "API route", Pattern: APIRoutePattern("/v1/roles"), KnownGap: true},
	}
	frontend := FrontendSource("")

	stale := FindStaleBaselineEntries(capabilities, frontend)

	if len(stale) != 0 {
		t.Fatalf("want a baselined route still missing its surface left alone, got %+v", stale)
	}
}

// TestStaleBaselineEntryMessageNamesTheCapabilitysOwnBaselineHint mirrors
// TestMissingMessageNamesTheCapabilitysOwnDeclarationHint for the stale-entry
// message, so a failure names the removal fix rather than a generic pointer.
func TestStaleBaselineEntryMessageNamesTheCapabilitysOwnBaselineHint(t *testing.T) {
	entry := StaleBaselineEntry{Capability: Capability{
		Name:         "GET /v1/roles",
		Source:       "API route",
		BaselineHint: `erun-backend-api/internal/routes/route_audit.go's KnownUnsurfacedRoutes map (remove "GET /v1/roles")`,
	}}

	msg := entry.Message()
	if !strings.Contains(msg, "KnownUnsurfacedRoutes") {
		t.Fatalf("want Message() to name the capability's own BaselineHint, got %q", msg)
	}
}

// TestFindMissingDesktopSurfaceClearsARouteReferencedOnlyByItsWailsBinding
// locks the mechanism that recognizes Wails-mediated routes: a route whose
// literal path never appears in TypeScript because erun-ui/frontend/src
// calls the Wails-bound Go method by name instead is still found, through
// WailsBinding, as long as that method name is what actually appears.
func TestFindMissingDesktopSurfaceClearsARouteReferencedOnlyByItsWailsBinding(t *testing.T) {
	capabilities := []Capability{
		{
			Name:         "POST /v1/reviews/{review_id}/reviewers",
			Source:       "API route",
			Pattern:      APIRoutePattern("/v1/reviews/{review_id}/reviewers"),
			WailsBinding: "AddReviewer",
		},
	}
	// No literal "/v1/reviews/.../reviewers" path anywhere -- only the Wails
	// method name the frontend actually calls.
	frontend := FrontendSource("useAddReviewerMutation(AddReviewer)")

	missing := FindMissingDesktopSurface(capabilities, frontend)

	if len(missing) != 0 {
		t.Fatalf("want the route cleared by its WailsBinding match, got %+v", missing)
	}
}

// TestFindMissingDesktopSurfaceStillFlagsAWailsBoundRouteWithNoRealReference
// is the negative case: a WailsBinding entry that is wrong (or a route that
// gains one without ever actually being called from the frontend) must not
// silently clear the gate. WailsBinding is only ever correct because it was
// hand-verified against real source on both ends -- the mechanism itself does
// no verification, so an absent reference must still fail exactly like today.
func TestFindMissingDesktopSurfaceStillFlagsAWailsBoundRouteWithNoRealReference(t *testing.T) {
	capabilities := []Capability{
		{
			Name:         "POST /v1/reviews/{review_id}/reviewers",
			Source:       "API route",
			Pattern:      APIRoutePattern("/v1/reviews/{review_id}/reviewers"),
			WailsBinding: "AddReviewer",
		},
	}
	frontend := FrontendSource("export function UnrelatedComponent() { return null }")

	missing := FindMissingDesktopSurface(capabilities, frontend)

	if len(missing) != 1 {
		t.Fatalf("want the route still flagged when neither Pattern nor WailsBinding actually appears, got %+v", missing)
	}
}

func TestFindUnboundAppMethodsFlagsAnUnexportedMethodWithNoOtherCaller(t *testing.T) {
	decls := []AppMethodDecl{
		{Name: "whipOrchestratorNow", Exported: false, File: "orchestrator_pacing.go", Line: 289, IdentUses: 0},
	}

	unbound := FindUnboundAppMethods(decls)

	if len(unbound) != 1 || unbound[0].Name != "whipOrchestratorNow" {
		t.Fatalf("want whipOrchestratorNow flagged, got %+v", unbound)
	}
	if msg := unbound[0].Message(); msg == "" {
		t.Fatal("Message() must not be empty; the gate must name the fix")
	}
}

func TestFindUnboundAppMethodsClearsAnUnexportedMethodWithAnotherCaller(t *testing.T) {
	decls := []AppMethodDecl{
		{Name: "resolveDeployContext", Exported: false, File: "deploy.go", Line: 42, IdentUses: 2},
	}

	unbound := FindUnboundAppMethods(decls)

	if len(unbound) != 0 {
		t.Fatalf("want a method with another caller cleared, got %+v", unbound)
	}
}

func TestFindUnboundAppMethodsClearsAnExportedMethodEvenWithNoCaller(t *testing.T) {
	decls := []AppMethodDecl{
		{Name: "Deploy", Exported: true, File: "deploy.go", Line: 10, IdentUses: 0},
	}

	unbound := FindUnboundAppMethods(decls)

	if len(unbound) != 0 {
		t.Fatalf("want an exported method cleared regardless of callers -- Wails binds it whether or not Go code also calls it, got %+v", unbound)
	}
}
