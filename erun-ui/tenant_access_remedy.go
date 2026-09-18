package main

import (
	"context"
	"net/http"

	eruncommon "github.com/sophium/erun/erun-common"
)

// loadAccessRemedy resolves the copyable grant for a single restricted route,
// for the surfaces that record one refusal rather than a panel's set. nil
// means there is no command to hand over, which every caller renders as the
// plain sentence it already had.
func loadAccessRemedy(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, userID string, read string) *eruncommon.PlatformAccessRemedy {
	remedies := loadAccessRemedies(ctx, client, capabilities, userID, read)
	if len(remedies) == 0 {
		return nil
	}
	remedy := remedies[read]
	return &remedy
}

// canReadTenantRoles reports whether the caller may read the role list the
// remedy resolves against. An unknown capability set answers yes: a client
// that could not learn its permissions attempts the read and reports what the
// server says, rather than withholding a remedy it may in fact be able to
// build.
func canReadTenantRoles(capabilities eruncommon.PlatformCapabilities) bool {
	return !capabilities.Known() || capabilities.Allows(http.MethodGet, "/v1/roles")
}

// The remedy values are eruncommon.PlatformAccessRemedy, the one type both
// this and a denial's own sentence name the role from — a second local shape
// would be a copy that can drift from the command it describes.
//
// loadAccessRemedies resolves the copyable grant for each restricted route in
// reads. It reads the tenant's roles once, and only when at least one read was
// actually refused and the caller may read the role list at all: a caller who
// needs no remedy, and a caller refused even the role list, both pay nothing
// for a call that could only fail.
//
// Every failure degrades to no remedy rather than a wrong one. The denial
// still renders (with its existing sentence) when the roles could not be read,
// the caller's own user id is unknown, or no role covers the missing access —
// in that last case there is genuinely no command to hand over, since a role
// has to exist before anyone can be granted it.
func loadAccessRemedies(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, userID string, reads ...string) map[string]eruncommon.PlatformAccessRemedy {
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
	remedies := make(map[string]eruncommon.PlatformAccessRemedy, len(missing))
	for _, read := range missing {
		method, apiPath, ok := eruncommon.SplitCapabilityRead(read)
		if !ok {
			continue
		}
		remedy, found := eruncommon.PlatformAccessRemedyFor(userID, roles, method, apiPath)
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
