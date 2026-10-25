package main

import (
	"strings"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// finishDeployByTenantEnv finalizes by the tenant/env parsed from the trace line,
// falling back to the selection, since the observing tab may target another env.
func (a *App) finishDeployByTenantEnv(selection uimodel.Selection, tenant, environment string, status activityQueueStatus, errMsg string) {
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
			// A successful deploy clears any stale 'failed' flag on the row.
			a.emitEnvStatus(uimodel.Selection{Tenant: tenant, Environment: environment}, "")
		}
		if status == activityQueueStatusSucceeded || status == activityQueueStatusSkipped {
			// Releases the create→deploy→open gate only once the runtime exists.
			a.envops.EnvironmentDeployed(tenant, environment)
		}
	}
}

// startInitFromTrace registers the umbrella init entry; init's helm step still
// gets its own deploy entry.
func (a *App) startInitFromTrace(selection uimodel.Selection, tenant, environment string) {
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

// startDoctorFromTrace never locks terminals: doctor prompts interactively in the
// terminal it runs in.
func (a *App) startDoctorFromTrace(selection uimodel.Selection, tenant, environment string) {
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

// finishDoctorByTenantEnv also records the last-run outcome the Manage dialog shows.
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
	a.envops.DoctorCompleted(tenant, environment, status == activityQueueStatusSucceeded, errMsg)
}

// startSSHDInitFromTrace locks terminals like init/deploy; it has no interactive prompts.
func (a *App) startSSHDInitFromTrace(selection uimodel.Selection, tenant, environment string) {
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

// finishSSHDInitByTenantEnv also records the last-run outcome the Manage dialog shows.
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
	a.envops.SSHDInitCompleted(tenant, environment, status == activityQueueStatusSucceeded, errMsg)
}

// startDeployFromTrace is a no-op when an active entry exists, so poller and trace converge.
func (a *App) startDeployFromTrace(selection uimodel.Selection, tenant, environment, release, version string) {
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
	// An empty release means the runtime chart; other components are labeled by name.
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

// startCommandFromTrace keys build/release/push on the selection, since their
// trace lines carry no tenant/env; no-op without one. It never locks: these run
// in the terminal the user is reading.
func (a *App) startCommandFromTrace(selection uimodel.Selection, command string) {
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

// finishCommandBySelection's unlock is idempotent.
func (a *App) finishCommandBySelection(selection uimodel.Selection, command string, status activityQueueStatus, errMsg string) {
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
