package config

import (
	"testing"
)

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
