package desktopsurface

import "testing"

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
