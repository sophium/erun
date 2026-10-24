package main

import (
	"strings"
)

// finishDeployByTenantEnv finalizes the active deploy entry for the
// (tenant, environment) parsed from a `==> Deployed`/`==> Skipping`
// trace line, falling back to the session's selection only when the
// line did not name them (an older erun build, or an unexpected
// shape). Looking up by parsed tenant/env keeps finalization
// robust when the trace is observed in a tab whose selection differs
// from the deploy's target — e.g. a generic Local shell where the user
// invoked `erun open` manually and the session selection has no
// tenant/env bound at all.
func (a *App) finishDeployByTenantEnv(selection uiSelection, tenant, environment string, status activityQueueStatus, errMsg string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		a.finishActivityTracking(selection, status, errMsg)
		return
	}
	if a.activityQueue == nil {
		return
	}
	entry, ok := a.activityQueue.findActiveByCommand("deploy", tenant, environment)
	if !ok {
		return
	}
	if final, finished := a.activityQueue.finish(entry.ID, status, errMsg); finished {
		a.unlockTerminalsForActivity(final)
		if status == activityQueueStatusSucceeded {
			// A successful deploy supersedes any stale 'failed' flag on the row:
			// clearing keeps the sidebar dot and hover card truthful and lets the
			// next session exit respawn normally.
			a.emitEnvStatus(uiSelection{Tenant: tenant, Environment: environment}, "")
		}
		if status == activityQueueStatusSucceeded || status == activityQueueStatusSkipped {
			// The runtime is now reachable (deployed, or skipped because it was
			// already current). Signal the create→deploy→open gate so a
			// freshly-created env opens its tabs only after deploy lands, never
			// against a runtime that does not exist.
			a.emitEnvironmentDeployed(tenant, environment)
		}
	}
}

// startInitFromTrace registers the umbrella init entry covering the whole
// bootstrap. The `==> Deploying` line for the helm step within init still
// registers its own deploy entry, finalized independently.
func (a *App) startInitFromTrace(selection uiSelection, tenant, environment string) {
	if a.activityQueue == nil {
		return
	}
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	if _, ok := a.activityQueue.findActiveByCommand("init", tenant, environment); ok {
		return
	}
	kubeContext := a.resolveActivityKubeContext(selection, tenant, environment)
	entry, fresh := a.activityQueue.start(activityQueueEntry{
		Command:           "init",
		Tenant:            tenant,
		Environment:       environment,
		KubernetesContext: kubeContext,
		Source:            "trace",
		Summary:           "init " + tenant + "/" + environment,
	})
	if !fresh {
		return
	}
	a.rememberKubeContextForActivity(kubeContext)
	a.lockTerminalsForActivity(entry)
}

// finishInitByTenantEnv looks the entry up by parsed tenant/env so the trace,
// wherever it is observed, converges on the same init record.
func (a *App) finishInitByTenantEnv(tenant, environment string, status activityQueueStatus, errMsg string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	if a.activityQueue == nil {
		return
	}
	entry, ok := a.activityQueue.findActiveByCommand("init", tenant, environment)
	if !ok {
		return
	}
	if final, finished := a.activityQueue.finish(entry.ID, status, errMsg); finished {
		a.unlockTerminalsForActivity(final)
	}
}

// startDoctorFromTrace registers an activity entry for `erun doctor`, giving
// it a persistent, glanceable presence in the activity drawer/sidebar spinner
// regardless of which surface started it (sidebar, Manage dialog, or a failed
// deploy card's "Run doctor" recovery button). Unlike deploy/init it never
// locks terminals: doctor's recovery actions prompt interactively in the very
// terminal it runs in, and locking that terminal would block the prompt.
func (a *App) startDoctorFromTrace(selection uiSelection, tenant, environment string) {
	if a.activityQueue == nil {
		return
	}
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	if _, ok := a.activityQueue.findActiveByCommand("doctor", tenant, environment); ok {
		return
	}
	kubeContext := a.resolveActivityKubeContext(selection, tenant, environment)
	if _, fresh := a.activityQueue.start(activityQueueEntry{
		Command:           "doctor",
		Tenant:            tenant,
		Environment:       environment,
		KubernetesContext: kubeContext,
		Source:            "trace",
		Summary:           "doctor " + tenant + "/" + environment,
	}); !fresh {
		return
	}
	a.rememberKubeContextForActivity(kubeContext)
}

// finishDoctorByTenantEnv finalizes the activity entry and records the
// persisted last-run outcome the Manage dialog's SSH tab reads
// (state.doctor.lastDoctorBySelection) — the answer "is this healthy?" that
// otherwise vanished the moment the terminal scrolled past it.
func (a *App) finishDoctorByTenantEnv(tenant, environment string, status activityQueueStatus, errMsg string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	if a.activityQueue != nil {
		if entry, ok := a.activityQueue.findActiveByCommand("doctor", tenant, environment); ok {
			a.activityQueue.finish(entry.ID, status, errMsg)
		}
	}
	a.emitDoctorCompleted(tenant, environment, status == activityQueueStatusSucceeded, errMsg)
}

// startSSHDInitFromTrace registers an activity entry for `erun sshd init`,
// giving it a persistent, glanceable presence in the activity drawer/sidebar
// spinner regardless of which surface started it. Unlike doctor, sshd init
// runs no interactive recovery prompts of its own — it is a one-shot
// provisioning action like init/deploy — so it locks terminals the same way
// those do.
func (a *App) startSSHDInitFromTrace(selection uiSelection, tenant, environment string) {
	if a.activityQueue == nil {
		return
	}
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	if _, ok := a.activityQueue.findActiveByCommand("sshd-init", tenant, environment); ok {
		return
	}
	kubeContext := a.resolveActivityKubeContext(selection, tenant, environment)
	entry, fresh := a.activityQueue.start(activityQueueEntry{
		Command:           "sshd-init",
		Tenant:            tenant,
		Environment:       environment,
		KubernetesContext: kubeContext,
		Source:            "trace",
		Summary:           "sshd init " + tenant + "/" + environment,
	})
	if !fresh {
		return
	}
	a.rememberKubeContextForActivity(kubeContext)
	a.lockTerminalsForActivity(entry)
}

// finishSSHDInitByTenantEnv finalizes the activity entry and records the
// persisted last-run outcome the Manage dialog's SSH access section reads —
// the answer "did enabling SSHD work?" that otherwise vanished the moment the
// terminal scrolled past it.
func (a *App) finishSSHDInitByTenantEnv(tenant, environment string, status activityQueueStatus, errMsg string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	if a.activityQueue != nil {
		if entry, ok := a.activityQueue.findActiveByCommand("sshd-init", tenant, environment); ok {
			if final, finished := a.activityQueue.finish(entry.ID, status, errMsg); finished {
				a.unlockTerminalsForActivity(final)
			}
		}
	}
	a.emitSSHDInitCompleted(tenant, environment, status == activityQueueStatusSucceeded, errMsg)
}

// startDeployFromTrace registers a deploy entry from a `==> Deploying` trace.
// No-op when an active entry already exists, so the helm poller and trace
// handler converge on the same record.
func (a *App) startDeployFromTrace(selection uiSelection, tenant, environment, release, version string) {
	if a.activityQueue == nil {
		return
	}
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	release = strings.TrimSpace(release)
	version = strings.TrimSpace(version)
	if tenant == "" || environment == "" {
		return
	}
	if _, ok := a.activityQueue.findActiveByCommand("deploy", tenant, environment); ok {
		return
	}
	// The runtime chart's `==> Deploying` line carries no release token, so an
	// empty parsed release means the runtime deploy; fall back to its release
	// name. A non-runtime component names itself, so the drawer labels it by
	// component ("deploy erun/local · erun-backend-postgres") instead of
	// reading like a full-env redeploy.
	summary := "deploy " + tenant + "/" + environment
	if release == "" {
		release = releaseNameForTenant(tenant)
	} else {
		summary += " · " + release
	}
	kubeContext := a.resolveActivityKubeContext(selection, tenant, environment)
	entry, fresh := a.activityQueue.start(activityQueueEntry{
		Command:           "deploy",
		Tenant:            tenant,
		Environment:       environment,
		Version:           version,
		Release:           release,
		Namespace:         namespaceForTenantEnv(tenant, environment),
		KubernetesContext: kubeContext,
		Source:            "trace",
		Summary:           summary,
	})
	if !fresh {
		return
	}
	a.rememberKubeContextForActivity(kubeContext)
	a.lockTerminalsForActivity(entry)
	if a.activityStatusPoller != nil {
		a.activityStatusPoller(entry)
	}
}

// startCommandFromTrace registers a build/release/push entry from a
// `==> Building` / `==> Releasing` / `==> Pushing` trace observed in
// the session's PTY. Unlike deploy/init these umbrella lines carry no
// tenant/env (build, release, and push have no deploy target the way
// RunHelmDeploy does), so the activity is keyed off the session
// selection — the user invoked the command from a specific terminal
// tab, and that tab's env row is the natural place to render the
// spinner. No-op when the selection has no tenant/env yet (a generic
// Local shell at the repo level): there is no row to attach the
// indicator to, and a stray activity entry with empty tenant/env
// would land on every row.
//
// Intentionally skips lockTerminalsForActivity: deploys lock the
// session so a user cannot run conflicting commands while helm is
// rolling out, but build/release/push run IN the user's terminal —
// locking it would freeze the very tab the user is reading output in.
// The sidebar spinner is the only indicator these commands need.
func (a *App) startCommandFromTrace(selection uiSelection, command string) {
	if a.activityQueue == nil {
		return
	}
	tenant := strings.TrimSpace(selection.Tenant)
	environment := strings.TrimSpace(selection.Environment)
	if tenant == "" || environment == "" {
		return
	}
	if _, ok := a.activityQueue.findActiveByCommand(command, tenant, environment); ok {
		return
	}
	kubeContext := a.resolveActivityKubeContext(selection, tenant, environment)
	if _, fresh := a.activityQueue.start(activityQueueEntry{
		Command:           command,
		Tenant:            tenant,
		Environment:       environment,
		KubernetesContext: kubeContext,
		Source:            "trace",
		Summary:           command + " " + tenant + "/" + environment,
	}); !fresh {
		return
	}
	a.rememberKubeContextForActivity(kubeContext)
}

// finishCommandBySelection is keyed off the session selection because the
// build/release/push completion lines carry no tenant/env — see
// startCommandFromTrace. The unlock is idempotent in case an earlier code path
// latched a lock this entry did not.
func (a *App) finishCommandBySelection(selection uiSelection, command string, status activityQueueStatus, errMsg string) {
	if a.activityQueue == nil {
		return
	}
	tenant := strings.TrimSpace(selection.Tenant)
	environment := strings.TrimSpace(selection.Environment)
	if tenant == "" || environment == "" {
		return
	}
	entry, ok := a.activityQueue.findActiveByCommand(command, tenant, environment)
	if !ok {
		return
	}
	if final, finished := a.activityQueue.finish(entry.ID, status, errMsg); finished {
		a.unlockTerminalsForActivity(final)
	}
}
