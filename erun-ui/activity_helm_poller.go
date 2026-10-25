package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

type helmReleaseSnapshot struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Revision   string `json:"revision"`
	Status     string `json:"status"`
	Chart      string `json:"chart"`
	AppVersion string `json:"app_version"`
	Updated    string `json:"updated"`
}

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

// One context's errors are swallowed so it cannot stall the rest.
func (a *App) reconcileHelmReleasesOnce() {
	if a.activityQueue == nil {
		return
	}
	contexts := a.watchedKubeContexts()
	if len(contexts) == 0 {
		return
	}
	ctx := a.context()
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

// Explicit status flags, not `--all`, which helm v4 dropped; a failing list
// leaves deploy entries stuck running.
func helmListTenantDevopsArgs(kubeContext string) []string {
	args := []string{
		"list",
		"--all-namespaces",
		"--deployed",
		"--pending",
		"--failed",
		"--uninstalling",
		"--output", "json",
		"--filter", "-devops$",
	}
	if strings.TrimSpace(kubeContext) != "" {
		args = append(args, "--kube-context", kubeContext)
	}
	return args
}

func listTenantDevopsHelmReleases(ctx context.Context, kubeContext string) ([]helmReleaseSnapshot, error) {
	cmd := exec.CommandContext(ctx, "helm", helmListTenantDevopsArgs(kubeContext)...)
	core.HideConsoleWindow(cmd)
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

func (a *App) applyHelmReleaseSnapshot(kubeContext string, release helmReleaseSnapshot) {
	tenant, environment, ok := splitTenantDevopsRelease(release.Name, release.Namespace)
	if !ok {
		return
	}
	switch normalizeHelmStatus(release.Status) {
	case "pending-install", "pending-upgrade", "pending-rollback", "uninstalling":
		a.upsertHelmActivity(tenant, environment, kubeContext, release)
	case "deployed":
		a.finishHelmDeployedIfActive(tenant, environment, release)
	case "failed":
		a.finishHelmActivityIfActive(tenant, environment, activityQueueStatusFailed, "helm release failed")
	}
}

// helmDeployedFreshnessSkew absorbs poller intervals and clock skew without admitting a stale deploy.
const helmDeployedFreshnessSkew = 60 * time.Second

// finishHelmDeployedIfActive finalizes only the tracked deploy: version match
// rejects a stale release, freshness catches same-version redeploys.
func (a *App) finishHelmDeployedIfActive(tenant, environment string, release helmReleaseSnapshot) {
	if a.activityQueue == nil {
		return
	}
	active, ok := a.activityQueue.findActiveByCommand("deploy", tenant, environment)
	if !ok {
		return
	}
	if !helmDeployedSnapshotMatchesEntry(release, a.observedRuntimeVersion(tenant, environment, release), active) {
		return
	}
	if final, finished := a.activityQueue.finish(active.ID, activityQueueStatusSucceeded, ""); finished {
		a.unlockTerminalsForActivity(final)
	}
}

// observedRuntimeVersion is the release's appVersion (the chart's), unless the
// env states its runtime chart separately, where the recorded runtime version wins.
func (a *App) observedRuntimeVersion(tenant, environment string, release helmReleaseSnapshot) string {
	observed := strings.TrimSpace(release.AppVersion)
	env, ok := a.lookupEnvConfig(tenant, environment)
	if !ok || strings.TrimSpace(env.RuntimeChart) == "" {
		return observed
	}
	if recorded := strings.TrimSpace(env.RuntimeVersion); recorded != "" {
		return recorded
	}
	return observed
}

func helmDeployedSnapshotMatchesEntry(release helmReleaseSnapshot, observed string, entry activityQueueEntry) bool {
	expected := strings.TrimSpace(entry.Version)
	observed = strings.TrimSpace(observed)
	if expected == "" || observed == "" || expected != observed {
		return false
	}
	updated, ok := parseHelmUpdated(release.Updated)
	if !ok {
		return false
	}
	if updated.Add(helmDeployedFreshnessSkew).Before(entry.StartedAt) {
		return false
	}
	return true
}

// helmUpdatedLayouts are the timestamp shapes helm versions emit.
var helmUpdatedLayouts = []string{
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05 -0700 MST",
	"2006-01-02 15:04:05.999999999 -0700",
	"2006-01-02 15:04:05 -0700",
	time.RFC3339Nano,
	time.RFC3339,
}

func parseHelmUpdated(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range helmUpdatedLayouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// The entry ID derives from tenant/env/version so the poller and trace handler converge on one entry.
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
		Version:           a.observedRuntimeVersion(tenant, environment, release),
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

// Idempotent: other paths may finalize first.
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

// finalizeMissingHelmReleasesForContext finalizes entries whose release was deleted mid-deploy.
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
		// A fresh entry may race the next `helm list`.
		if time.Since(entry.StartedAt) < 2*activityPollerInterval {
			continue
		}
		if final, finished := a.activityQueue.finish(entry.ID, activityQueueStatusFailed, "helm release no longer exists"); finished {
			a.unlockTerminalsForActivity(final)
		}
	}
}

// A `<tenant>-devops` release lives in a `<tenant>-<env>` namespace.
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

// normalizeHelmStatus also accepts older helm's underscore-separated names.
func normalizeHelmStatus(status string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(status)), "_", "-")
}

func helmReleaseKey(namespace, release string) string {
	return strings.TrimSpace(namespace) + "/" + strings.TrimSpace(release)
}
