package main

import (
	"context"
	"fmt"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// hostedDefinitionDriftTimeout bounds the marker panel's own read. It matches
// tenantDashboardTimeout: the same platform, the same read-only shape.
const hostedDefinitionDriftTimeout = tenantDashboardTimeout

// hostedDefinitionDriftFor reads the environment's own hosted marker and
// compares it against the revision the platform holds. It is the read-only
// half of the drift hint: it says how far behind the local copy is and offers
// the pull, and it writes nothing.
//
// It answers a question the operator asked — the marker panel's own refresh —
// or that the desktop asks on start, never a poll that could loop back into a
// write. Auto-upload driven by the config watcher is deliberately not here;
// see erun-ui/AGENTS.md on the write→event→write loop this repository has
// already shipped once.
func (a *App) hostedDefinitionDriftFor(ctx context.Context, tenant, environment string) uiHostedDefinitionDrift {
	config, _, err := eruncommon.LoadEnvConfig(tenant, environment)
	if err != nil {
		return uiHostedDefinitionDrift{Error: fmt.Sprintf("Cannot read %s/%s's hosted marker: %s", tenant, environment, err)}
	}
	marker, ok := eruncommon.HostedEnvironmentFromConfig(config)
	if !ok {
		return uiHostedDefinitionDrift{Error: fmt.Sprintf("%s/%s is not marked as hosted, so there is nothing to compare", tenant, environment)}
	}

	client, requestCtx, cancel, err := a.tenantPlatformClient(ctx, tenant)
	if err != nil {
		return uiHostedDefinitionDrift{
			LocalRevision: marker.DefinitionRevision,
			Describe:      eruncommon.HostedDefinitionDrift{LocalRevision: marker.DefinitionRevision}.Describe(),
			Error:         fmt.Sprintf("Cannot read %s/%s's platform definition: %s", tenant, environment, err),
		}
	}
	defer cancel()

	record, err := client.GetEnvironmentDefinition(requestCtx, marker.EnvironmentID)
	if err != nil {
		return uiHostedDefinitionDrift{
			LocalRevision: marker.DefinitionRevision,
			Describe:      eruncommon.HostedDefinitionDrift{LocalRevision: marker.DefinitionRevision}.Describe(),
			Error:         fmt.Sprintf("Cannot read %s/%s's platform definition: %s", tenant, environment, err),
		}
	}
	// The row the platform resolved has to be the row the marker names, or the
	// comparison would answer about a different environment's definition — the
	// same refusal an upload and a pull make, applied to the read.
	if err := eruncommon.CheckHostedMarkerTarget(marker, client.APIHost(), record.TenantID, record.EnvironmentID); err != nil {
		return uiHostedDefinitionDrift{
			LocalRevision:    marker.DefinitionRevision,
			PlatformRevision: record.Revision,
			Error:            err.Error(),
		}
	}

	drift := eruncommon.HostedDefinitionDrift{
		LocalRevision:    marker.DefinitionRevision,
		PlatformRevision: record.Revision,
	}
	return uiHostedDefinitionDrift{
		LocalRevision:    drift.LocalRevision,
		PlatformRevision: drift.PlatformRevision,
		Behind:           drift.IsBehind(),
		Describe:         drift.Describe(),
	}
}

// CheckHostedDefinitionDrift answers the marker panel's own refresh: whether
// the platform's copy of this environment has moved past what this machine
// last pulled. It reads and reports; it never writes, and it never uploads.
func (a *App) CheckHostedDefinitionDrift(tenant, environment string) (uiHostedDefinitionDrift, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return uiHostedDefinitionDrift{}, fmt.Errorf(
			"no environment selected to check: open an environment's Manage dialog and use its hosted panel, or run `erun platform env pull TENANT ENVIRONMENT` from a terminal")
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostedDefinitionDriftTimeout)
	defer cancel()
	return a.hostedDefinitionDriftFor(ctx, tenant, environment), nil
}

// hostedEnvironmentToUI renders the marker for the environment's details
// panel, or nil when the environment carries none — the common case, which the
// frontend renders as absence rather than as an empty marker.
func hostedEnvironmentToUI(config eruncommon.EnvConfig) *uiHostedEnvironment {
	marker, ok := eruncommon.HostedEnvironmentFromConfig(config)
	if !ok {
		return nil
	}
	return &uiHostedEnvironment{
		Describe:           marker.Describe(),
		APIHost:            marker.APIHost,
		TenantID:           marker.TenantID,
		EnvironmentID:      marker.EnvironmentID,
		DefinitionRevision: marker.DefinitionRevision,
	}
}
