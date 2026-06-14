package eruncommon

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestUpgradeVersionsResolverForStore pins the resolution policy every
// transport shares for Upgrade all: it queries the env's listed registries
// (provenance first) plus the canonical ERun image, tags each result with its
// source registry (issue #527), and only fails when no registry resolves
// (issues #497/#501). The registry interaction is not reachable from the
// dry-run integration harness (network), so a white-box test with an injected
// lookup owns the policy.
func TestUpgradeVersionsResolverForStore(t *testing.T) {
	petiosEnv := EnvConfig{Name: "rihards-develop", RuntimeRegistry: "ghcr.io/petios"}

	t.Run("queries the provenance registry and the canonical image", func(t *testing.T) {
		var queries []string
		resolver := UpgradeVersionsResolverForStore(nil, func(_ context.Context, namespace, repository string) (RuntimeRegistryVersions, error) {
			queries = append(queries, namespace+"/"+repository)
			return RuntimeRegistryVersions{LatestStable: "1.0.85", LatestSnapshot: "1.0.86-snapshot-1"}, nil
		})
		sourced, err := resolver(Context{}, "petios", petiosEnv)
		if err != nil {
			t.Fatalf("resolver: %v", err)
		}
		want := []string{"ghcr.io/petios/petios-devops", DefaultContainerRegistry + "/" + DefaultRuntimeImageName}
		if strings.Join(queries, ",") != strings.Join(want, ",") {
			t.Fatalf("unexpected queries: got %v want %v", queries, want)
		}
		if len(sourced) != 2 || sourced[0].Registry != "ghcr.io/petios" {
			t.Fatalf("expected the provenance and canonical sources, got %+v", sourced)
		}
	})

	t.Run("a tenant lookup failure still resolves via the canonical image (#501)", func(t *testing.T) {
		// The tenant image is a wrapper the deploy rebuilds FROM the canonical
		// image, and ghcr 403s private and nonexistent repos alike — a listing
		// failure must not block the upgrade.
		resolver := UpgradeVersionsResolverForStore(nil, func(_ context.Context, _, repository string) (RuntimeRegistryVersions, error) {
			if repository == DefaultRuntimeImageName {
				return RuntimeRegistryVersions{LatestStable: "1.0.85"}, nil
			}
			return RuntimeRegistryVersions{}, errors.New("ghcr token request failed: 403 Forbidden")
		})
		sourced, err := resolver(Context{}, "petios", petiosEnv)
		if err != nil {
			t.Fatalf("a failed tenant listing must not block, got %v", err)
		}
		if len(sourced) != 1 || sourced[0].Versions.LatestStable != "1.0.85" {
			t.Fatalf("expected the canonical source to carry through, got %+v", sourced)
		}
	})

	t.Run("all lookups failing is an error with the first failure (#497)", func(t *testing.T) {
		firstErr := errors.New("ghcr token request failed: 403 Forbidden")
		resolver := UpgradeVersionsResolverForStore(nil, func(_ context.Context, _, _ string) (RuntimeRegistryVersions, error) {
			return RuntimeRegistryVersions{}, firstErr
		})
		if _, err := resolver(Context{}, "petios", petiosEnv); err == nil {
			t.Fatal("expected an error when no registry resolves")
		}
	})

	t.Run("no provenance falls back to the default registry namespace", func(t *testing.T) {
		var namespaces []string
		resolver := UpgradeVersionsResolverForStore(nil, func(_ context.Context, namespace, _ string) (RuntimeRegistryVersions, error) {
			namespaces = append(namespaces, namespace)
			return RuntimeRegistryVersions{LatestStable: "1.0.85"}, nil
		})
		if _, err := resolver(Context{}, "fresh", EnvConfig{Name: "dev"}); err != nil {
			t.Fatalf("resolver: %v", err)
		}
		if len(namespaces) != 2 || namespaces[0] != DefaultContainerRegistry {
			t.Fatalf("expected the default registry namespace, got %v", namespaces)
		}
	})

	t.Run("the seam's error form stages an unresolved env", func(t *testing.T) {
		t.Setenv(UpgradeVersionsOverrideEnv, "error=boom failure")
		resolver := UpgradeVersionsResolverForStore(nil, func(context.Context, string, string) (RuntimeRegistryVersions, error) {
			t.Fatal("the seam must short-circuit the registry lookup")
			return RuntimeRegistryVersions{}, nil
		})
		_, err := resolver(Context{}, "petios", petiosEnv)
		if err == nil || !strings.Contains(err.Error(), "boom failure") {
			t.Fatalf("expected the staged failure, got %v", err)
		}
	})
}

// TestResolveEnvUpgradeItemCandidates pins the per-env candidate logic (issue
// #527): registries agreeing on the newest version yield a single target,
// disagreeing registries yield an ambiguous item the caller must pick, and a
// version equal to current is up to date.
func TestResolveEnvUpgradeItemCandidates(t *testing.T) {
	env := EnvConfig{Name: "prod", RuntimeVersion: "1.0.0", Type: EnvironmentTypeRuntime}
	noTrace := func(string) {}

	t.Run("registries agreeing yield one target", func(t *testing.T) {
		resolver := func(_ Context, _ string, _ EnvConfig) ([]SourcedRuntimeVersions, error) {
			return []SourcedRuntimeVersions{
				{Registry: "ghcr.io/acme", Versions: RuntimeRegistryVersions{LatestStable: "2.0.0"}},
				{Registry: "registry.internal/acme", Versions: RuntimeRegistryVersions{LatestStable: "2.0.0"}},
			}, nil
		}
		item := resolveEnvUpgradeItem("team", env, "", resolver, noTrace)
		if !item.Lagging || item.Target != "2.0.0" || len(item.Candidates) != 1 {
			t.Fatalf("expected a single 2.0.0 target, got %+v", item)
		}
	})

	t.Run("registries disagreeing yield multiple candidates and no auto-target", func(t *testing.T) {
		resolver := func(_ Context, _ string, _ EnvConfig) ([]SourcedRuntimeVersions, error) {
			return []SourcedRuntimeVersions{
				{Registry: "ghcr.io/acme", Versions: RuntimeRegistryVersions{LatestStable: "2.0.0"}},
				{Registry: "registry.internal/acme", Versions: RuntimeRegistryVersions{LatestStable: "1.9.0"}},
			}, nil
		}
		item := resolveEnvUpgradeItem("team", env, "", resolver, noTrace)
		if item.Lagging || item.Target != "" || len(item.Candidates) != 2 || item.UnresolvedReason == "" {
			t.Fatalf("expected an ambiguous item with two candidates, got %+v", item)
		}
	})

	t.Run("a version equal to current is up to date", func(t *testing.T) {
		resolver := func(_ Context, _ string, _ EnvConfig) ([]SourcedRuntimeVersions, error) {
			return []SourcedRuntimeVersions{{Registry: "ghcr.io/acme", Versions: RuntimeRegistryVersions{LatestStable: "1.0.0"}}}, nil
		}
		item := resolveEnvUpgradeItem("team", env, "", resolver, noTrace)
		if item.Lagging || item.Target != "1.0.0" || item.UnresolvedReason != "" || len(item.Candidates) != 0 {
			t.Fatalf("expected an up-to-date item at the current version, got %+v", item)
		}
	})

	t.Run("an explicit override is the single target", func(t *testing.T) {
		item := resolveEnvUpgradeItem("team", env, "3.0.0", nil, noTrace)
		if !item.Lagging || item.Target != "3.0.0" || len(item.Candidates) != 1 {
			t.Fatalf("expected the override target, got %+v", item)
		}
	})
}

// TestBuildUpgradePlanPerEnv pins the per-env enumeration: only opted-in envs
// become plan items, each resolved independently.
func TestBuildUpgradePlanPerEnv(t *testing.T) {
	store := upgradeResolverStore{
		tenants: []TenantConfig{{Name: "team"}},
		envsByTenant: map[string][]EnvConfig{
			"team": {
				{Name: "prod", AutoUpgrade: true, RuntimeVersion: "1.0.0", Type: EnvironmentTypeRuntime},
				{Name: "off", AutoUpgrade: false},
			},
		},
	}
	resolver := func(_ Context, _ string, _ EnvConfig) ([]SourcedRuntimeVersions, error) {
		return []SourcedRuntimeVersions{{Registry: "ghcr.io/acme", Versions: RuntimeRegistryVersions{LatestStable: "2.0.0"}}}, nil
	}
	plan, err := BuildUpgradePlan(store, UpgradeTarget{}, resolver)
	if err != nil {
		t.Fatalf("BuildUpgradePlan: %v", err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Environment != "prod" || plan.Items[0].Target != "2.0.0" || !plan.Items[0].Lagging {
		t.Fatalf("expected one lagging prod item, got %+v", plan.Items)
	}
}

// TestRunUpgradePlanReportsUnresolvedDistinctly pins the run-side accounting
// (issue #497): a member whose target is unresolved is never counted "up to
// date" — it has its own class in the result and the deployer never sees it.
func TestRunUpgradePlanReportsUnresolvedDistinctly(t *testing.T) {
	plan := UpgradePlan{Items: []UpgradePlanItem{
		{Tenant: "team", Environment: "lagging", Channel: "stable", Current: "1.0.0", Target: "2.0.0", Lagging: true},
		{Tenant: "team", Environment: "current", Channel: "stable", Current: "2.0.0", Target: "2.0.0", Lagging: false},
		{Tenant: "petios", Environment: "unknown", Channel: "snapshot", Current: "1.0.0", Lagging: false, UnresolvedReason: "multiple newer versions across registries; pick one or pass --version"},
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

// upgradeResolverStore is the minimal DeployStore for the plan tests.
type upgradeResolverStore struct {
	tenants      []TenantConfig
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
func (s upgradeResolverStore) ListTenantConfigs() ([]TenantConfig, error) { return s.tenants, nil }
func (s upgradeResolverStore) ListEnvConfigs(tenant string) ([]EnvConfig, error) {
	return s.envsByTenant[tenant], nil
}
