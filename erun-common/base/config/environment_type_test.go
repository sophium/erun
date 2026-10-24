package config

import (
	"testing"
)

func TestEnvironmentTypeIsValid(t *testing.T) {
	cases := []struct {
		envType EnvironmentType
		want    bool
	}{
		{EnvironmentTypeLocalAgent, true},
		{EnvironmentTypeRemoteAgent, true},
		{EnvironmentTypeRuntime, true},
		{EnvironmentTypeHost, true},
		{"", false},
		{"bogus", false},
	}
	for _, tc := range cases {
		if got := tc.envType.IsValid(); got != tc.want {
			t.Errorf("EnvironmentType(%q).IsValid() = %v, want %v", tc.envType, got, tc.want)
		}
	}
}

func TestEnvironmentTypeUsesDindSidecar(t *testing.T) {
	cases := []struct {
		envType EnvironmentType
		want    bool
	}{
		{EnvironmentTypeLocalAgent, true},
		{EnvironmentTypeRemoteAgent, true},
		{EnvironmentTypeRuntime, false},
		{EnvironmentTypeHost, false},
		{"", false},
		{"bogus", false},
	}
	for _, tc := range cases {
		if got := tc.envType.UsesDindSidecar(); got != tc.want {
			t.Errorf("EnvironmentType(%q).UsesDindSidecar() = %v, want %v", tc.envType, got, tc.want)
		}
	}
}

// TestEnvironmentTypePredicatesCoverEveryValidType relies on BuildsHere and
// RemoteWorktree panicking on an unhandled type.
func TestEnvironmentTypePredicatesCoverEveryValidType(t *testing.T) {
	for _, envType := range ValidEnvironmentTypes {
		t.Run(string(envType), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("predicate panicked for type %q: %v", envType, r)
				}
			}()
			env := EnvConfig{Type: envType}
			_ = env.BuildsHere()
			_ = env.RemoteWorktree()
			_ = env.HasPod()
			_ = env.ResolvedUpgradeChannel()
			_ = envType.UsesDindSidecar()
		})
	}
}

func TestHostEnvironmentPredicates(t *testing.T) {
	env := EnvConfig{Type: EnvironmentTypeHost}

	if !env.Type.IsValid() {
		t.Error("EnvironmentTypeHost must be valid")
	}
	if got := env.ResolvedType(); got != EnvironmentTypeHost {
		t.Errorf("ResolvedType() = %q, want %q", got, EnvironmentTypeHost)
	}
	if !env.BuildsHere() {
		t.Error("BuildsHere() = false, want true — a host env builds locally")
	}
	if env.RemoteWorktree() {
		t.Error("RemoteWorktree() = true, want false — a host worktree lives on this machine")
	}
	if env.HasPod() {
		t.Error("HasPod() = true, want false — a host environment has no pod")
	}
	if got := env.ResolvedUpgradeChannel(); got != UpgradeChannelSnapshot {
		t.Errorf("ResolvedUpgradeChannel() = %q, want %q", got, UpgradeChannelSnapshot)
	}
}

// TestUnresolvedEnvironmentTypeKeepsPreHostBehavior: a legacy env with an unresolved
// type must not read as host.
func TestUnresolvedEnvironmentTypeKeepsPreHostBehavior(t *testing.T) {
	env := EnvConfig{}
	if env.Type.IsValid() {
		t.Fatal("zero-value Type must not be valid")
	}
	if env.ResolvedType() != "" {
		t.Fatalf("ResolvedType() = %q, want empty for an unresolved type", env.ResolvedType())
	}
	if env.BuildsHere() {
		t.Error("BuildsHere() on an unresolved type must stay false, matching pre-host behavior")
	}
	if env.RemoteWorktree() {
		t.Error("RemoteWorktree() on an unresolved type must stay false, matching pre-host behavior")
	}
	if env.HasPod() {
		t.Error("HasPod() on an unresolved type must stay false (IsValid() gates it)")
	}
}
