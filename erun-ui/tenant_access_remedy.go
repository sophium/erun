package main

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// loadAccessRemedy returns nil when there is no grant command to offer.
func loadAccessRemedy(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, userID string, read string) *platformclient.PlatformAccessRemedy {
	remedies := loadAccessRemedies(ctx, client, capabilities, userID, read)
	if len(remedies) == 0 {
		return nil
	}
	remedy := remedies[read]
	return &remedy
}

// An unknown capability set answers yes, so the read is attempted.
func canReadTenantRoles(capabilities model.PlatformCapabilities) bool {
	return !capabilities.Known() || capabilities.Allows(http.MethodGet, "/v1/roles")
}

// loadAccessRemedies reads the role list once, only when something was refused.
// Every failure degrades to no remedy rather than a wrong one.
func loadAccessRemedies(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, userID string, reads ...string) map[string]platformclient.PlatformAccessRemedy {
	missing := make([]string, 0, len(reads))
	for _, read := range reads {
		if read != "" {
			missing = append(missing, read)
		}
	}
	if len(missing) == 0 || !canReadTenantRoles(capabilities) {
		return nil
	}
	roles, err := client.ListRoles(ctx)
	if err != nil {
		return nil
	}
	remedies := make(map[string]platformclient.PlatformAccessRemedy, len(missing))
	for _, read := range missing {
		method, apiPath, ok := platformclient.SplitCapabilityRead(read)
		if !ok {
			continue
		}
		remedy, found := platformclient.PlatformAccessRemedyFor(userID, roles, method, apiPath)
		if !found {
			continue
		}
		remedies[read] = remedy
	}
	if len(remedies) == 0 {
		return nil
	}
	return remedies
}
