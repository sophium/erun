package main

import (
	"context"
	"fmt"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// hostedDefinitionUploadTimeout bounds the platform write. It matches the drift
// read: the same platform, the same one-round-trip shape.
const hostedDefinitionUploadTimeout = tenantDashboardTimeout

// uiHostedDefinitionUpload is what an upload did, in the terms the panel
// renders: the revision it wrote, and the local copy's divergence as of the
// upload so the panel can replace what it was showing without a second read.
type uiHostedDefinitionUpload struct {
	Revision    int                           `json:"revision"`
	Describe    string                        `json:"describe"`
	LocalChange uiHostedDefinitionLocalChange `json:"localChange"`
}

// UploadHostedDefinition sends this environment's portable settings to the
// platform row its marker names.
//
// It is the operator's own action, and it is the recovery path the
// watcher-driven upload falls back to: when this machine is not signed in to
// the tenant's platform, or the last automatic upload failed, this is what the
// panel's own button calls. The automatic path and this one are the same
// transaction — see uploadHostedDefinition.
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

// uploadHostedDefinition runs one definition upload, recording the write it is
// about to make with the origin filter so the config watcher does not read that
// write as an outside change and upload over it.
//
// The mark is cleared on every path that ends without a write. A mark left
// behind by a failed upload would swallow the next genuine outside change, and
// nothing would report it — the same silent-swallow shape the filter exists to
// prevent.
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

	upload, err := eruncommon.PushEnvironmentDefinition(requestCtx, a.deps.store, client, eruncommon.EnvDefinitionPushParams{
		Tenant:      tenant,
		Environment: environment,
	})
	if err != nil {
		return uiHostedDefinitionUpload{}, err
	}
	// The push stamped the marker, which the watcher will see as a write; the
	// event that carries it still refreshes the sidebar, because the origin
	// filter gates the upload and not the reload. This event is what refreshes
	// the panel's own marker row, which the sidebar reload does not carry.
	a.emitHostedDefinitionUploaded(tenant, environment, upload.Revision)

	return uiHostedDefinitionUpload{
		Revision: upload.Revision,
		// An upload takes everything the panel can see, so the sentence says
		// the revision rather than repeating the field list.
		Describe: fmt.Sprintf("uploaded %s/%s to %s as definition revision %d", tenant, environment, upload.Environment.EnvironmentID, upload.Revision),
		// Read back rather than assumed: the transfer's own stamp is what
		// makes the copy in step, so this reports in-step only when that
		// stamp actually landed, and reports the state plainly when something
		// changed the config underneath the upload.
		LocalChange: a.reloadedHostedLocalChange(tenant, environment),
	}, nil
}

// reloadedHostedLocalChange re-reads an environment's config and reports its
// divergence, so a caller that just wrote it reports the state on disk rather
// than the state it intended to leave.
func (a *App) reloadedHostedLocalChange(tenant, environment string) uiHostedDefinitionLocalChange {
	config, _, err := a.deps.store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return uiHostedDefinitionLocalChange{}
	}
	return hostedDefinitionLocalChangeToUI(eruncommon.HostedDefinitionLocalChangeFor(config))
}

// autoUploadHostedDefinition uploads one environment's settings if they have
// moved since the last transfer.
//
// It is the reusable decision, and it is deliberately silent about the states
// where there is nothing to do and nothing to report. It does not consult the
// origin filter: whether a change was this desktop's own write is a question
// about the watcher's reaction, not about whether the environment needs an
// upload — the launch catch-up and the panel's button ask the second question
// and are not the observer the mark was recorded for.
func (a *App) autoUploadHostedDefinition(tenant, environment string) {
	config, _, err := a.deps.store.LoadEnvConfig(tenant, environment)
	if err != nil {
		// A delete, a half-written create, or `erun init` still in progress.
		// The sidebar reload already reports the config tree's state; an
		// upload has nothing to send and no row to send it to.
		return
	}
	if _, ok := eruncommon.HostedEnvironmentFromConfig(config); !ok {
		return
	}
	if !eruncommon.HostedDefinitionLocalChangeFor(config).HasUnsentChange() {
		// The setting that changed never leaves the machine, or it is already
		// what the platform holds. The digest is what says so without asking.
		return
	}
	// Readiness is checked before anything is reported. A machine that is not
	// signed in to the tenant's platform, or that never configured one, would
	// otherwise raise a notification on every config write — and the state it
	// would name already has its own surface. The divergence stays visible in
	// the marker panel either way, because it is a local fact.
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
		return
	}
}

// reactToConfigWatchTargets is the config watcher's reaction to a debounced
// batch of config-tree changes: it refreshes the state every change implies,
// and uploads the ones that are not this desktop's own write.
//
// The origin filter is applied here rather than inside
// autoUploadHostedDefinition because this is the observer a mark is recorded
// for — the one caller that is told about a write, and so the only one that
// can be told the write was ours. Without that step a transfer's own stamp
// would be read back as an outside change and uploaded over, one upload per
// round trip, forever.
//
// The state refresh is deliberately not filtered alongside it. The origin
// filter gates the upload, never the reload: a successful transfer rewrites
// the environment's config, and the surfaces built from that config still have
// to be told.
func (a *App) reactToConfigWatchTargets(targets []definitionWatchTarget) {
	a.emitEnvironmentsChanged()
	for _, target := range targets {
		if a.definitionWriteOrigin.consume(target.tenant, target.environment) {
			continue
		}
		a.autoUploadHostedDefinition(target.tenant, target.environment)
	}
}

// catchUpHostedDefinitions uploads every hosted environment whose settings
// moved while this desktop was closed.
//
// The watcher cannot see those changes — they happened with nothing armed —
// which is the whole reason the marker carries a digest rather than relying on
// the watcher remembering. `erun init`, `erun cloud set`, and a deploy all
// write an environment's config from outside this process, so this one pass at
// launch is what keeps them from diverging silently: it converges what it can,
// and everything it cannot is left visible in the marker panel.
//
// It runs once and does not re-arm anything, so it cannot become a loop: each
// environment is visited at most once, and each upload's own write is filtered
// by the origin mark.
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
			if _, ok := eruncommon.HostedEnvironmentFromConfig(config); !ok {
				continue
			}
			if !eruncommon.HostedDefinitionLocalChangeFor(config).HasUnsentChange() {
				continue
			}
			a.autoUploadHostedDefinition(tenant.Name, config.Name)
		}
	}
}
