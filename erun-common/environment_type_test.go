package eruncommon

import "testing"

// TestEnvironmentTypeIsValid pins the canonical set including host — the
// fourth type this test suite exists to protect (#1380).
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

// TestEnvironmentTypePredicatesCoverEveryValidType is the completeness net the
// issue asks for: it walks every type IsValid() accepts and calls each
// exclusion-shaped predicate. BuildsHere and RemoteWorktree panic on an
// unhandled type, so a fifth type added to validEnvironmentTypes without a
// matching case in both switches fails this test loudly — a compile-or-test
// failure, not a silent reclassification into whichever branch nobody
// updated.
func TestEnvironmentTypePredicatesCoverEveryValidType(t *testing.T) {
	for _, envType := range validEnvironmentTypes {
		envType := envType
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
		})
	}
}

// TestHostEnvironmentPredicates names every classifier's answer for host
// explicitly, one assertion per predicate, so a future change to any of them
// has to look this test in the eye rather than fail somewhere downstream.
func TestHostEnvironmentPredicates(t *testing.T) {
	env := EnvConfig{Type: EnvironmentTypeHost}

	if !env.Type.IsValid() {
		t.Error("EnvironmentTypeHost must be valid")
	}
	if got := env.ResolvedType(); got != EnvironmentTypeHost {
		t.Errorf("ResolvedType() = %q, want %q", got, EnvironmentTypeHost)
	}
	// Building is the point of a host env (desktop-app builds, host-credential
	// tasks) — it must answer true, not fall out of an "!= runtime" exclusion.
	if !env.BuildsHere() {
		t.Error("BuildsHere() = false, want true — a host env builds locally")
	}
	// The bug the issue names verbatim: a host worktree is the most local
	// thing in the product, so this must be false, not true from a
	// "!= local-agent" exclusion.
	if env.RemoteWorktree() {
		t.Error("RemoteWorktree() = true, want false — a host worktree lives on this machine")
	}
	// The one property that is actually unique to host: no pod at all.
	if env.HasPod() {
		t.Error("HasPod() = true, want false — a host environment has no pod")
	}
	// A host env iterates on local builds like an agent env, not like a
	// runtime env consuming stable releases.
	if got := env.ResolvedUpgradeChannel(); got != UpgradeChannelSnapshot {
		t.Errorf("ResolvedUpgradeChannel() = %q, want %q", got, UpgradeChannelSnapshot)
	}
}

// TestUnresolvedEnvironmentTypeKeepsPreHostBehavior guards the backward-
// compatibility case every host-specific check in this codebase was written
// against: a legacy env whose type never resolved (see
// legacyEnvTypeFromRemoteSnapshot) must not be reclassified as a host
// environment just because it also answers false to RemoteWorktree/BuildsHere
// questions the same way host does.
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
