package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// White-box because the registry lookup needs network the dry-run harness cannot reach.
func TestUpgradeVersionsResolverForStore(t *testing.T) {
	petiosEnv := config.EnvConfig{Name: "rihards-develop", RuntimeRegistry: "ghcr.io/petios"}

	t.Run("queries the provenance registry and the canonical image", func(t *testing.T) {
		var queries []string
		resolver := UpgradeVersionsResolverForStore(nil, func(_ context.Context, namespace, repository string) (model.RuntimeRegistryVersions, error) {
			queries = append(queries, namespace+"/"+repository)
			return model.RuntimeRegistryVersions{LatestStable: "1.0.85", LatestSnapshot: "1.0.86-snapshot-1"}, nil
		})
		sourced := resolveOrFatal(t, resolver, "petios", petiosEnv, "resolver: %v")
		want := []string{"ghcr.io/petios/petios-devops", config.DefaultContainerRegistry + "/" + config.DefaultRuntimeImageName}
		if strings.Join(queries, ",") != strings.Join(want, ",") {
			t.Fatalf("unexpected queries: got %v want %v", queries, want)
		}
		assertProvenanceAndCanonicalSources(t, sourced)
	})

	t.Run("a tenant lookup failure still resolves via the canonical image (#501)", func(t *testing.T) {
		// ghcr 403s private and missing repos alike, so a tenant listing failure must not block.
		resolver := UpgradeVersionsResolverForStore(nil, func(_ context.Context, _, repository string) (model.RuntimeRegistryVersions, error) {
			if repository == config.DefaultRuntimeImageName {
				return model.RuntimeRegistryVersions{LatestStable: "1.0.85"}, nil
			}
			return model.RuntimeRegistryVersions{}, errors.New("ghcr token request failed: 403 Forbidden")
		})
		sourced := resolveOrFatal(t, resolver, "petios", petiosEnv, "a failed tenant listing must not block, got %v")
		assertCanonicalSourceCarriedThrough(t, sourced)
	})

	t.Run("all lookups failing is an error with the first failure (#497)", func(t *testing.T) {
		firstErr := errors.New("ghcr token request failed: 403 Forbidden")
		resolver := UpgradeVersionsResolverForStore(nil, func(_ context.Context, _, _ string) (model.RuntimeRegistryVersions, error) {
			return model.RuntimeRegistryVersions{}, firstErr
		})
		if _, err := resolver(core.Context{}, "petios", petiosEnv); err == nil {
			t.Fatal("expected an error when no registry resolves")
		}
	})

	t.Run("no provenance falls back to the default registry namespace", func(t *testing.T) {
		var namespaces []string
		resolver := UpgradeVersionsResolverForStore(nil, func(_ context.Context, namespace, _ string) (model.RuntimeRegistryVersions, error) {
			namespaces = append(namespaces, namespace)
			return model.RuntimeRegistryVersions{LatestStable: "1.0.85"}, nil
		})
		resolveOrFatal(t, resolver, "fresh", config.EnvConfig{Name: "dev"}, "resolver: %v")
		if len(namespaces) != 2 || namespaces[0] != config.DefaultContainerRegistry {
			t.Fatalf("expected the default registry namespace, got %v", namespaces)
		}
	})
}

func assertProvenanceAndCanonicalSources(t *testing.T, sourced []SourcedRuntimeVersions) {
	t.Helper()
	if len(sourced) != 2 || sourced[0].Registry != "ghcr.io/petios" {
		t.Fatalf("expected the provenance and canonical sources, got %+v", sourced)
	}
}

func assertCanonicalSourceCarriedThrough(t *testing.T, sourced []SourcedRuntimeVersions) {
	t.Helper()
	if len(sourced) != 1 || sourced[0].Versions.LatestStable != "1.0.85" {
		t.Fatalf("expected the canonical source to carry through, got %+v", sourced)
	}
}

func resolveOrFatal(t *testing.T, resolver func(core.Context, string, config.EnvConfig) ([]SourcedRuntimeVersions, error), tenant string, env config.EnvConfig, failMsg string) []SourcedRuntimeVersions {
	t.Helper()
	sourced, err := resolver(core.Context{}, tenant, env)
	if err != nil {
		t.Fatalf(failMsg, err)
	}
	return sourced
}

// Registry disagreement yields an ambiguous item, not an auto-target.
func TestResolveEnvUpgradeItemCandidates(t *testing.T) {
	env := config.EnvConfig{Name: "prod", RuntimeVersion: "1.0.0", Type: config.EnvironmentTypeRuntime}
	noTrace := func(string) {}

	t.Run("registries agreeing yield one target", func(t *testing.T) {
		resolver := func(_ core.Context, _ string, _ config.EnvConfig) ([]SourcedRuntimeVersions, error) {
			return []SourcedRuntimeVersions{
				{Registry: "ghcr.io/acme", Versions: model.RuntimeRegistryVersions{LatestStable: "2.0.0"}},
				{Registry: "registry.internal/acme", Versions: model.RuntimeRegistryVersions{LatestStable: "2.0.0"}},
			}, nil
		}
		item := resolveEnvUpgradeItem("team", env, "", resolver, noTrace)
		assertLaggingSingleTarget(t, item, "2.0.0", "expected a single 2.0.0 target, got %+v")
	})

	t.Run("registries disagreeing yield multiple candidates and no auto-target", func(t *testing.T) {
		resolver := func(_ core.Context, _ string, _ config.EnvConfig) ([]SourcedRuntimeVersions, error) {
			return []SourcedRuntimeVersions{
				{Registry: "ghcr.io/acme", Versions: model.RuntimeRegistryVersions{LatestStable: "2.0.0"}},
				{Registry: "registry.internal/acme", Versions: model.RuntimeRegistryVersions{LatestStable: "1.9.0"}},
			}, nil
		}
		item := resolveEnvUpgradeItem("team", env, "", resolver, noTrace)
		if item.Lagging || item.Target != "" || len(item.Candidates) != 2 || item.UnresolvedReason == "" {
			t.Fatalf("expected an ambiguous item with two candidates, got %+v", item)
		}
	})
}

func assertLaggingSingleTarget(t *testing.T, item model.UpgradePlanItem, wantTarget, failMsg string) {
	t.Helper()
	if !item.Lagging || item.Target != wantTarget || len(item.Candidates) != 1 {
		t.Fatalf(failMsg, item)
	}
}
