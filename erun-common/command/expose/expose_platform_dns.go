package expose

import (
	"context"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/platformclient"
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
func resolveExposeDNSUpserter(ctx core.Context, environment, erunAlias string, cloudStore config.CloudReadStore, deps cloud.CloudDependencies, hasDirectOverride bool, explicit DNSRecordUpserterFunc) (DNSRecordUpserterFunc, config.CloudProviderConfig, error) {
	if explicit != nil {
		return explicit, config.CloudProviderConfig{}, nil
	}
	if hasDirectOverride || !cloud.HasAnyErunPlatformAlias(cloudStore) {
		return upsertPowerDNSRecord, config.CloudProviderConfig{}, nil
	}
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, cloudStore, erunAlias, deps)
	if err != nil {
		return nil, config.CloudProviderConfig{}, err
	}
	return platformDNSRecordUpserter(client, environment), provider, nil
}

// resolveUnexposeDNSDeleter is resolveExposeDNSUpserter's delete-side twin.
func resolveUnexposeDNSDeleter(ctx core.Context, environment, erunAlias string, cloudStore config.CloudReadStore, deps cloud.CloudDependencies, hasDirectOverride bool, explicit DNSRecordDeleterFunc) (DNSRecordDeleterFunc, config.CloudProviderConfig, error) {
	if explicit != nil {
		return explicit, config.CloudProviderConfig{}, nil
	}
	if hasDirectOverride || !cloud.HasAnyErunPlatformAlias(cloudStore) {
		return deletePowerDNSRecord, config.CloudProviderConfig{}, nil
	}
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, cloudStore, erunAlias, deps)
	if err != nil {
		return nil, config.CloudProviderConfig{}, err
	}
	return platformDNSRecordDeleter(client, environment), provider, nil
}

// platformDNSRecordUpserter adapts PlatformClient.SetEnvironmentHostname to
// DNSRecordUpserterFunc. The environment id lookup happens here, at call
// time, rather than when the writer is constructed, so it never runs under
// --dry-run: RunExposeService only ever invokes upsertDNSRecord after its
// own dry-run check, mirroring upsertPowerDNSRecord's "live-only" contract.
func platformDNSRecordUpserter(client *platformclient.PlatformClient, environment string) DNSRecordUpserterFunc {
	return func(params DNSRecordUpsertParams) error {
		environmentID, err := cloud.ResolvePlatformEnvironmentID(context.Background(), client, environment)
		if err != nil {
			return err
		}
		_, err = client.SetEnvironmentHostname(context.Background(), environmentID, platformclient.PlatformSetEnvironmentHostnameParams{TargetIP: params.Value})
		return err
	}
}

// platformDNSRecordDeleter is platformDNSRecordUpserter's delete-side twin.
func platformDNSRecordDeleter(client *platformclient.PlatformClient, environment string) DNSRecordDeleterFunc {
	return func(DNSRecordDeleteParams) error {
		environmentID, err := cloud.ResolvePlatformEnvironmentID(context.Background(), client, environment)
		if err != nil {
			return err
		}
		return client.DeleteEnvironmentHostname(context.Background(), environmentID)
	}
}
