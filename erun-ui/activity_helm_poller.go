package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
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

// A single context's errors are swallowed so one misconfigured context
// does not stall reconciliation for the rest.
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

// Uses explicit status flags instead of `--all` (dropped in helm v4; the
// explicit flags work on both v3 and v4). If `helm list` errors,
// reconciliation silently stalls and deploy entries stay stuck at
// "running" in the activity panel, so the flag choice is load-bearing.
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
	eruncommon.HideConsoleWindow(cmd)
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

// Tolerance for matching a "deployed" release to a just-registered entry:
// wide enough to absorb a few poller intervals plus clock skew, tight
// enough that a stale prior deploy from minutes ago can't slip through.
const helmDeployedFreshnessSkew = 60 * time.Second

// finishHelmDeployedIfActive finalizes a deploy entry on helm "deployed",
// but only for the deploy this entry is tracking: a version match guards
// against a stale prior "deployed", and timestamp freshness catches
// same-version redeploys a version match alone can't tell apart. When the
// evidence is inconclusive it defers to the `==> Deployed` trace line and
// the pod-readiness watchdog rather than finalizing.
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

// observedRuntimeVersion is the version an observed release says the environment
// is running. That is the release's appVersion, which is its *chart's* version --
// the same number as the runtime image's only while the two ride one release
// line. An env that states its chart separately (EnvConfig.runtimechart) has them
// apart, so reporting the chart's number would label the environment with a
// version it is not running: exactly the confusion naming the coordinates
// separately exists to end. There the env's recorded runtime version is the
// answer, written by the deploy that rolled it.
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

// Timestamp shapes `helm list -o json` has been seen to emit across helm
// versions: Go's default time.Time.String() form, sometimes with
// sub-second precision trimmed or the timezone abbreviation dropped.
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

// The entry ID is derived from tenant/env/version, so the helm poller and
// the PTY trace handler converge on one record instead of creating
// duplicate deploy entries.
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

// Idempotent by design: the trace handler or pod-readiness path may
// finalize the entry first, so no matching active entry is expected here,
// not an error.
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
		// A freshly-registered helm-source entry may race the next
		// `helm list`; give the release a moment to surface before
		// treating it as gone.
		if time.Since(entry.StartedAt) < 2*activityPollerInterval {
			continue
		}
		if final, finished := a.activityQueue.finish(entry.ID, activityQueueStatusFailed, "helm release no longer exists"); finished {
			a.unlockTerminalsForActivity(final)
		}
	}
}

// Encodes the release/namespace naming convention: a `<tenant>-devops`
// release lives in a `<tenant>-<env>` namespace.
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
