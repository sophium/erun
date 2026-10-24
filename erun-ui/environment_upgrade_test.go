package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

func TestResolveUpgradePlanFallsBackToDefaultRuntimeWhenTenantImageMissing(t *testing.T) {
	const fallbackSnapshot = "1.0.86-snapshot-20260606082157"
	app := NewApp(erunUIDeps{
		store: stubUIStore{
			tenants: map[string]model.TenantConfig{
				"petios": {Name: "petios"},
			},
			envs: map[string]config.EnvConfig{
				"petios/rihards-develop": {
					Name:           "rihards-develop",
					AutoUpgrade:    true,
					UpgradeChannel: config.UpgradeChannelSnapshot,
					RuntimeVersion: "1.0.85-snapshot-20260101000000",
				},
			},
		},
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (model.RuntimeRegistryVersions, error) {
			if namespace != config.DefaultContainerRegistry {
				t.Fatalf("unexpected registry namespace: %s", namespace)
			}
			switch repository {
			case "petios-devops":
				// A successful-but-empty lookup triggers the fallback; a failed one does not.
				return model.RuntimeRegistryVersions{}, nil
			case config.DefaultRuntimeImageName:
				return model.RuntimeRegistryVersions{
					Image:          namespace + "/" + repository,
					Tags:           []string{"1.0.85", fallbackSnapshot},
					LatestStable:   "1.0.85",
					LatestSnapshot: fallbackSnapshot,
				}, nil
			default:
				t.Fatalf("unexpected registry repository: %s", repository)
			}
			return model.RuntimeRegistryVersions{}, nil
		},
	})

	plan, err := app.ResolveUpgradePlan()
	if err != nil {
		t.Fatalf("ResolveUpgradePlan failed: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("expected 1 plan item, got %d: %+v", len(plan.Items), plan.Items)
	}
	item := plan.Items[0]
	if item.Channel != config.UpgradeChannelSnapshot {
		t.Fatalf("expected snapshot channel, got %q", item.Channel)
	}
	if item.Target != fallbackSnapshot {
		t.Fatalf("expected fallback snapshot target %q, got %q", fallbackSnapshot, item.Target)
	}
	if !item.Lagging {
		t.Fatalf("expected env to lag the fallback snapshot, got up to date: %+v", item)
	}
}

func TestResolveUpgradePlanOffersCandidatesWhenRegistriesDisagree(t *testing.T) {
	const tenantSnapshot = "1.0.90-snapshot-20260606090000"
	const defaultSnapshot = "1.0.86-snapshot-20260606082157"
	app := NewApp(erunUIDeps{
		store: stubUIStore{
			tenants: map[string]model.TenantConfig{
				"petios": {Name: "petios"},
			},
			envs: map[string]config.EnvConfig{
				"petios/rihards-develop": {
					Name:           "rihards-develop",
					AutoUpgrade:    true,
					UpgradeChannel: config.UpgradeChannelSnapshot,
					RuntimeVersion: "1.0.85-snapshot-20260101000000",
				},
			},
		},
		resolveImageRegistry: disagreeingSnapshotRegistry(t, tenantSnapshot, defaultSnapshot),
	})

	plan, err := app.ResolveUpgradePlan()
	if err != nil {
		t.Fatalf("ResolveUpgradePlan failed: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("expected 1 plan item, got %d", len(plan.Items))
	}
	item := plan.Items[0]
	if item.Target != "" || item.Lagging {
		t.Fatalf("expected an ambiguous (no auto-target) member awaiting a pick, got %+v", item)
	}
	if len(item.Candidates) != 2 {
		t.Fatalf("expected two candidates to pick from, got %+v", item.Candidates)
	}
	if !strings.Contains(item.UnresolvedReason, "multiple newer versions") {
		t.Fatalf("expected the multiple-newer reason, got %q", item.UnresolvedReason)
	}
	assertCandidateVersions(t, item.Candidates, tenantSnapshot, defaultSnapshot)
}

func disagreeingSnapshotRegistry(t *testing.T, tenantSnapshot, defaultSnapshot string) func(context.Context, string, string) (model.RuntimeRegistryVersions, error) {
	t.Helper()

	return func(_ context.Context, namespace, repository string) (model.RuntimeRegistryVersions, error) {
		switch repository {
		case "petios-devops":
			return model.RuntimeRegistryVersions{
				Image:          namespace + "/" + repository,
				Tags:           []string{tenantSnapshot},
				LatestSnapshot: tenantSnapshot,
			}, nil
		case config.DefaultRuntimeImageName:
			return model.RuntimeRegistryVersions{
				Image:          namespace + "/" + repository,
				LatestSnapshot: defaultSnapshot,
			}, nil
		default:
			t.Fatalf("unexpected registry repository: %s", repository)
		}
		return model.RuntimeRegistryVersions{}, nil
	}
}

func assertCandidateVersions(t *testing.T, candidates []model.UpgradeVersionCandidate, want ...string) {
	t.Helper()

	versions := map[string]bool{}
	for _, candidate := range candidates {
		versions[candidate.Version] = true
	}
	for _, w := range want {
		if !versions[w] {
			t.Fatalf("expected %q among candidates, got %+v", w, candidates)
		}
	}
}

func TestResolveUpgradePlanFallsBackToCanonicalOnFailedTenantLookup(t *testing.T) {
	const canonicalSnapshot = "1.0.86-snapshot-20260611061111"
	app := NewApp(erunUIDeps{
		store: stubUIStore{
			tenants: map[string]model.TenantConfig{
				"petios": {Name: "petios"},
			},
			envs: map[string]config.EnvConfig{
				"petios/rihards-develop": {
					Name:           "rihards-develop",
					AutoUpgrade:    true,
					UpgradeChannel: config.UpgradeChannelSnapshot,
					RuntimeVersion: "1.0.86-snapshot-20260610133238",
				},
			},
		},
		resolveImageRegistry: func(_ context.Context, _, repository string) (model.RuntimeRegistryVersions, error) {
			if repository == config.DefaultRuntimeImageName {
				return model.RuntimeRegistryVersions{
					LatestStable:   "1.0.85",
					LatestSnapshot: canonicalSnapshot,
				}, nil
			}
			return model.RuntimeRegistryVersions{}, errors.New("ghcr token request failed: 403 Forbidden")
		},
	})

	plan, err := app.ResolveUpgradePlan()
	if err != nil {
		t.Fatalf("ResolveUpgradePlan failed: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("expected 1 plan item, got %d: %+v", len(plan.Items), plan.Items)
	}
	item := plan.Items[0]
	if item.Target != canonicalSnapshot || !item.Lagging || item.UnresolvedReason != "" {
		t.Fatalf("expected a lagging member targeting the canonical snapshot, got %+v", item)
	}
}

func TestResolveUpgradePlanUnresolvedWhenCanonicalLookupAlsoFails(t *testing.T) {
	app := NewApp(erunUIDeps{
		store: stubUIStore{
			tenants: map[string]model.TenantConfig{
				"petios": {Name: "petios"},
			},
			envs: map[string]config.EnvConfig{
				"petios/rihards-develop": {
					Name:           "rihards-develop",
					AutoUpgrade:    true,
					UpgradeChannel: config.UpgradeChannelSnapshot,
					RuntimeVersion: "1.0.86-snapshot-20260610133238",
				},
			},
		},
		resolveImageRegistry: func(_ context.Context, _, repository string) (model.RuntimeRegistryVersions, error) {
			if repository == config.DefaultRuntimeImageName {
				return model.RuntimeRegistryVersions{}, errors.New("registry unreachable")
			}
			return model.RuntimeRegistryVersions{}, errors.New("ghcr token request failed: 403 Forbidden")
		},
	})

	plan, err := app.ResolveUpgradePlan()
	if err != nil {
		t.Fatalf("ResolveUpgradePlan failed: %v", err)
	}
	item := plan.Items[0]
	if item.Target != "" || item.Lagging {
		t.Fatalf("expected an unresolved, non-lagging member, got %+v", item)
	}
	if strings.TrimSpace(item.UnresolvedReason) == "" {
		t.Fatalf("expected a registry-error reason, got %q", item.UnresolvedReason)
	}
}
