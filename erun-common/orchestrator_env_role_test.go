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
			config := ERunConfig{
				Orchestrators: []OrchestratorConfig{
					{
						ID:   "orch-1",
						Name: "Orchestrator One",
						Environments: []OrchestratorEnvConfig{
							{Tenant: "team", Environment: "dev", Directory: "/repo", Role: tt.role},
						},
					},
				},
			}

			data, err := yaml.Marshal(config)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}

			hasRoleKey := strings.Contains(string(data), "role:")
			if tt.role == "" && hasRoleKey {
				t.Fatalf("unset role must not be written, got:\n%s", data)
			}
			if tt.role != "" && !hasRoleKey {
				t.Fatalf("declared role %q must be written, got:\n%s", tt.role, data)
			}

			var roundTripped ERunConfig
			if err := yaml.Unmarshal(data, &roundTripped); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}

			if len(roundTripped.Orchestrators) != 1 || len(roundTripped.Orchestrators[0].Environments) != 1 {
				t.Fatalf("round trip lost structure: %+v", roundTripped)
			}
			got := roundTripped.Orchestrators[0].Environments[0].Role
			if got != tt.role {
				t.Fatalf("round-tripped role = %q, want %q", got, tt.role)
			}
		})
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
		{role: "review", want: false},
		{role: "Code", want: false},
	}
	for _, tt := range tests {
		if got := tt.role.IsValid(); got != tt.want {
			t.Errorf("OrchestratorEnvRole(%q).IsValid() = %v, want %v", tt.role, got, tt.want)
		}
	}
}
