package expose

import (
	"context"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/platformapi"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// The DNS write goes through the platform API when an erun platform alias is
// configured and no ServicesZone/PlatformNamespace override is given; otherwise
// it execs pdnsutil directly. The Ingress always applies to the target cluster.

// resolveExposeDNSUpserter: explicit always wins; provider is zero on the direct path.
func resolveExposeDNSUpserter(ctx core.Context, environment, erunAlias string, cloudStore config.CloudReadStore, deps cloud.CloudDependencies, hasDirectOverride bool, explicit DNSRecordUpserterFunc) (DNSRecordUpserterFunc, model.CloudProviderConfig, error) {
	if explicit != nil {
		return explicit, model.CloudProviderConfig{}, nil
	}
	if hasDirectOverride || !platformapi.HasAnyErunPlatformAlias(cloudStore) {
		return upsertPowerDNSRecord, model.CloudProviderConfig{}, nil
	}
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, cloudStore, erunAlias, deps)
	if err != nil {
		return nil, model.CloudProviderConfig{}, err
	}
	return platformDNSRecordUpserter(client, environment), provider, nil
}

func resolveUnexposeDNSDeleter(ctx core.Context, environment, erunAlias string, cloudStore config.CloudReadStore, deps cloud.CloudDependencies, hasDirectOverride bool, explicit DNSRecordDeleterFunc) (DNSRecordDeleterFunc, model.CloudProviderConfig, error) {
	if explicit != nil {
		return explicit, model.CloudProviderConfig{}, nil
	}
	if hasDirectOverride || !platformapi.HasAnyErunPlatformAlias(cloudStore) {
		return deletePowerDNSRecord, model.CloudProviderConfig{}, nil
	}
	client, provider, err := platformapi.NewPlatformClientForAlias(ctx, cloudStore, erunAlias, deps)
	if err != nil {
		return nil, model.CloudProviderConfig{}, err
	}
	return platformDNSRecordDeleter(client, environment), provider, nil
}

// The environment id lookup happens at call time so it never runs under --dry-run.
func platformDNSRecordUpserter(client *platformclient.PlatformClient, environment string) DNSRecordUpserterFunc {
	return func(params DNSRecordUpsertParams) error {
		environmentID, err := platformapi.ResolvePlatformEnvironmentID(context.Background(), client, environment)
		if err != nil {
			return err
		}
		_, err = client.SetEnvironmentHostname(context.Background(), environmentID, platformclient.PlatformSetEnvironmentHostnameParams{TargetIP: params.Value})
		return err
	}
}

func platformDNSRecordDeleter(client *platformclient.PlatformClient, environment string) DNSRecordDeleterFunc {
	return func(DNSRecordDeleteParams) error {
		environmentID, err := platformapi.ResolvePlatformEnvironmentID(context.Background(), client, environment)
		if err != nil {
			return err
		}
		return client.DeleteEnvironmentHostname(context.Background(), environmentID)
	}
}
