package main

import (
	"context"
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
				// Tenant image not available (mirrors the 403 / unpublished image
				// case): no tags, no channel latests.
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
