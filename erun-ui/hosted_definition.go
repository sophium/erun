package main

import (
	"context"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

const hostedDefinitionDriftTimeout = tenantDashboardTimeout

// hostedDefinitionDriftFor compares the hosted marker with the platform's
// revision; it writes nothing.
func (a *App) hostedDefinitionDriftFor(ctx context.Context, tenant, environment string) uiHostedDefinitionDrift {
	config, _, err := configpkg.LoadEnvConfig(tenant, environment)
	if err != nil {
		return uiHostedDefinitionDrift{Error: fmt.Sprintf("Cannot read %s/%s's hosted marker: %s", tenant, environment, err)}
	}
	marker, ok := configpkg.HostedEnvironmentFromConfig(config)
	if !ok {
		return uiHostedDefinitionDrift{Error: fmt.Sprintf("%s/%s is not marked as hosted, so there is nothing to compare", tenant, environment)}
	}

	client, requestCtx, cancel, err := a.tenantPlatformClient(ctx, tenant)
	if err != nil {
		return uiHostedDefinitionDrift{
			LocalRevision: marker.DefinitionRevision,
			Describe:      configpkg.HostedDefinitionDrift{LocalRevision: marker.DefinitionRevision}.Describe(),
			Error:         fmt.Sprintf("Cannot read %s/%s's platform definition: %s", tenant, environment, err),
		}
	}
	defer cancel()

	record, err := client.GetEnvironmentDefinition(requestCtx, marker.EnvironmentID)
	if err != nil {
		return uiHostedDefinitionDrift{
			LocalRevision: marker.DefinitionRevision,
			Describe:      configpkg.HostedDefinitionDrift{LocalRevision: marker.DefinitionRevision}.Describe(),
			Error:         fmt.Sprintf("Cannot read %s/%s's platform definition: %s", tenant, environment, err),
		}
	}
	// Refuse a resolved row that is not the one the marker names.
	if err := configpkg.CheckHostedMarkerTarget(marker, client.APIHost(), record.TenantID, record.EnvironmentID); err != nil {
		return uiHostedDefinitionDrift{
			LocalRevision:    marker.DefinitionRevision,
			PlatformRevision: record.Revision,
			Error:            err.Error(),
		}
	}

	drift := configpkg.HostedDefinitionDrift{
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

// CheckHostedDefinitionDrift reports whether the platform's copy moved past
// what this machine last pulled; it never writes.
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

func hostedEnvironmentToUI(config configpkg.EnvConfig) *uiHostedEnvironment {
	marker, ok := configpkg.HostedEnvironmentFromConfig(config)
	if !ok {
		return nil
	}
	return &uiHostedEnvironment{
		Describe:           marker.Describe(),
		APIHost:            marker.APIHost,
		TenantID:           marker.TenantID,
		EnvironmentID:      marker.EnvironmentID,
		DefinitionRevision: marker.DefinitionRevision,
		LocalChange:        hostedDefinitionLocalChangeToUI(configpkg.HostedDefinitionLocalChangeFor(config)),
	}
}

// The sentence is carried, not reworded, so the panel and CLI agree.
func hostedDefinitionLocalChangeToUI(change configpkg.HostedDefinitionLocalChange) uiHostedDefinitionLocalChange {
	return uiHostedDefinitionLocalChange{
		Available: change.Available,
		Changed:   change.Changed,
		Describe:  change.Describe(),
	}
}
