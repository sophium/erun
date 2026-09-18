package eruncommon

import (
	"fmt"
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
// path and the CLI's set-role path both consult: any role -- including
// undeclared -- works for an agent environment, since it already has a
// worktree to review and an in-pod agent to delegate to; a host environment
// takes any role but runtime, which means deploy, pin, and observe and has no
// pod there for any of them to act on; a runtime environment has no worktree
// to review and no in-pod agent to delegate to, so only
// OrchestratorEnvRoleRuntime is allowed for it; an unrecognized type allows
// nothing.
func TestOrchestratorEnvRoleAllowed(t *testing.T) {
	// A host environment is deliberately NOT in this set: it is the one type
	// that admits a role list which is neither "everything" nor a single value,
	// so it gets its own cases below rather than being folded into "any role".
	anyRoleTypes := []EnvironmentType{EnvironmentTypeLocalAgent, EnvironmentTypeRemoteAgent}
	anyRole := []OrchestratorEnvRole{"", OrchestratorEnvRoleCode, OrchestratorEnvRoleBuild, OrchestratorEnvRoleRuntime}
	for _, envType := range anyRoleTypes {
		for _, role := range anyRole {
			if !OrchestratorEnvRoleAllowed(envType, role) {
				t.Errorf("OrchestratorEnvRoleAllowed(%q, %q) = false, want true", envType, role)
			}
		}
	}

	// A host environment is a real directory on this machine -- so code, build,
	// and undeclared are all fine -- but it has no pod, and the runtime role is
	// defined as operating the environment directly: deploy, pin, observe. Every
	// one of those refuses a host environment, so offering the role would promise
	// a relationship the link cannot deliver.
	hostCases := []struct {
		role OrchestratorEnvRole
		want bool
	}{
		{role: "", want: true},
		{role: OrchestratorEnvRoleCode, want: true},
		{role: OrchestratorEnvRoleBuild, want: true},
		{role: OrchestratorEnvRoleRuntime, want: false},
	}
	for _, tt := range hostCases {
		if got := OrchestratorEnvRoleAllowed(EnvironmentTypeHost, tt.role); got != tt.want {
			t.Errorf("OrchestratorEnvRoleAllowed(host, %q) = %v, want %v", tt.role, got, tt.want)
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
// environment carries, and that no other type requires a role. Host is in
// this list on purpose and it is the interesting one: "" here means only that
// undeclared is legal, NOT that every role is -- host still refuses runtime
// (TestOrchestratorEnvRoleAllowed above), so a caller must not read "" as
// "anything goes" and skip the allowed check.
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

// TestOrchestratorEnvRoleIneligibilityReason locks the operator-facing copy for
// every type whose eligible set is decided without a host environment: empty
// when the role is allowed, a concrete reason naming the runtime role as the
// way out when a runtime environment is refused a different role, and a generic
// reason for a type this package does not recognize at all.
//
// A host environment is refused exactly one role while allowing every other,
// which is a different rule with a different reason, so it is covered by
// TestOrchestratorEnvRoleIneligibilityReasonForAHostEnvironment below rather
// than folded in here.
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

// TestOrchestratorEnvRoleIneligibilityReasonForAHostEnvironment locks the one
// refusal a host environment has. The reason must name what is missing (a pod)
// AND what still works -- an operator told only "not allowed" would have no way
// to link the environment at all -- and must not reuse the runtime
// environment's "no worktree" wording, which is false for a host environment:
// it has a real directory on this machine.
func TestOrchestratorEnvRoleIneligibilityReasonForAHostEnvironment(t *testing.T) {
	for _, role := range []OrchestratorEnvRole{OrchestratorEnvRoleCode, OrchestratorEnvRoleBuild, ""} {
		if got := OrchestratorEnvRoleIneligibilityReason(EnvironmentTypeHost, role); got != "" {
			t.Errorf("OrchestratorEnvRoleIneligibilityReason(host, %q) = %q, want \"\"", role, got)
		}
	}
	hostRuntime := OrchestratorEnvRoleIneligibilityReason(EnvironmentTypeHost, OrchestratorEnvRoleRuntime)
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

// TestSetOrchestratorEnvRoleRefusesRuntimeForAHostEnvironment is the
// end-to-end regression for the refusal itself, driven through the same
// writer the CLI's `erun orchestrator set-role` calls. Asserted at this level
// rather than only against the gate above because the gate is a pure function
// and the defect was that a caller could reach it with a pairing it refuses:
// the CLI has to inherit the refusal without a line of its own, and this is
// what proves it does.
func TestSetOrchestratorEnvRoleRefusesRuntimeForAHostEnvironment(t *testing.T) {
	hostDir := "/home/erun/host-work"
	store := newOrchestratorRoleStubStore(t, EnvConfig{
		Name:          "laptop",
		Type:          EnvironmentTypeHost,
		LocalRepoPath: hostDir,
	})

	_, err := SetOrchestratorEnvRole(Context{}, store, SetOrchestratorEnvRoleParams{
		OrchestratorID: "orch-1",
		Tenant:         "frs",
		Environment:    "laptop",
		Role:           OrchestratorEnvRoleRuntime,
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

	// The same host environment must still accept the roles that do work, or the
	// refusal above would be indistinguishable from refusing host links outright.
	for _, role := range []OrchestratorEnvRole{"", OrchestratorEnvRoleCode, OrchestratorEnvRoleBuild} {
		if _, err := SetOrchestratorEnvRole(Context{}, store, SetOrchestratorEnvRoleParams{
			OrchestratorID: "orch-1",
			Tenant:         "frs",
			Environment:    "laptop",
			Role:           role,
		}); err != nil {
			t.Errorf("SetOrchestratorEnvRole(host, %q) failed: %v", role, err)
		}
	}
}

// orchestratorRoleStubStore is the minimal OrchestratorRoleStore the
// set-role writer needs: one orchestrator already linked to frs/laptop, plus
// whatever EnvConfig the test stages as that environment's real type. saved
// records whether a write actually reached the config, so a refused role can
// be shown to have written nothing.
type orchestratorRoleStubStore struct {
	config ERunConfig
	env    EnvConfig
	saved  bool
}

func newOrchestratorRoleStubStore(t *testing.T, env EnvConfig) *orchestratorRoleStubStore {
	t.Helper()
	return &orchestratorRoleStubStore{
		config: ERunConfig{Orchestrators: []OrchestratorConfig{{
			ID:           "orch-1",
			Name:         "Orchestrator One",
			Environments: []OrchestratorEnvConfig{{Tenant: "frs", Environment: "laptop", Directory: "/repo"}},
		}}},
		env: env,
	}
}

func (s *orchestratorRoleStubStore) LoadERunConfig() (ERunConfig, string, error) {
	return s.config, "", nil
}

func (s *orchestratorRoleStubStore) SaveERunConfig(config ERunConfig) error {
	s.config = config
	s.saved = true
	return nil
}

func (s *orchestratorRoleStubStore) LoadEnvConfig(tenant, environment string) (EnvConfig, string, error) {
	if tenant != "frs" || environment != "laptop" {
		return EnvConfig{}, "", fmt.Errorf("no environment %s/%s", tenant, environment)
	}
	return s.env, "", nil
}
