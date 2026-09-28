package eruncommon

import (
	"encoding/json"
	"testing"
	"time"
)

// listLifecycleStore is the minimum ListStore a single list entry resolves
// against: one tenant with one environment, no cluster and no cloud context.
type listLifecycleStore struct {
	tenant TenantConfig
	env    EnvConfig
}

func (s listLifecycleStore) LoadERunConfig() (ERunConfig, string, error) {
	return ERunConfig{}, "", nil
}

func (s listLifecycleStore) LoadTenantConfig(string) (TenantConfig, string, error) {
	return s.tenant, "", nil
}

func (s listLifecycleStore) LoadEnvConfig(string, string) (EnvConfig, string, error) {
	return s.env, "", nil
}

func (s listLifecycleStore) ListTenantConfigs() ([]TenantConfig, error) {
	return []TenantConfig{s.tenant}, nil
}

func (s listLifecycleStore) ListEnvConfigs(string) ([]EnvConfig, error) {
	return []EnvConfig{s.env}, nil
}

func listLifecycleStates() map[EnvironmentLifecycleState]bool {
	return map[EnvironmentLifecycleState]bool{
		EnvironmentLifecycleRunning:      true,
		EnvironmentLifecycleIdle:         true,
		EnvironmentLifecycleDeployFailed: true,
		EnvironmentLifecycleStopped:      true,
		EnvironmentLifecycleUnknown:      true,
	}
}

// TestListEnvironmentEntryCarriesALifecycleState is the reproduction for the
// clause-2 gap: the environment list surface is documented as carrying
// running/idle/deploy-failed/stopped, and the per-environment entry the list
// emits carried no lifecycle field at all. It reads the emitted JSON rather
// than the Go field so the case is decidable against the wire contract a
// native client actually decodes, and so it fails on the pre-fix code by an
// absent key rather than by not compiling.
func TestListEnvironmentEntryCarriesALifecycleState(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	store := listLifecycleStore{
		tenant: TenantConfig{Name: "team"},
		env:    EnvConfig{Name: "dev", Type: EnvironmentTypeLocalAgent},
	}

	entry := listEnvironmentResult(store, store.tenant, store.env, OpenResult{}, nil, nil)
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal list entry: %v", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal list entry: %v", err)
	}
	value, ok := fields["lifecycle"]
	if !ok {
		t.Fatalf("the list entry carries no lifecycle key; got keys: %v", listEntryKeys(fields))
	}

	var state EnvironmentLifecycleState
	if err := json.Unmarshal(value, &state); err != nil {
		t.Fatalf("lifecycle is not a state string: %v", err)
	}
	if !listLifecycleStates()[state] {
		t.Fatalf("lifecycle = %q, which is not a lifecycle state a client can render", state)
	}
}

// TestListLifecycleNeverClaimsWhatItCannotObserve pins the half of the list
// surface's contract that keeps it honest. Neither a cloud-context power state
// nor a deploy diagnosis is observed on the list path, so the two states that
// need them must resolve to unknown rather than being inferred from whatever
// signals were read.
func TestListLifecycleNeverClaimsWhatItCannotObserve(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	env := EnvConfig{Name: "dev", Type: EnvironmentTypeRemoteAgent, ManagedCloud: true}
	store := listLifecycleStore{tenant: TenantConfig{Name: "team"}, env: env}

	got := ResolveListEnvironmentLifecycle(store, "team", env.Name, time.Now())
	if got == EnvironmentLifecycleStopped || got == EnvironmentLifecycleDeployFailed {
		t.Fatalf("list lifecycle = %q, which needs an observation the list path never makes", got)
	}
	if got != EnvironmentLifecycleUnknown {
		t.Fatalf("list lifecycle for a managed-cloud environment with no power state read = %q, want %q", got, EnvironmentLifecycleUnknown)
	}
}

// TestListLifecycleResolvesRunningFromStoredActivity is the positive half: the
// one signal the list path does have -- the stored idle status -- is actually
// read and used, so the field is not a constant.
//
// It also pins what that signal alone can and cannot say. "idle" is a
// stop-eligibility answer, and ResolveEnvironmentIdleStatus only grants it to a
// cloud-managed environment; on the list path a managed-cloud environment also
// has no power-state reading, so it lands on unknown rather than idle. The two
// cases below are therefore the whole reachable range of this field, and a
// future change that widens it should widen this case deliberately.
func TestListLifecycleResolvesRunningFromStoredActivity(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	env := EnvConfig{Name: "dev", Type: EnvironmentTypeLocalAgent, Idle: EnvironmentIdleConfig{Timeout: "5m"}}
	store := listLifecycleStore{tenant: TenantConfig{Name: "team"}, env: env}

	got := ResolveListEnvironmentLifecycle(store, "team", env.Name, time.Now())
	if got != EnvironmentLifecycleRunning {
		t.Fatalf("list lifecycle = %q, want %q: the idle status resolved, and no deploy diagnosis ran to observe it unhealthy", got, EnvironmentLifecycleRunning)
	}
}

// TestReadModelEnvironmentLifecycleAgreesWithItsState closes the one shape a
// list lifecycle field could introduce into the read model: ListEnvironmentResult
// is the read model's own `environment` object, so the field is emitted there
// too, and a client that renders only the summary must never see a lifecycle
// that contradicts the payload's resolved state.
func TestReadModelEnvironmentLifecycleAgreesWithItsState(t *testing.T) {
	health := &EnvironmentHealth{Deploy: DeployDiagnosisResult{HelmStatus: "STATUS: failed"}}
	model := AssembleEnvironmentReadModel("team", ListEnvironmentResult{Name: "dev", Lifecycle: EnvironmentLifecycleIdle}, nil, nil, health)

	if model.State != EnvironmentLifecycleDeployFailed {
		t.Fatalf("state = %q, want %q", model.State, EnvironmentLifecycleDeployFailed)
	}
	if model.Environment.Lifecycle != model.State {
		t.Fatalf("environment.lifecycle = %q contradicts state = %q", model.Environment.Lifecycle, model.State)
	}
}

func listEntryKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
