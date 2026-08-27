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
