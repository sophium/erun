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
	// A \bwhip\b regex would miss this camelCase identifier.
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

// TestAPIRoutePatternDoesNotMatchAnUnrelatedWordSharingTheLastSegment: the frontend has unrelated
// "Roles" copy, so a bare "roles" substring must not clear the route.
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

func TestFindMissingDesktopSurfaceClearsARouteReferencedOnlyByItsWailsBinding(t *testing.T) {
	capabilities := []Capability{
		{
			Name:         "POST /v1/reviews/{review_id}/reviewers",
			Source:       "API route",
			Pattern:      APIRoutePattern("/v1/reviews/{review_id}/reviewers"),
			WailsBinding: "AddReviewer",
		},
	}
	frontend := FrontendSource("useAddReviewerMutation(AddReviewer)")

	missing := FindMissingDesktopSurface(capabilities, frontend)

	if len(missing) != 0 {
		t.Fatalf("want the route cleared by its WailsBinding match, got %+v", missing)
	}
}

// TestFindMissingDesktopSurfaceStillFlagsAWailsBoundRouteWithNoRealReference: WailsBinding does no
// verification itself, so an absent reference must still fail.
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

func TestFindMissingDesktopSurfaceClearsAFlagSpelledInCamelCase(t *testing.T) {
	capabilities := []Capability{
		{Name: "review list --waiting-on-me", Source: "CLI flag", Tokens: []string{"waiting-on-me", "waitingOnMe"}},
	}
	frontend := FrontendSource("const filter = { waitingOnMe: true }")

	missing := FindMissingDesktopSurface(capabilities, frontend)

	if len(missing) != 0 {
		t.Fatalf("want the flag cleared by its camelCase spelling, got %+v", missing)
	}
}

func TestFindMissingDesktopSurfaceFlagsAFlagWhoseCommandIsSurfacedButDimensionIsNot(t *testing.T) {
	capabilities := []Capability{
		{Name: "review_list", Source: "MCP tool", Token: "list"},
		{Name: "review list --author-user-id", Source: "CLI flag", Tokens: []string{"author-user-id", "authorUserId"}},
	}
	frontend := FrontendSource("export function ReviewList() { return reviewList() }")

	missing := FindMissingDesktopSurface(capabilities, frontend)

	if len(missing) != 1 || missing[0].Capability.Name != "review list --author-user-id" {
		t.Fatalf("want only the flag flagged while its command clears, got %+v", missing)
	}
	if msg := missing[0].Message(); !strings.Contains(msg, "authorUserId") {
		t.Fatalf("want Message() to name every spelling it searched for, got %q", msg)
	}
}

func TestFindMissingDesktopSurfaceRequiresEveryTokenToBeAbsentBeforeFlagging(t *testing.T) {
	capabilities := []Capability{
		{Name: "expose --no-tls", Source: "CLI flag", Tokens: []string{"no-tls", "noTls"}},
	}
	frontend := FrontendSource("<Checkbox name=\"no-tls\" />")

	missing := FindMissingDesktopSurface(capabilities, frontend)

	if len(missing) != 0 {
		t.Fatalf("want the flag cleared by its kebab-case spelling alone, got %+v", missing)
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

// TestContainsFlagIdentifierIgnoresIncidentalMatches: a flag's camelCase spelling is an ordinary word,
// so field reads and declarations must not count.
func TestContainsFlagIdentifierIgnoresIncidentalMatches(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		want   bool
	}{
		{"property access only", "const x = remedy.roleName", false},
		{"optional field declaration only", "interface U { roleName?: string }", false},
		{"both incidental shapes", "interface U { roleName?: string }\nconst x = remedy.roleName", false},
		{"a jsx attribute is a real binding", "<Field roleName={value} />", true},
		{"an object-literal key is a real binding", "const o = { roleName: value }", true},
		{"a positional argument is a real binding", "openAws(roleName)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := FrontendSource(tc.source).prepare().containsFlagIdentifier("roleName")
			if got != tc.want {
				t.Errorf("containsFlagIdentifier(%q) = %v, want %v", tc.source, got, tc.want)
			}
		})
	}
}
