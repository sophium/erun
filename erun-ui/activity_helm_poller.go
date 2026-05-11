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

// helmListTenantDevopsArgs is the argv we pass to `helm list` to
// enumerate `<tenant>-devops` releases across the cluster.
//
// We pass the explicit status flags (`--deployed --pending --failed
// --uninstalling`) instead of the older `--all` umbrella flag because
// helm v4 dropped `--all`. An erroring `helm list` would silently take
// down the whole reconciliation channel: finalization on "deployed"
// plus pending-state upserts both stop firing, leaving entries stuck
// at "running" in the activity panel. The explicit flags are accepted
// on both helm v3 and v4.
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

// listTenantDevopsHelmReleases runs `helm list` filtered to releases
// whose name ends in "-devops" — the canonical tenant-devops chart name
// the runtime uses. Restricting at the helm side keeps payload size
// bounded even on clusters with many unrelated releases.
func listTenantDevopsHelmReleases(ctx context.Context, kubeContext string) ([]helmReleaseSnapshot, error) {
	cmd := exec.CommandContext(ctx, "helm", helmListTenantDevopsArgs(kubeContext)...)
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
		a.finishHelmDeployedIfActive(tenant, environment, release)
	case "failed":
		a.finishHelmActivityIfActive(tenant, environment, activityQueueStatusFailed, "helm release failed")
	}
}

// helmDeployedFreshnessSkew is the tolerance applied when comparing a
// helm release's Updated timestamp against the entry's StartedAt. It
// covers the realistic gap between helm marking a release Updated and
// the desktop registering the corresponding entry — a few poller
// intervals plus modest clock skew — without being so loose that a
// stale prior deploy from minutes ago slips through.
const helmDeployedFreshnessSkew = 60 * time.Second

// finishHelmDeployedIfActive finalizes an active deploy entry when helm
// reports the release as "deployed", but only when the snapshot
// describes the deploy this entry is tracking.
//
// Two checks gate finalization:
//
//   - Version match: helm's release.AppVersion must equal entry.Version.
//     A "deployed" status with the previous deploy's AppVersion is the
//     stale state we're racing — finalizing on it would mark the
//     activity done with the user's new version still rolling out.
//   - Updated freshness: release.Updated must be within
//     helmDeployedFreshnessSkew of (or after) entry.StartedAt. This
//     catches same-version redeploys (common in snapshot workflows)
//     where AppVersion alone can't distinguish the prior "deployed"
//     from the new one.
//
// Either check missing data (unparseable Updated, empty AppVersion or
// Version) is treated as inconclusive and we defer to the trace
// handler's `==> Deployed` line and the pod-readiness watchdog. Helm
// "failed" still finalizes unconditionally so a PTY that dies mid-deploy
// cannot leave an entry stuck running.
func (a *App) finishHelmDeployedIfActive(tenant, environment string, release helmReleaseSnapshot) {
	if a.activityQueue == nil {
		return
	}
	active, ok := a.activityQueue.findActiveByCommand("deploy", tenant, environment)
	if !ok {
		return
	}
	if !helmDeployedSnapshotMatchesEntry(release, active) {
		return
	}
	if final, finished := a.activityQueue.finish(active.ID, activityQueueStatusSucceeded, ""); finished {
		a.unlockTerminalsForActivity(final)
	}
}

// helmDeployedSnapshotMatchesEntry reports whether a "deployed" helm
// release snapshot describes the deploy that the supplied entry is
// tracking, by checking version match and Updated freshness. Returns
// false (defer finalization) whenever evidence is missing — a missing
// AppVersion, an unparseable Updated, or an Updated older than the
// entry's StartedAt by more than the skew tolerance.
func helmDeployedSnapshotMatchesEntry(release helmReleaseSnapshot, entry activityQueueEntry) bool {
	expected := strings.TrimSpace(entry.Version)
	observed := strings.TrimSpace(release.AppVersion)
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

// helmUpdatedLayouts lists the timestamp formats `helm list -o json`
// has been observed to emit. Helm v3+ uses Go's default
// time.Time.String() shape ("2006-01-02 15:04:05.999999999 -0700 MST");
// some versions trim sub-second precision or omit the timezone
// abbreviation. We try each in order and accept the first that parses.
var helmUpdatedLayouts = []string{
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05 -0700 MST",
	"2006-01-02 15:04:05.999999999 -0700",
	"2006-01-02 15:04:05 -0700",
	time.RFC3339Nano,
	time.RFC3339,
}

// parseHelmUpdated parses the `updated` field from `helm list -o json`.
// The string is the Go default time.Time format; we accept a few
// variants for robustness across helm versions.
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
