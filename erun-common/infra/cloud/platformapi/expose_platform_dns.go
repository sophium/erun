package platformapi

import (
	"context"
	"fmt"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// HasAnyErunPlatformAlias reports whether any erun-type alias is configured; without one,
// expose/unexpose use pdnsutil directly.
func HasAnyErunPlatformAlias(cloudStore config.CloudReadStore) bool {
	providers, err := providerconfig.ListCloudProviders(cloudStore)
	if err != nil {
		return false
	}
	for _, provider := range providers {
		if provider.Provider == config.CloudProviderERun {
			return true
		}
	}
	return false
}

// ResolvePlatformEnvironmentID maps an environment name to its platform id; RLS
// scopes the list to the caller's tenant.
func ResolvePlatformEnvironmentID(ctx context.Context, client *platformclient.PlatformClient, environment string) (string, error) {
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
