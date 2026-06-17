package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestResolveUpgradePlanFallsBackToDefaultRuntimeWhenTenantImageMissing locks
// the Upgrade-all plan to the same tenant→ERun fallback the Runtime version
// picker uses (see TestLoadVersionSuggestionsFallsBackToDefaultRuntimeTagsWhenTenantImageMissing):
// when the tenant-specific image (`petios-devops`) has no resolvable versions,
// the channel target comes from the default ERun image (`erun-devops`) so the
// env still gets an upgrade target instead of resolving to "(unset)".
func TestResolveUpgradePlanFallsBackToDefaultRuntimeWhenTenantImageMissing(t *testing.T) {
	const fallbackSnapshot = "1.0.86-snapshot-20260606082157"
	app := NewApp(erunUIDeps{
		store: stubUIStore{
			tenants: map[string]eruncommon.TenantConfig{
				"petios": {Name: "petios"},
			},
			envs: map[string]eruncommon.EnvConfig{
				"petios/rihards-develop": {
					Name:           "rihards-develop",
					AutoUpgrade:    true,
					UpgradeChannel: eruncommon.UpgradeChannelSnapshot,
					RuntimeVersion: "1.0.85-snapshot-20260101000000",
				},
			},
		},
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (eruncommon.RuntimeRegistryVersions, error) {
			if namespace != eruncommon.DefaultContainerRegistry {
				t.Fatalf("unexpected registry namespace: %s", namespace)
			}
			switch repository {
			case "petios-devops":
				// Tenant image never published: the lookup succeeds but finds
				// no tags. This — and only this — falls back to the default
				// image; a FAILED lookup (e.g. ghcr 403) must not (issue
				// #497, see TestResolveUpgradePlanReportsFailedLookupAsUnresolved).
				return eruncommon.RuntimeRegistryVersions{}, nil
			case eruncommon.DefaultRuntimeImageName:
				return eruncommon.RuntimeRegistryVersions{
					Image:          namespace + "/" + repository,
					Tags:           []string{"1.0.85", fallbackSnapshot},
					LatestStable:   "1.0.85",
					LatestSnapshot: fallbackSnapshot,
				}, nil
			default:
				t.Fatalf("unexpected registry repository: %s", repository)
			}
			return eruncommon.RuntimeRegistryVersions{}, nil
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
	if item.Channel != eruncommon.UpgradeChannelSnapshot {
		t.Fatalf("expected snapshot channel, got %q", item.Channel)
	}
	if item.Target != fallbackSnapshot {
		t.Fatalf("expected fallback snapshot target %q, got %q", fallbackSnapshot, item.Target)
	}
	if !item.Lagging {
		t.Fatalf("expected env to lag the fallback snapshot, got up to date: %+v", item)
	}
}

// TestResolveUpgradePlanOffersCandidatesWhenRegistriesDisagree confirms the
// multi-registry pick (issue #527): when the env's listed registries and the
// canonical image publish different newer versions for the tracked channel, the
// env is not auto-resolved — it carries every distinct candidate so the
// operator picks one in the Upgrade-all dialog, and the run skips it until they
// do.
func TestResolveUpgradePlanOffersCandidatesWhenRegistriesDisagree(t *testing.T) {
	const tenantSnapshot = "1.0.90-snapshot-20260606090000"
	const defaultSnapshot = "1.0.86-snapshot-20260606082157"
	app := NewApp(erunUIDeps{
		store: stubUIStore{
			tenants: map[string]eruncommon.TenantConfig{
				"petios": {Name: "petios"},
			},
			envs: map[string]eruncommon.EnvConfig{
				"petios/rihards-develop": {
					Name:           "rihards-develop",
					AutoUpgrade:    true,
					UpgradeChannel: eruncommon.UpgradeChannelSnapshot,
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

// disagreeingSnapshotRegistry resolves the tenant image and the canonical ERun
// image to different latest snapshots, so the upgrade planner sees two distinct
// newer versions for the tracked channel.
func disagreeingSnapshotRegistry(t *testing.T, tenantSnapshot, defaultSnapshot string) func(context.Context, string, string) (eruncommon.RuntimeRegistryVersions, error) {
	t.Helper()

	return func(_ context.Context, namespace, repository string) (eruncommon.RuntimeRegistryVersions, error) {
		switch repository {
		case "petios-devops":
			return eruncommon.RuntimeRegistryVersions{
				Image:          namespace + "/" + repository,
				Tags:           []string{tenantSnapshot},
				LatestSnapshot: tenantSnapshot,
			}, nil
		case eruncommon.DefaultRuntimeImageName:
			return eruncommon.RuntimeRegistryVersions{
				Image:          namespace + "/" + repository,
				LatestSnapshot: defaultSnapshot,
			}, nil
		default:
			t.Fatalf("unexpected registry repository: %s", repository)
		}
		return eruncommon.RuntimeRegistryVersions{}, nil
	}
}

// assertCandidateVersions confirms each wanted version appears among the
// offered upgrade candidates.
func assertCandidateVersions(t *testing.T, candidates []eruncommon.UpgradeVersionCandidate, want ...string) {
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

// TestResolveUpgradePlanFallsBackToCanonicalOnFailedTenantLookup locks the
// corrected policy (issue #501): a tenant whose registry listing FAILS (the
// observed ghcr 403 — indistinguishable from "never published" on ghcr) gets
// its target from the canonical ERun image, because the tenant image is a
// wrapper the deploy rebuilds FROM that canonical image at the requested
// version. The env therefore upgrades instead of being parked on "latest
// unknown".
func TestResolveUpgradePlanFallsBackToCanonicalOnFailedTenantLookup(t *testing.T) {
	const canonicalSnapshot = "1.0.86-snapshot-20260611061111"
	app := NewApp(erunUIDeps{
		store: stubUIStore{
			tenants: map[string]eruncommon.TenantConfig{
				"petios": {Name: "petios"},
			},
			envs: map[string]eruncommon.EnvConfig{
				"petios/rihards-develop": {
					Name:           "rihards-develop",
					AutoUpgrade:    true,
					UpgradeChannel: eruncommon.UpgradeChannelSnapshot,
					RuntimeVersion: "1.0.86-snapshot-20260610133238",
				},
			},
		},
		resolveImageRegistry: func(_ context.Context, _, repository string) (eruncommon.RuntimeRegistryVersions, error) {
			if repository == eruncommon.DefaultRuntimeImageName {
				return eruncommon.RuntimeRegistryVersions{
					LatestStable:   "1.0.85",
					LatestSnapshot: canonicalSnapshot,
				}, nil
			}
			return eruncommon.RuntimeRegistryVersions{}, errors.New("ghcr token request failed: 403 Forbidden")
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

// TestResolveUpgradePlanUnresolvedWhenCanonicalLookupAlsoFails keeps the
// honest terminal state: when neither the tenant repo nor the canonical
// image is resolvable, the member is "latest unknown" with the registry
// failure as the reason — the dialog renders it and the run skips it.
func TestResolveUpgradePlanUnresolvedWhenCanonicalLookupAlsoFails(t *testing.T) {
	app := NewApp(erunUIDeps{
		store: stubUIStore{
			tenants: map[string]eruncommon.TenantConfig{
				"petios": {Name: "petios"},
			},
			envs: map[string]eruncommon.EnvConfig{
				"petios/rihards-develop": {
					Name:           "rihards-develop",
					AutoUpgrade:    true,
					UpgradeChannel: eruncommon.UpgradeChannelSnapshot,
					RuntimeVersion: "1.0.86-snapshot-20260610133238",
				},
			},
		},
		resolveImageRegistry: func(_ context.Context, _, repository string) (eruncommon.RuntimeRegistryVersions, error) {
			if repository == eruncommon.DefaultRuntimeImageName {
				return eruncommon.RuntimeRegistryVersions{}, errors.New("registry unreachable")
			}
			return eruncommon.RuntimeRegistryVersions{}, errors.New("ghcr token request failed: 403 Forbidden")
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
