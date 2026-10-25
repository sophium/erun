package settings

import (
	"context"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/envdefinition"
)

// hostedDefinitionDriftFor compares the hosted marker with the platform's
// revision; it writes nothing.
func (s *Service) hostedDefinitionDriftFor(ctx context.Context, tenant, environment string) HostedDefinitionDrift {
	config, _, err := configpkg.LoadEnvConfig(tenant, environment)
	if err != nil {
		return HostedDefinitionDrift{Error: fmt.Sprintf("Cannot read %s/%s's hosted marker: %s", tenant, environment, err)}
	}
	marker, ok := envdefinition.HostedEnvironmentFromConfig(config)
	if !ok {
		return HostedDefinitionDrift{Error: fmt.Sprintf("%s/%s is not marked as hosted, so there is nothing to compare", tenant, environment)}
	}

	client, requestCtx, cancel, err := s.d.PlatformClient(ctx, tenant)
	if err != nil {
		return HostedDefinitionDrift{
			LocalRevision: marker.DefinitionRevision,
			Describe:      envdefinition.HostedDefinitionDrift{LocalRevision: marker.DefinitionRevision}.Describe(),
			Error:         fmt.Sprintf("Cannot read %s/%s's platform definition: %s", tenant, environment, err),
		}
	}
	defer cancel()

	record, err := client.GetEnvironmentDefinition(requestCtx, marker.EnvironmentID)
	if err != nil {
		return HostedDefinitionDrift{
			LocalRevision: marker.DefinitionRevision,
			Describe:      envdefinition.HostedDefinitionDrift{LocalRevision: marker.DefinitionRevision}.Describe(),
			Error:         fmt.Sprintf("Cannot read %s/%s's platform definition: %s", tenant, environment, err),
		}
	}
	// Refuse a resolved row that is not the one the marker names.
	if err := envdefinition.CheckHostedMarkerTarget(marker, client.APIHost(), record.TenantID, record.EnvironmentID); err != nil {
		return HostedDefinitionDrift{
			LocalRevision:    marker.DefinitionRevision,
			PlatformRevision: record.Revision,
			Error:            err.Error(),
		}
	}

	drift := envdefinition.HostedDefinitionDrift{
		LocalRevision:    marker.DefinitionRevision,
		PlatformRevision: record.Revision,
	}
	return HostedDefinitionDrift{
		LocalRevision:    drift.LocalRevision,
		PlatformRevision: drift.PlatformRevision,
		Behind:           drift.IsBehind(),
		Describe:         drift.Describe(),
	}
}

// CheckHostedDefinitionDrift reports whether the platform's copy moved past
// what this machine last pulled; it never writes.
func (s *Service) CheckHostedDefinitionDrift(tenant, environment string) (HostedDefinitionDrift, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return HostedDefinitionDrift{}, fmt.Errorf(
			"no environment selected to check: open an environment's Manage dialog and use its hosted panel, or run `erun platform env pull TENANT ENVIRONMENT` from a terminal")
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.d.PlatformRequestTimeout)
	defer cancel()
	return s.hostedDefinitionDriftFor(ctx, tenant, environment), nil
}

func hostedEnvironmentToUI(config configpkg.EnvConfig) *HostedEnvironment {
	marker, ok := envdefinition.HostedEnvironmentFromConfig(config)
	if !ok {
		return nil
	}
	return &HostedEnvironment{
		Describe:           marker.Describe(),
		APIHost:            marker.APIHost,
		TenantID:           marker.TenantID,
		EnvironmentID:      marker.EnvironmentID,
		DefinitionRevision: marker.DefinitionRevision,
		LocalChange:        hostedDefinitionLocalChangeToUI(envdefinition.HostedDefinitionLocalChangeFor(config)),
	}
}

// The sentence is carried, not reworded, so the panel and CLI agree.
func hostedDefinitionLocalChangeToUI(change envdefinition.HostedDefinitionLocalChange) HostedDefinitionLocalChange {
	return HostedDefinitionLocalChange{
		Available: change.Available,
		Changed:   change.Changed,
		Describe:  change.Describe(),
	}
}
