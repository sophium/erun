package eruncommon

import (
	"context"
	"fmt"
)

// expose_platform_dns.go lets `erun expose`/`erun unexpose` perform the
// per-env wildcard DNS write through the platform's API instead of a direct
// `kubectl exec ... pdnsutil`, for a caller with an erun
// platform alias configured but no direct PowerDNS access to the platform's
// own cluster -- a developer's local cluster, most concretely. The Ingress
// half of expose still applies directly to the target environment's own
// cluster, which the caller already has credentials for by definition; only
// the DNS write, which lands in the platform's cluster, has this split.
//
// The decision is automatic, not an opt-in flag, so a desktop user gets it
// for free the moment they configure an erun platform alias: an explicit
// ServicesZone/PlatformNamespace override (the hosted deploy Job's own
// signal -- it always has direct PowerDNS access and never configures a
// cloud alias) always keeps the direct path; otherwise no alias configured
// keeps the direct path unchanged too (the common case today); an alias
// configured switches to the platform route. --erun-alias only exists to
// disambiguate when more than one erun-type alias is configured -- the same
// role it plays for `erun review`.

// resolveExposeDNSUpserter picks the upsert path expose's DNS write uses.
// explicit (test/caller-injected) always wins; hasDirectOverride is
// params.ServicesZone/PlatformNamespace being set. provider is the zero
// value when the direct path is used, so callers can trace "via platform
// api" only when it is non-zero.
func resolveExposeDNSUpserter(ctx Context, environment, erunAlias string, cloudStore CloudReadStore, deps CloudDependencies, hasDirectOverride bool, explicit DNSRecordUpserterFunc) (DNSRecordUpserterFunc, CloudProviderConfig, error) {
	if explicit != nil {
		return explicit, CloudProviderConfig{}, nil
	}
	if hasDirectOverride || !hasAnyErunPlatformAlias(cloudStore) {
		return upsertPowerDNSRecord, CloudProviderConfig{}, nil
	}
	client, provider, err := newPlatformClientForAlias(ctx, cloudStore, erunAlias, deps)
	if err != nil {
		return nil, CloudProviderConfig{}, err
	}
	return platformDNSRecordUpserter(client, environment), provider, nil
}

// resolveUnexposeDNSDeleter is resolveExposeDNSUpserter's delete-side twin.
func resolveUnexposeDNSDeleter(ctx Context, environment, erunAlias string, cloudStore CloudReadStore, deps CloudDependencies, hasDirectOverride bool, explicit DNSRecordDeleterFunc) (DNSRecordDeleterFunc, CloudProviderConfig, error) {
	if explicit != nil {
		return explicit, CloudProviderConfig{}, nil
	}
	if hasDirectOverride || !hasAnyErunPlatformAlias(cloudStore) {
		return deletePowerDNSRecord, CloudProviderConfig{}, nil
	}
	client, provider, err := newPlatformClientForAlias(ctx, cloudStore, erunAlias, deps)
	if err != nil {
		return nil, CloudProviderConfig{}, err
	}
	return platformDNSRecordDeleter(client, environment), provider, nil
}

// hasAnyErunPlatformAlias reports whether any erun-type cloud alias is
// configured at all, local config lookup only. It is what keeps a caller
// with zero configured aliases (the overwhelming majority, and every
// existing use of expose/unexpose before this change) on the direct pdnsutil
// path with no behavior change.
func hasAnyErunPlatformAlias(cloudStore CloudReadStore) bool {
	providers, err := ListCloudProviders(cloudStore)
	if err != nil {
		return false
	}
	for _, provider := range providers {
		if provider.Provider == CloudProviderERun {
			return true
		}
	}
	return false
}

// resolvePlatformEnvironmentID finds environment's platform-assigned id by
// name among the caller's own tenant's environments (row-level security
// already scopes the list to the caller's tenant), since expose/unexpose
// only know the environment by its local tenant/name pair, while the
// hostname route is keyed by the platform's own environment id.
func resolvePlatformEnvironmentID(ctx context.Context, client *PlatformClient, environment string) (string, error) {
	environments, err := client.ListEnvironments(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve platform environment id for %q: %w", environment, err)
	}
	for _, candidate := range environments {
		if candidate.Name == environment {
			return candidate.EnvironmentID, nil
		}
	}
	return "", fmt.Errorf("environment %q is not registered on the erun platform; register it first (see `erun platform environment register`)", environment)
}

// platformDNSRecordUpserter adapts PlatformClient.SetEnvironmentHostname to
// DNSRecordUpserterFunc. The environment id lookup happens here, at call
// time, rather than when the writer is constructed, so it never runs under
// --dry-run: RunExposeService only ever invokes upsertDNSRecord after its
// own dry-run check, mirroring upsertPowerDNSRecord's "live-only" contract.
func platformDNSRecordUpserter(client *PlatformClient, environment string) DNSRecordUpserterFunc {
	return func(params DNSRecordUpsertParams) error {
		environmentID, err := resolvePlatformEnvironmentID(context.Background(), client, environment)
		if err != nil {
			return err
		}
		_, err = client.SetEnvironmentHostname(context.Background(), environmentID, PlatformSetEnvironmentHostnameParams{TargetIP: params.Value})
		return err
	}
}

// platformDNSRecordDeleter is platformDNSRecordUpserter's delete-side twin.
func platformDNSRecordDeleter(client *PlatformClient, environment string) DNSRecordDeleterFunc {
	return func(DNSRecordDeleteParams) error {
		environmentID, err := resolvePlatformEnvironmentID(context.Background(), client, environment)
		if err != nil {
			return err
		}
		return client.DeleteEnvironmentHostname(context.Background(), environmentID)
	}
}
