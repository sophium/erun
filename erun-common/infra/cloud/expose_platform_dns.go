package cloud

import (
	"context"
	"fmt"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// HasAnyErunPlatformAlias reports whether any erun-type cloud alias is
// configured at all, local config lookup only. It is what keeps a caller
// with zero configured aliases (the overwhelming majority, and every
// existing use of expose/unexpose before this change) on the direct pdnsutil
// path with no behavior change.
func HasAnyErunPlatformAlias(cloudStore config.CloudReadStore) bool {
	providers, err := ListCloudProviders(cloudStore)
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

// ResolvePlatformEnvironmentID finds environment's platform-assigned id by
// name among the caller's own tenant's environments (row-level security
// already scopes the list to the caller's tenant), since expose/unexpose
// only know the environment by its local tenant/name pair, while the
// hostname route is keyed by the platform's own environment id.
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
