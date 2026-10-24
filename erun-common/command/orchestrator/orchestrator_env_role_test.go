package orchestrator

import (
	"fmt"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"gopkg.in/yaml.v3"
)

// TestOrchestratorEnvConfigWithoutRoleLoadsUnchanged: a link predating the role field loads as
// undeclared, not a default.
func TestOrchestratorEnvConfigWithoutRoleLoadsUnchanged(t *testing.T) {
	const doc = `orchestrators:
  - id: orch-1
    name: Orchestrator One
    environments:
      - tenant: team
        environment: dev
        directory: /repo
`
	var config configpkg.ERunConfig
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
		role configpkg.OrchestratorEnvRole
		want bool
	}{
		{role: "", want: true},
		{role: configpkg.OrchestratorEnvRoleCode, want: true},
		{role: configpkg.OrchestratorEnvRoleBuild, want: true},
		{role: configpkg.OrchestratorEnvRoleRuntime, want: true},
		{role: "review", want: false},
		{role: "Code", want: false},
	}
	for _, tt := range tests {
		if got := tt.role.IsValid(); got != tt.want {
			t.Errorf("OrchestratorEnvRole(%q).IsValid() = %v, want %v", tt.role, got, tt.want)
		}
	}
}

func TestOrchestratorEnvRoleAllowed(t *testing.T) {
	// Host is excluded: it allows every role but runtime.
	anyRoleTypes := []configpkg.EnvironmentType{configpkg.EnvironmentTypeLocalAgent, configpkg.EnvironmentTypeRemoteAgent}
	anyRole := []configpkg.OrchestratorEnvRole{"", configpkg.OrchestratorEnvRoleCode, configpkg.OrchestratorEnvRoleBuild, configpkg.OrchestratorEnvRoleRuntime}
	for _, envType := range anyRoleTypes {
		for _, role := range anyRole {
			if !configpkg.OrchestratorEnvRoleAllowed(envType, role) {
				t.Errorf("OrchestratorEnvRoleAllowed(%q, %q) = false, want true", envType, role)
			}
		}
	}

	// Host has no pod, so the runtime role (deploy, pin, observe) is refused.
	hostCases := []struct {
		role configpkg.OrchestratorEnvRole
		want bool
	}{
		{role: "", want: true},
		{role: configpkg.OrchestratorEnvRoleCode, want: true},
		{role: configpkg.OrchestratorEnvRoleBuild, want: true},
		{role: configpkg.OrchestratorEnvRoleRuntime, want: false},
	}
	for _, tt := range hostCases {
		if got := configpkg.OrchestratorEnvRoleAllowed(configpkg.EnvironmentTypeHost, tt.role); got != tt.want {
			t.Errorf("OrchestratorEnvRoleAllowed(host, %q) = %v, want %v", tt.role, got, tt.want)
		}
	}

	runtimeCases := []struct {
		role configpkg.OrchestratorEnvRole
		want bool
	}{
		{role: configpkg.OrchestratorEnvRoleRuntime, want: true},
		{role: configpkg.OrchestratorEnvRoleCode, want: false},
		{role: configpkg.OrchestratorEnvRoleBuild, want: false},
		{role: "", want: false},
	}
	for _, tt := range runtimeCases {
		if got := configpkg.OrchestratorEnvRoleAllowed(configpkg.EnvironmentTypeRuntime, tt.role); got != tt.want {
			t.Errorf("OrchestratorEnvRoleAllowed(runtime, %q) = %v, want %v", tt.role, got, tt.want)
		}
	}

	if configpkg.OrchestratorEnvRoleAllowed("", configpkg.OrchestratorEnvRoleRuntime) {
		t.Fatalf("OrchestratorEnvRoleAllowed(unrecognized type, runtime) = true, want false")
	}
}

// TestOrchestratorEnvRoleRequiredFor: for host, "" means only that undeclared is legal; it still
// refuses runtime.
func TestOrchestratorEnvRoleRequiredFor(t *testing.T) {
	if got := configpkg.OrchestratorEnvRoleRequiredFor(configpkg.EnvironmentTypeRuntime); got != configpkg.OrchestratorEnvRoleRuntime {
		t.Errorf("OrchestratorEnvRoleRequiredFor(runtime) = %q, want %q", got, configpkg.OrchestratorEnvRoleRuntime)
	}
	for _, envType := range []configpkg.EnvironmentType{configpkg.EnvironmentTypeLocalAgent, configpkg.EnvironmentTypeRemoteAgent, configpkg.EnvironmentTypeHost, ""} {
		if got := configpkg.OrchestratorEnvRoleRequiredFor(envType); got != "" {
			t.Errorf("OrchestratorEnvRoleRequiredFor(%q) = %q, want \"\"", envType, got)
		}
	}
}

func TestOrchestratorEnvRoleIneligibilityReason(t *testing.T) {
	if got := configpkg.OrchestratorEnvRoleIneligibilityReason(configpkg.EnvironmentTypeRuntime, configpkg.OrchestratorEnvRoleRuntime); got != "" {
		t.Errorf("OrchestratorEnvRoleIneligibilityReason(runtime, runtime) = %q, want \"\"", got)
	}
	for _, role := range []configpkg.OrchestratorEnvRole{configpkg.OrchestratorEnvRoleCode, configpkg.OrchestratorEnvRoleBuild, ""} {
		got := configpkg.OrchestratorEnvRoleIneligibilityReason(configpkg.EnvironmentTypeRuntime, role)
		if !strings.Contains(got, "no worktree") || !strings.Contains(got, "runtime role") {
			t.Errorf("OrchestratorEnvRoleIneligibilityReason(runtime, %q) = %q, want it to name the gap and the runtime role", role, got)
		}
	}

	if got := configpkg.OrchestratorEnvRoleIneligibilityReason("", ""); !strings.Contains(got, "isn't recognized") {
		t.Errorf("OrchestratorEnvRoleIneligibilityReason(unrecognized, \"\") = %q, want an unrecognized-type reason", got)
	}
}

// TestOrchestratorEnvRoleIneligibilityReasonForAHostEnvironment: the host reason must name the
// missing pod and what still works, not "no worktree".
func TestOrchestratorEnvRoleIneligibilityReasonForAHostEnvironment(t *testing.T) {
	for _, role := range []configpkg.OrchestratorEnvRole{configpkg.OrchestratorEnvRoleCode, configpkg.OrchestratorEnvRoleBuild, ""} {
		if got := configpkg.OrchestratorEnvRoleIneligibilityReason(configpkg.EnvironmentTypeHost, role); got != "" {
			t.Errorf("OrchestratorEnvRoleIneligibilityReason(host, %q) = %q, want \"\"", role, got)
		}
	}
	hostRuntime := configpkg.OrchestratorEnvRoleIneligibilityReason(configpkg.EnvironmentTypeHost, configpkg.OrchestratorEnvRoleRuntime)
	if !strings.Contains(hostRuntime, "no pod") {
		t.Errorf("OrchestratorEnvRoleIneligibilityReason(host, runtime) = %q, want it to name the missing pod", hostRuntime)
	}
	if !strings.Contains(hostRuntime, "code") || !strings.Contains(hostRuntime, "build") {
		t.Errorf("OrchestratorEnvRoleIneligibilityReason(host, runtime) = %q, want it to name the roles that do work", hostRuntime)
	}
	if strings.Contains(hostRuntime, "no worktree") {
		t.Errorf("OrchestratorEnvRoleIneligibilityReason(host, runtime) = %q, must not claim a host env has no worktree -- it has a real directory on this machine", hostRuntime)
	}
}

func TestSetOrchestratorEnvRoleRefusesRuntimeForAHostEnvironment(t *testing.T) {
	hostDir := "/home/erun/host-work"
	store := newOrchestratorRoleStubStore(t, configpkg.EnvConfig{
		Name:          "laptop",
		Type:          configpkg.EnvironmentTypeHost,
		LocalRepoPath: hostDir,
	})

	_, err := SetOrchestratorEnvRole(core.Context{}, store, SetOrchestratorEnvRoleParams{
		OrchestratorID: "orch-1",
		Tenant:         "frs",
		Environment:    "laptop",
		Role:           configpkg.OrchestratorEnvRoleRuntime,
	})
	if err == nil {
		t.Fatal("SetOrchestratorEnvRole(host, runtime) succeeded, want a refusal")
	}
	for _, want := range []string{"frs/laptop", "host", "runtime", "no pod"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not mention %q", err, want)
		}
	}
	if store.saved {
		t.Error("a refused role write must not reach the config")
	}

	// Otherwise the refusal would be indistinguishable from refusing host links outright.
	for _, role := range []configpkg.OrchestratorEnvRole{"", configpkg.OrchestratorEnvRoleCode, configpkg.OrchestratorEnvRoleBuild} {
		if _, err := SetOrchestratorEnvRole(core.Context{}, store, SetOrchestratorEnvRoleParams{
			OrchestratorID: "orch-1",
			Tenant:         "frs",
			Environment:    "laptop",
			Role:           role,
		}); err != nil {
			t.Errorf("SetOrchestratorEnvRole(host, %q) failed: %v", role, err)
		}
	}
}

// saved records whether a write reached the config.
type orchestratorRoleStubStore struct {
	config configpkg.ERunConfig
	env    configpkg.EnvConfig
	saved  bool
}

func newOrchestratorRoleStubStore(t *testing.T, env configpkg.EnvConfig) *orchestratorRoleStubStore {
	t.Helper()
	return &orchestratorRoleStubStore{
		config: configpkg.ERunConfig{Orchestrators: []configpkg.OrchestratorConfig{{
			ID:           "orch-1",
			Name:         "Orchestrator One",
			Environments: []configpkg.OrchestratorEnvConfig{{Tenant: "frs", Environment: "laptop", Directory: "/repo"}},
		}}},
		env: env,
	}
}

func (s *orchestratorRoleStubStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.config, "", nil
}

func (s *orchestratorRoleStubStore) SaveERunConfig(config configpkg.ERunConfig) error {
	s.config = config
	s.saved = true
	return nil
}

func (s *orchestratorRoleStubStore) LoadEnvConfig(tenant, environment string) (configpkg.EnvConfig, string, error) {
	if tenant != "frs" || environment != "laptop" {
		return configpkg.EnvConfig{}, "", fmt.Errorf("no environment %s/%s", tenant, environment)
	}
	return s.env, "", nil
}
