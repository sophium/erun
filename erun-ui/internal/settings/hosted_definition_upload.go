package settings

import (
	"context"
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/platform"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// HostedDefinitionUpload carries LocalChange so the panel updates without a
// second read.
type HostedDefinitionUpload struct {
	Revision    int                         `json:"revision"`
	Describe    string                      `json:"describe"`
	LocalChange HostedDefinitionLocalChange `json:"localChange"`
}

// UploadHostedDefinition sends this environment's portable settings to the
// platform row its marker names; it is the manual recovery for auto-upload.
func (s *Service) UploadHostedDefinition(tenant, environment string) (HostedDefinitionUpload, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return HostedDefinitionUpload{}, fmt.Errorf(
			"no environment selected to upload: open an environment's Manage dialog and use its hosted panel, or run `erun platform env push TENANT ENVIRONMENT` from a terminal")
	}
	ctx, cancel := context.WithTimeout(s.d.Context(), s.d.PlatformRequestTimeout)
	defer cancel()
	return s.uploadHostedDefinition(ctx, tenant, environment)
}

// uploadHostedDefinition marks its write with the origin filter so the watcher
// does not re-upload it. Every path without a write clears the mark, or it
// would swallow the next outside change.
func (s *Service) uploadHostedDefinition(ctx context.Context, tenant, environment string) (HostedDefinitionUpload, error) {
	s.definitionWriteOrigin.mark(tenant, environment)
	result, err := s.runHostedDefinitionUpload(ctx, tenant, environment)
	if err != nil {
		s.definitionWriteOrigin.clear(tenant, environment)
		return HostedDefinitionUpload{}, err
	}
	return result, nil
}

func (s *Service) runHostedDefinitionUpload(ctx context.Context, tenant, environment string) (HostedDefinitionUpload, error) {
	client, requestCtx, cancel, err := s.d.PlatformClient(ctx, tenant)
	if err != nil {
		return HostedDefinitionUpload{}, err
	}
	defer cancel()

	upload, err := platform.PushEnvironmentDefinition(requestCtx, s.d.Store, client, model.EnvDefinitionPushParams{
		Tenant:      tenant,
		Environment: environment,
	})
	if err != nil {
		return HostedDefinitionUpload{}, err
	}
	// The origin filter gates the upload, not reloads; this event refreshes the
	// panel's marker row.
	s.emitHostedDefinitionUploaded(tenant, environment, upload.Revision)

	return HostedDefinitionUpload{
		Revision: upload.Revision,
		Describe: fmt.Sprintf("uploaded %s/%s to %s as definition revision %d", tenant, environment, upload.Environment.EnvironmentID, upload.Revision),
		// Read back: in step only if the transfer's stamp actually landed.
		LocalChange: s.reloadedHostedLocalChange(tenant, environment),
	}, nil
}

func (s *Service) reloadedHostedLocalChange(tenant, environment string) HostedDefinitionLocalChange {
	config, _, err := s.d.Store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return HostedDefinitionLocalChange{}
	}
	return hostedDefinitionLocalChangeToUI(configpkg.HostedDefinitionLocalChangeFor(config))
}

// autoUploadHostedDefinition uploads settings that moved since the last
// transfer. It ignores the origin filter: only the watcher's reaction may.
func (s *Service) autoUploadHostedDefinition(tenant, environment string) {
	config, _, err := s.d.Store.LoadEnvConfig(tenant, environment)
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
	if !s.d.PlatformReady(tenant) {
		return
	}

	ctx, cancel := context.WithTimeout(s.d.Context(), s.d.PlatformRequestTimeout)
	defer cancel()
	if _, err := s.uploadHostedDefinition(ctx, tenant, environment); err != nil {
		s.notify("warning", fmt.Sprintf(
			"Could not upload %s/%s's settings to the platform: %s. Its settings have changed here and the platform still holds the previous ones — retry from the Manage dialog's hosted panel, or run `erun platform env push %s %s`.",
			tenant, environment, err, tenant, environment,
		))
	}
}

// reactToConfigWatchTargets refreshes state for every change and uploads only
// those that are not this desktop's own write; only the upload is filtered.
func (s *Service) reactToConfigWatchTargets(targets []definitionWatchTarget) {
	// A config-file change carries no selection, so the frontend must not auto-open.
	s.d.Emit(uimodel.EnvironmentsChangedEvent, struct{}{})
	for _, target := range targets {
		if s.definitionWriteOrigin.consume(target.tenant, target.environment) {
			continue
		}
		s.autoUploadHostedDefinition(target.tenant, target.environment)
	}
}

// CatchUpHostedDefinitions uploads environments whose settings moved while the
// desktop was closed. It runs once and re-arms nothing, so it cannot loop.
func (s *Service) CatchUpHostedDefinitions() {
	tenants, err := s.d.Store.ListTenantConfigs()
	if err != nil {
		return
	}
	for _, tenant := range tenants {
		envs, err := s.d.Store.ListEnvConfigs(tenant.Name)
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
			s.autoUploadHostedDefinition(tenant.Name, config.Name)
		}
	}
}

type hostedDefinitionUploadedPayload struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Revision    int    `json:"revision"`
}

// emitHostedDefinitionUploaded refreshes the Manage dialog, which
// environments-changed does not reload.
func (s *Service) emitHostedDefinitionUploaded(tenant, environment string, revision int) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	s.d.Emit(uimodel.HostedDefinitionUploadedEvent, hostedDefinitionUploadedPayload{
		Tenant:      tenant,
		Environment: environment,
		Revision:    revision,
	})
}
