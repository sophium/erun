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

// TestResolveUpgradePlanPrefersTenantImageOverDefault confirms the fallback only
// fills gaps: when the tenant image publishes the tracked channel, its version
// wins over the default ERun image's.
func TestResolveUpgradePlanPrefersTenantImageOverDefault(t *testing.T) {
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
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (eruncommon.RuntimeRegistryVersions, error) {
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
		},
	})

	plan, err := app.ResolveUpgradePlan()
	if err != nil {
		t.Fatalf("ResolveUpgradePlan failed: %v", err)
	}
	if len(plan.Items) != 1 {
		t.Fatalf("expected 1 plan item, got %d", len(plan.Items))
	}
	if got := plan.Items[0].Target; got != tenantSnapshot {
		t.Fatalf("expected tenant snapshot %q to win, got %q", tenantSnapshot, got)
	}
}

// TestResolveUpgradePlanReportsFailedLookupAsUnresolved locks the preview to
// the run's semantics (issue #497): a tenant whose registry lookup FAILS
// (the observed ghcr 403, as opposed to succeeding with no published tags)
// is never substituted with the default ERun image's versions. The member
// renders "latest unknown" with the reason instead of promising an upgrade
// the scoped run would then refuse as "target unresolved".
func TestResolveUpgradePlanReportsFailedLookupAsUnresolved(t *testing.T) {
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
				t.Fatal("a failed tenant lookup must not be papered over with the default image")
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
	if item.Target != "" || item.Lagging {
		t.Fatalf("expected an unresolved, non-lagging member, got %+v", item)
	}
	if !strings.Contains(item.UnresolvedReason, "403 Forbidden") {
		t.Fatalf("expected the lookup failure as the reason, got %q", item.UnresolvedReason)
	}
}
