package eruncommon

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestOrchestratorEnvConfigRoleRoundTrip locks the contract issue erun#1688
// depends on: a declared role survives config load/save, and an undeclared
// role neither writes a `role:` key nor round-trips into a default.
func TestOrchestratorEnvConfigRoleRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		role OrchestratorEnvRole
	}{
		{name: "build", role: OrchestratorEnvRoleBuild},
		{name: "code", role: OrchestratorEnvRoleCode},
		{name: "unset", role: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertOrchestratorEnvRoleRoundTrips(t, tt.role)
		})
	}
}

func assertOrchestratorEnvRoleRoundTrips(t *testing.T, role OrchestratorEnvRole) {
	t.Helper()
	config := ERunConfig{
		Orchestrators: []OrchestratorConfig{
			{
				ID:   "orch-1",
				Name: "Orchestrator One",
				Environments: []OrchestratorEnvConfig{
					{Tenant: "team", Environment: "dev", Directory: "/repo", Role: role},
				},
			},
		},
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	hasRoleKey := strings.Contains(string(data), "role:")
	if role == "" && hasRoleKey {
		t.Fatalf("unset role must not be written, got:\n%s", data)
	}
	if role != "" && !hasRoleKey {
		t.Fatalf("declared role %q must be written, got:\n%s", role, data)
	}

	var roundTripped ERunConfig
	if err := yaml.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if len(roundTripped.Orchestrators) != 1 || len(roundTripped.Orchestrators[0].Environments) != 1 {
		t.Fatalf("round trip lost structure: %+v", roundTripped)
	}
	got := roundTripped.Orchestrators[0].Environments[0].Role
	if got != role {
		t.Fatalf("round-tripped role = %q, want %q", got, role)
	}
}

// TestOrchestratorEnvConfigWithoutRoleLoadsUnchanged locks that an existing
// link with no `role:` key at all -- the shape every config predating this
// field has -- loads with an empty (undeclared) role rather than a default.
func TestOrchestratorEnvConfigWithoutRoleLoadsUnchanged(t *testing.T) {
	const doc = `orchestrators:
  - id: orch-1
    name: Orchestrator One
    environments:
      - tenant: team
        environment: dev
        directory: /repo
`
	var config ERunConfig
	if err := yaml.Unmarshal([]byte(doc), &config); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(config.Orchestrators) != 1 || len(config.Orchestrators[0].Environments) != 1 {
		t.Fatalf("unexpected structure: %+v", config)
	}
	env := config.Orchestrators[0].Environments[0]
	if env.Role != "" {
		t.Fatalf("Role = %q, want empty (undeclared)", env.Role)
	}
	if env.Tenant != "team" || env.Environment != "dev" || env.Directory != "/repo" {
		t.Fatalf("existing fields must load unchanged, got %+v", env)
	}

	// Re-saving the loaded config must not inject a role key that was never there.
	data, err := yaml.Marshal(config)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(data), "role:") {
		t.Fatalf("re-marshal must not inject a role key, got:\n%s", data)
	}
}

func TestOrchestratorEnvRoleIsValid(t *testing.T) {
	tests := []struct {
		role OrchestratorEnvRole
		want bool
	}{
		{role: "", want: true},
		{role: OrchestratorEnvRoleCode, want: true},
		{role: OrchestratorEnvRoleBuild, want: true},
		{role: OrchestratorEnvRoleRuntime, want: true},
		{role: "review", want: false},
		{role: "Code", want: false},
	}
	for _, tt := range tests {
		if got := tt.role.IsValid(); got != tt.want {
			t.Errorf("OrchestratorEnvRole(%q).IsValid() = %v, want %v", tt.role, got, tt.want)
		}
	}
}

// TestOrchestratorEnvRoleAllowed locks the shared gate erun-ui's link/edit
// path and (by design, per OrchestratorRoleStore's doc comment) not the
// CLI's set-role path consult: any role -- including undeclared -- works for
// an agent or host environment, since it already has a worktree to review
// and an in-pod agent to delegate to; a runtime environment has neither, so
// only OrchestratorEnvRoleRuntime is allowed for it; an unrecognized type
// allows nothing.
func TestOrchestratorEnvRoleAllowed(t *testing.T) {
	anyRoleTypes := []EnvironmentType{EnvironmentTypeLocalAgent, EnvironmentTypeRemoteAgent, EnvironmentTypeHost}
	anyRole := []OrchestratorEnvRole{"", OrchestratorEnvRoleCode, OrchestratorEnvRoleBuild, OrchestratorEnvRoleRuntime}
	for _, envType := range anyRoleTypes {
		for _, role := range anyRole {
			if !OrchestratorEnvRoleAllowed(envType, role) {
				t.Errorf("OrchestratorEnvRoleAllowed(%q, %q) = false, want true", envType, role)
			}
		}
	}

	runtimeCases := []struct {
		role OrchestratorEnvRole
		want bool
	}{
		{role: OrchestratorEnvRoleRuntime, want: true},
		{role: OrchestratorEnvRoleCode, want: false},
		{role: OrchestratorEnvRoleBuild, want: false},
		{role: "", want: false},
	}
	for _, tt := range runtimeCases {
		if got := OrchestratorEnvRoleAllowed(EnvironmentTypeRuntime, tt.role); got != tt.want {
			t.Errorf("OrchestratorEnvRoleAllowed(runtime, %q) = %v, want %v", tt.role, got, tt.want)
		}
	}

	if OrchestratorEnvRoleAllowed("", OrchestratorEnvRoleRuntime) {
		t.Fatalf("OrchestratorEnvRoleAllowed(unrecognized type, runtime) = true, want false")
	}
}

// TestOrchestratorEnvRoleRequiredFor locks the one-role constraint a runtime
// environment carries, and that every other type has none (any role,
// including undeclared, already works for it).
func TestOrchestratorEnvRoleRequiredFor(t *testing.T) {
	if got := OrchestratorEnvRoleRequiredFor(EnvironmentTypeRuntime); got != OrchestratorEnvRoleRuntime {
		t.Errorf("OrchestratorEnvRoleRequiredFor(runtime) = %q, want %q", got, OrchestratorEnvRoleRuntime)
	}
	for _, envType := range []EnvironmentType{EnvironmentTypeLocalAgent, EnvironmentTypeRemoteAgent, EnvironmentTypeHost, ""} {
		if got := OrchestratorEnvRoleRequiredFor(envType); got != "" {
			t.Errorf("OrchestratorEnvRoleRequiredFor(%q) = %q, want \"\"", envType, got)
		}
	}
}

// TestOrchestratorEnvRoleIneligibilityReason locks the operator-facing copy:
// empty when the role is allowed, a concrete reason naming the runtime role
// as the way out when a runtime environment is refused a different role, and
// a generic reason for a type this package does not recognize at all.
func TestOrchestratorEnvRoleIneligibilityReason(t *testing.T) {
	if got := OrchestratorEnvRoleIneligibilityReason(EnvironmentTypeRuntime, OrchestratorEnvRoleRuntime); got != "" {
		t.Errorf("OrchestratorEnvRoleIneligibilityReason(runtime, runtime) = %q, want \"\"", got)
	}
	for _, role := range []OrchestratorEnvRole{OrchestratorEnvRoleCode, OrchestratorEnvRoleBuild, ""} {
		got := OrchestratorEnvRoleIneligibilityReason(EnvironmentTypeRuntime, role)
		if !strings.Contains(got, "no worktree") || !strings.Contains(got, "runtime role") {
			t.Errorf("OrchestratorEnvRoleIneligibilityReason(runtime, %q) = %q, want it to name the gap and the runtime role", role, got)
		}
	}
	if got := OrchestratorEnvRoleIneligibilityReason("", ""); !strings.Contains(got, "isn't recognized") {
		t.Errorf("OrchestratorEnvRoleIneligibilityReason(unrecognized, \"\") = %q, want an unrecognized-type reason", got)
	}
}
