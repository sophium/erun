package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"
)

// helmReleaseSnapshot mirrors the relevant fields of `helm list -o json`
// output. Helm emits more, but the desktop only needs name, namespace,
// status, and chart info to map a release back to a tenant/env entry.
type helmReleaseSnapshot struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Revision   string `json:"revision"`
	Status     string `json:"status"`
	Chart      string `json:"chart"`
	AppVersion string `json:"app_version"`
	Updated    string `json:"updated"`
}

// runHelmReleasePoller is the long-running goroutine that reconciles
// the activity queue's deploy entries with helm's live release state.
// Exits when stop is closed.
func (a *App) runHelmReleasePoller(stop <-chan struct{}) {
	ticker := time.NewTicker(activityPollerInterval)
	defer ticker.Stop()
	a.reconcileHelmReleasesOnce()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			a.reconcileHelmReleasesOnce()
		}
	}
}

// reconcileHelmReleasesOnce queries every watched kube context and
// applies the resulting release snapshots to the activity queue. Errors
// from a single context are swallowed so a misconfigured context does
// not stall reconciliation for the others.
func (a *App) reconcileHelmReleasesOnce() {
	if a.activityQueue == nil {
		return
	}
	contexts := a.watchedKubeContexts()
	if len(contexts) == 0 {
		return
	}
	ctx := a.activityWatcherCtx()
	for _, kubeContext := range contexts {
		releases, err := listTenantDevopsHelmReleases(ctx, kubeContext)
		if err != nil {
			continue
		}
		seen := make(map[string]struct{}, len(releases))
		for _, release := range releases {
			a.applyHelmReleaseSnapshot(kubeContext, release)
			seen[helmReleaseKey(release.Namespace, release.Name)] = struct{}{}
		}
		a.finalizeMissingHelmReleasesForContext(kubeContext, seen)
	}
}

// listTenantDevopsHelmReleases runs `helm list` filtered to releases
// whose name ends in "-devops" — the canonical tenant-devops chart name
// the runtime uses. Restricting at the helm side keeps payload size
// bounded even on clusters with many unrelated releases.
func listTenantDevopsHelmReleases(ctx context.Context, kubeContext string) ([]helmReleaseSnapshot, error) {
	args := []string{
		"list",
		"--all-namespaces",
		"--all", // include pending / failed states
		"--output", "json",
		"--filter", "-devops$",
	}
	if strings.TrimSpace(kubeContext) != "" {
		args = append(args, "--kube-context", kubeContext)
	}
	cmd := exec.CommandContext(ctx, "helm", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var releases []helmReleaseSnapshot
	if err := json.Unmarshal([]byte(trimmed), &releases); err != nil {
		return nil, err
	}
	return releases, nil
}

// applyHelmReleaseSnapshot converts a helm release status into an
// activity-queue transition. Pending statuses upsert a running entry;
// terminal statuses finalize the matching active entry idempotently.
// Releases that don't decode to a tenant/env pair are ignored.
func (a *App) applyHelmReleaseSnapshot(kubeContext string, release helmReleaseSnapshot) {
	tenant, environment, ok := splitTenantDevopsRelease(release.Name, release.Namespace)
	if !ok {
		return
	}
	switch normalizeHelmStatus(release.Status) {
	case "pending-install", "pending-upgrade", "pending-rollback", "uninstalling":
		a.upsertHelmActivity(tenant, environment, kubeContext, release)
	case "deployed":
		a.finishHelmActivityIfActive(tenant, environment, activityQueueStatusSucceeded, "")
	case "failed":
		a.finishHelmActivityIfActive(tenant, environment, activityQueueStatusFailed, "helm release failed")
	}
}

// upsertHelmActivity registers a new running entry for a pending helm
// release, or refreshes the namespace/context on an existing one. The
// entry's ID is fully determined by tenant/env/version, so the helm
// poller and the PTY trace handler converge on the same record.
func (a *App) upsertHelmActivity(tenant, environment, kubeContext string, release helmReleaseSnapshot) {
	if a.activityQueue == nil {
		return
	}
	if _, ok := a.activityQueue.findActiveByCommand("deploy", tenant, environment); ok {
		return
	}
	entry, fresh := a.activityQueue.start(activityQueueEntry{
		Command:           "deploy",
		Tenant:            tenant,
		Environment:       environment,
		Version:           strings.TrimSpace(release.AppVersion),
		Release:           release.Name,
		Namespace:         release.Namespace,
		KubernetesContext: kubeContext,
		Source:            "helm",
		Summary:           "deploy " + tenant + "/" + environment,
	})
	if !fresh {
		return
	}
	a.lockTerminalsForActivity(entry)
	if a.activityStatusPoller != nil {
		a.activityStatusPoller(entry)
	}
}

// finishHelmActivityIfActive transitions an active deploy entry to a
// terminal status when helm reports a final release state. Idempotent:
// when no active entry matches (the trace handler or pod-readiness path
// may have finalized it first), the call is a no-op.
func (a *App) finishHelmActivityIfActive(tenant, environment string, status activityQueueStatus, errMsg string) {
	if a.activityQueue == nil {
		return
	}
	active, ok := a.activityQueue.findActiveByCommand("deploy", tenant, environment)
	if !ok {
		return
	}
	if final, finished := a.activityQueue.finish(active.ID, status, errMsg); finished {
		a.unlockTerminalsForActivity(final)
	}
}

// finalizeMissingHelmReleasesForContext catches the corner case where a
// release is deleted out from under an in-flight deploy entry (for
// example, the user runs `helm uninstall` manually). The entry would
// otherwise stay running forever.
func (a *App) finalizeMissingHelmReleasesForContext(kubeContext string, seen map[string]struct{}) {
	if a.activityQueue == nil {
		return
	}
	for _, entry := range a.activityQueue.list() {
		if entry.Status != activityQueueStatusRunning {
			continue
		}
		if entry.Command != "deploy" || entry.Source != "helm" {
			continue
		}
		if entry.KubernetesContext != kubeContext {
			continue
		}
		if _, ok := seen[helmReleaseKey(entry.Namespace, entry.Release)]; ok {
			continue
		}
		// Grace period: a freshly-registered helm-source entry may
		// race the next `helm list` invocation. Skip finalize for
		// the first few seconds to let the release surface.
		if time.Since(entry.StartedAt) < 2*activityPollerInterval {
			continue
		}
		if final, finished := a.activityQueue.finish(entry.ID, activityQueueStatusFailed, "helm release no longer exists"); finished {
			a.unlockTerminalsForActivity(final)
		}
	}
}

// splitTenantDevopsRelease pulls (tenant, environment) out of a
// `<tenant>-devops` release deployed in a `<tenant>-<env>` namespace.
// Returns ok=false for any release that doesn't follow the convention.
func splitTenantDevopsRelease(release, namespace string) (string, string, bool) {
	release = strings.TrimSpace(release)
	namespace = strings.TrimSpace(namespace)
	if !strings.HasSuffix(release, "-devops") {
		return "", "", false
	}
	tenant := strings.TrimSuffix(release, "-devops")
	if tenant == "" {
		return "", "", false
	}
	prefix := tenant + "-"
	if !strings.HasPrefix(namespace, prefix) {
		return "", "", false
	}
	environment := strings.TrimPrefix(namespace, prefix)
	if environment == "" {
		return "", "", false
	}
	return tenant, environment, true
}

// normalizeHelmStatus accepts both the canonical helm status names and
// the underscore-separated variants some older helm versions emit.
func normalizeHelmStatus(status string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(status)), "_", "-")
}

func helmReleaseKey(namespace, release string) string {
	return strings.TrimSpace(namespace) + "/" + strings.TrimSpace(release)
}
