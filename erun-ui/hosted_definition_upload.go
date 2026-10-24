package main

import (
	"context"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/platform"
)

const hostedDefinitionUploadTimeout = tenantDashboardTimeout

// uiHostedDefinitionUpload carries LocalChange so the panel updates without a
// second read.
type uiHostedDefinitionUpload struct {
	Revision    int                           `json:"revision"`
	Describe    string                        `json:"describe"`
	LocalChange uiHostedDefinitionLocalChange `json:"localChange"`
}

// UploadHostedDefinition sends this environment's portable settings to the
// platform row its marker names; it is the manual recovery for auto-upload.
func (a *App) UploadHostedDefinition(tenant, environment string) (uiHostedDefinitionUpload, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return uiHostedDefinitionUpload{}, fmt.Errorf(
			"no environment selected to upload: open an environment's Manage dialog and use its hosted panel, or run `erun platform env push TENANT ENVIRONMENT` from a terminal")
	}
	ctx, cancel := context.WithTimeout(a.activityWatcherCtx(), hostedDefinitionUploadTimeout)
	defer cancel()
	return a.uploadHostedDefinition(ctx, tenant, environment)
}

// uploadHostedDefinition marks its write with the origin filter so the watcher
// does not re-upload it. Every path without a write clears the mark, or it
// would swallow the next outside change.
func (a *App) uploadHostedDefinition(ctx context.Context, tenant, environment string) (uiHostedDefinitionUpload, error) {
	a.definitionWriteOrigin.mark(tenant, environment)
	result, err := a.runHostedDefinitionUpload(ctx, tenant, environment)
	if err != nil {
		a.definitionWriteOrigin.clear(tenant, environment)
		return uiHostedDefinitionUpload{}, err
	}
	return result, nil
}

func (a *App) runHostedDefinitionUpload(ctx context.Context, tenant, environment string) (uiHostedDefinitionUpload, error) {
	client, requestCtx, cancel, err := a.tenantPlatformClient(ctx, tenant)
	if err != nil {
		return uiHostedDefinitionUpload{}, err
	}
	defer cancel()

	upload, err := platform.PushEnvironmentDefinition(requestCtx, a.deps.store, client, platform.EnvDefinitionPushParams{
		Tenant:      tenant,
		Environment: environment,
	})
	if err != nil {
		return uiHostedDefinitionUpload{}, err
	}
	// The origin filter gates the upload, not reloads; this event refreshes the
	// panel's marker row.
	a.emitHostedDefinitionUploaded(tenant, environment, upload.Revision)

	return uiHostedDefinitionUpload{
		Revision: upload.Revision,
		Describe: fmt.Sprintf("uploaded %s/%s to %s as definition revision %d", tenant, environment, upload.Environment.EnvironmentID, upload.Revision),
		// Read back: in step only if the transfer's stamp actually landed.
		LocalChange: a.reloadedHostedLocalChange(tenant, environment),
	}, nil
}

func (a *App) reloadedHostedLocalChange(tenant, environment string) uiHostedDefinitionLocalChange {
	config, _, err := a.deps.store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return uiHostedDefinitionLocalChange{}
	}
	return hostedDefinitionLocalChangeToUI(configpkg.HostedDefinitionLocalChangeFor(config))
}

// autoUploadHostedDefinition uploads settings that moved since the last
// transfer. It ignores the origin filter: only the watcher's reaction may.
func (a *App) autoUploadHostedDefinition(tenant, environment string) {
	config, _, err := a.deps.store.LoadEnvConfig(tenant, environment)
	if err != nil {
		// Deleted, half-created, or mid-init: nothing to send.
		return
	}
	if _, ok := configpkg.HostedEnvironmentFromConfig(config); !ok {
		return
	}
	if !configpkg.HostedDefinitionLocalChangeFor(config).HasUnsentChange() {
		return
	}
	// Check readiness first so an unsigned machine is not notified on every write.
	if resolution, resolveErr := a.resolveTenantPlatform(tenant, ""); resolveErr != nil || resolution.state != tenantPlatformStateReady {
		return
	}

	ctx, cancel := context.WithTimeout(a.activityWatcherCtx(), hostedDefinitionUploadTimeout)
	defer cancel()
	if _, err := a.uploadHostedDefinition(ctx, tenant, environment); err != nil {
		a.emitAppNotification("warning", fmt.Sprintf(
			"Could not upload %s/%s's settings to the platform: %s. Its settings have changed here and the platform still holds the previous ones — retry from the Manage dialog's hosted panel, or run `erun platform env push %s %s`.",
			tenant, environment, err, tenant, environment,
		))
	}
}

// reactToConfigWatchTargets refreshes state for every change and uploads only
// those that are not this desktop's own write; only the upload is filtered.
func (a *App) reactToConfigWatchTargets(targets []definitionWatchTarget) {
	a.emitEnvironmentsChanged()
	for _, target := range targets {
		if a.definitionWriteOrigin.consume(target.tenant, target.environment) {
			continue
		}
		a.autoUploadHostedDefinition(target.tenant, target.environment)
	}
}

// catchUpHostedDefinitions uploads environments whose settings moved while the
// desktop was closed. It runs once and re-arms nothing, so it cannot loop.
func (a *App) catchUpHostedDefinitions() {
	tenants, err := a.deps.store.ListTenantConfigs()
	if err != nil {
		return
	}
	for _, tenant := range tenants {
		envs, err := a.deps.store.ListEnvConfigs(tenant.Name)
		if err != nil {
			continue
		}
		for _, config := range envs {
			if _, ok := configpkg.HostedEnvironmentFromConfig(config); !ok {
				continue
			}
			if !configpkg.HostedDefinitionLocalChangeFor(config).HasUnsentChange() {
				continue
			}
			a.autoUploadHostedDefinition(tenant.Name, config.Name)
		}
	}
}
