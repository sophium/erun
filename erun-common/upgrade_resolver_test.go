package eruncommon

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestUpgradeVersionsResolverForStore pins the one resolution policy every
// transport uses for Upgrade all (issue #497: the desktop preview used to
// substitute the default ERun image's versions when a tenant lookup failed,
// promising upgrades the run then refused). The registry interaction is not
// reachable from the dry-run integration harness (network), so a white-box
// test with an injected lookup owns the policy; the seam-driven unresolved
// labeling is locked by the upgrade dry-run goldens.
func TestUpgradeVersionsResolverForStore(t *testing.T) {
	store := upgradeResolverStore{
		envsByTenant: map[string][]EnvConfig{
			"petios": {
				{Name: "old", RuntimeRegistry: ""},
				{Name: "rihards-develop", RuntimeRegistry: "ghcr.io/petios"},
			},
			"fresh": {{Name: "dev"}},
		},
	}

	t.Run("a tenant lookup failure falls back to the canonical image", func(t *testing.T) {
		// The tenant image is a wrapper the deploy rebuilds FROM the
		// canonical image at the requested version, and ghcr 403s private
		// and nonexistent repos alike — a listing failure must not block
		// the upgrade (issue #501).
		resolver := UpgradeVersionsResolverForStore(store, func(_ context.Context, _, repository string) (RuntimeRegistryVersions, error) {
			if repository == DefaultRuntimeImageName {
				return RuntimeRegistryVersions{LatestStable: "1.0.85", LatestSnapshot: "1.0.86-snapshot-1"}, nil
			}
			return RuntimeRegistryVersions{}, errors.New("ghcr token request failed: 403 Forbidden")
		})
		versions, err := resolver(Context{}, "petios")
		if err != nil {
			t.Fatalf("a failed tenant listing must fall back, got error %v", err)
		}
		if versions.LatestStable != "1.0.85" || versions.LatestSnapshot != "1.0.86-snapshot-1" {
			t.Fatalf("expected the canonical image's versions, got %+v", versions)
		}
	})

	t.Run("both lookups failing is unresolved with the canonical reason", func(t *testing.T) {
		canonicalErr := errors.New("registry unreachable")
		resolver := UpgradeVersionsResolverForStore(store, func(_ context.Context, _, repository string) (RuntimeRegistryVersions, error) {
			if repository == DefaultRuntimeImageName {
				return RuntimeRegistryVersions{}, canonicalErr
			}
			return RuntimeRegistryVersions{}, errors.New("ghcr token request failed: 403 Forbidden")
		})
		_, err := resolver(Context{}, "petios")
		if !errors.Is(err, canonicalErr) {
			t.Fatalf("expected the canonical failure as the reason, got %v", err)
		}
	})

	t.Run("tenant namespace comes from deploy provenance", func(t *testing.T) {
		var namespaces []string
		resolver := UpgradeVersionsResolverForStore(store, func(_ context.Context, namespace, repository string) (RuntimeRegistryVersions, error) {
			namespaces = append(namespaces, namespace+" "+repository)
			return RuntimeRegistryVersions{LatestStable: "1.0.85", LatestSnapshot: "1.0.86-snapshot-1"}, nil
		})
		if _, err := resolver(Context{}, "petios"); err != nil {
			t.Fatalf("resolver: %v", err)
		}
		if len(namespaces) != 1 || namespaces[0] != "ghcr.io/petios petios-devops" {
			t.Fatalf("expected the persisted RuntimeRegistry namespace, got %v", namespaces)
		}
	})

	t.Run("no provenance falls back to the default registry namespace", func(t *testing.T) {
		var namespaces []string
		resolver := UpgradeVersionsResolverForStore(store, func(_ context.Context, namespace, _ string) (RuntimeRegistryVersions, error) {
			namespaces = append(namespaces, namespace)
			return RuntimeRegistryVersions{LatestStable: "1.0.85", LatestSnapshot: "1.0.86-snapshot-1"}, nil
		})
		if _, err := resolver(Context{}, "fresh"); err != nil {
			t.Fatalf("resolver: %v", err)
		}
		if len(namespaces) != 1 || namespaces[0] != DefaultContainerRegistry {
			t.Fatalf("expected the default registry namespace, got %v", namespaces)
		}
	})

	t.Run("an unpublished tenant image falls back per channel to the default image", func(t *testing.T) {
		resolver := UpgradeVersionsResolverForStore(store, func(_ context.Context, _, repository string) (RuntimeRegistryVersions, error) {
			if repository == DefaultRuntimeImageName {
				return RuntimeRegistryVersions{LatestStable: "1.0.85", LatestSnapshot: "1.0.86-snapshot-1"}, nil
			}
			// The tenant repo resolves cleanly but has published nothing.
			return RuntimeRegistryVersions{}, nil
		})
		versions, err := resolver(Context{}, "petios")
		if err != nil {
			t.Fatalf("resolver: %v", err)
		}
		if versions.LatestStable != "1.0.85" || versions.LatestSnapshot != "1.0.86-snapshot-1" {
			t.Fatalf("expected the default-image fallback to fill empty channels, got %+v", versions)
		}
	})

	t.Run("a published tenant image wins over the default image", func(t *testing.T) {
		resolver := UpgradeVersionsResolverForStore(store, func(_ context.Context, _, repository string) (RuntimeRegistryVersions, error) {
			if repository == DefaultRuntimeImageName {
				t.Fatal("a fully-published tenant image must not query the default image")
			}
			return RuntimeRegistryVersions{LatestStable: "2.0.0", LatestSnapshot: "2.0.1-snapshot-1"}, nil
		})
		versions, err := resolver(Context{}, "petios")
		if err != nil {
			t.Fatalf("resolver: %v", err)
		}
		if versions.LatestStable != "2.0.0" {
			t.Fatalf("expected the tenant image's versions, got %+v", versions)
		}
	})

	t.Run("a failed default-image fallback leaves the channels unresolved without failing", func(t *testing.T) {
		resolver := UpgradeVersionsResolverForStore(store, func(_ context.Context, _, repository string) (RuntimeRegistryVersions, error) {
			if repository == DefaultRuntimeImageName {
				return RuntimeRegistryVersions{}, errors.New("registry unreachable")
			}
			return RuntimeRegistryVersions{}, nil
		})
		versions, err := resolver(Context{}, "petios")
		if err != nil {
			t.Fatalf("the tenant's own (successful, empty) lookup must not fail: %v", err)
		}
		if versions.LatestStable != "" || versions.LatestSnapshot != "" {
			t.Fatalf("expected unresolved channels, got %+v", versions)
		}
	})

	t.Run("the seam's error form stages an unresolved tenant", func(t *testing.T) {
		t.Setenv(UpgradeVersionsOverrideEnv, "error=boom failure")
		resolver := UpgradeVersionsResolverForStore(store, func(context.Context, string, string) (RuntimeRegistryVersions, error) {
			t.Fatal("the seam must short-circuit the registry lookup")
			return RuntimeRegistryVersions{}, nil
		})
		_, err := resolver(Context{}, "petios")
		if err == nil || !strings.Contains(err.Error(), "boom failure") {
			t.Fatalf("expected the staged failure, got %v", err)
		}
	})
}

// TestRunUpgradePlanReportsUnresolvedDistinctly pins the run-side accounting
// (issue #497): a member whose target is unresolved is never counted "up to
// date" — it has its own class in the result and the deployer never sees it.
func TestRunUpgradePlanReportsUnresolvedDistinctly(t *testing.T) {
	plan := UpgradePlan{Items: []UpgradePlanItem{
		{Tenant: "team", Environment: "lagging", Channel: "stable", Current: "1.0.0", Target: "2.0.0", Lagging: true},
		{Tenant: "team", Environment: "current", Channel: "stable", Current: "2.0.0", Target: "2.0.0", Lagging: false},
		{Tenant: "petios", Environment: "unknown", Channel: "snapshot", Current: "1.0.0", Lagging: false, UnresolvedReason: "ghcr token request failed: 403 Forbidden"},
	}}
	var deployed []string
	result := RunUpgradePlan(Context{}, plan, func(_ Context, item UpgradePlanItem) error {
		deployed = append(deployed, item.Tenant+"/"+item.Environment)
		return nil
	})
	if len(deployed) != 1 || deployed[0] != "team/lagging" {
		t.Fatalf("only the lagging member may deploy, got %v", deployed)
	}
	if len(result.Upgraded) != 1 || len(result.UpToDate) != 1 || len(result.Failed) != 0 {
		t.Fatalf("unexpected accounting: %+v", result)
	}
	if len(result.Unresolved) != 1 || result.Unresolved[0].Environment != "unknown" {
		t.Fatalf("the unresolved member must be reported as unresolved, got %+v", result.Unresolved)
	}
}

// upgradeResolverStore is the minimal DeployStore for the resolver tests.
type upgradeResolverStore struct {
	envsByTenant map[string][]EnvConfig
}

func (s upgradeResolverStore) LoadERunConfig() (ERunConfig, string, error) { return ERunConfig{}, "", nil }
func (s upgradeResolverStore) SaveERunConfig(ERunConfig) error             { return nil }
func (s upgradeResolverStore) LoadTenantConfig(name string) (TenantConfig, string, error) {
	return TenantConfig{Name: name}, "", nil
}
func (s upgradeResolverStore) LoadEnvConfig(tenant, environment string) (EnvConfig, string, error) {
	return EnvConfig{Name: environment}, "", nil
}
func (s upgradeResolverStore) ListTenantConfigs() ([]TenantConfig, error) { return nil, nil }
func (s upgradeResolverStore) ListEnvConfigs(tenant string) ([]EnvConfig, error) {
	return s.envsByTenant[tenant], nil
}
