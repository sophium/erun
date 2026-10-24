package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

// resolveActivityKubeContext picks the kube context to attach to a new
// trace-source activity entry. Selection wins when it carries a
// context (the session has been bound through the desktop UI). When it
// doesn't — e.g. a generic Local tab where the user invoked `erun open`
// manually — fall back to the env config's KubernetesContext so the
// container-status poller and lock-on-deploy watchlist still target
// the right cluster instead of whatever `kubectl config
// current-context` happens to be.
func (a *App) resolveActivityKubeContext(selection uiSelection, tenant, environment string) string {
	if ctx := strings.TrimSpace(selection.KubernetesContext); ctx != "" {
		return ctx
	}
	if a.deps.store == nil {
		return ""
	}
	envConfig, _, err := a.deps.store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(envConfig.KubernetesContext)
}

// pollActivityContainerStatuses feeds the queue per-container Ready snapshots
// so the frontend can render Ready pills while a deploy runs.
//
// The loop is intentionally display-only: it does NOT finalize entries. Pod
// readiness can flip to Ready seconds before helm's `--wait` returns
// (readyReplicas trails container readiness while controllers reconcile), so
// finalizing here would mark the activity done while the terminal still shows
// the deploy running. Completion is owned by the trace handler's `==> Deployed`
// line and by the helm poller's version+freshness check (a backstop if the PTY
// dies).
func (a *App) pollActivityContainerStatuses(ctx context.Context, entry activityQueueEntry) {
	if a.activityQueue == nil {
		return
	}
	ticker := time.NewTicker(activityQueuePollInterval)
	defer ticker.Stop()
	pollOnce := func() bool {
		if _, ok := a.activityQueue.findActive(entry.Tenant, entry.Environment); !ok {
			return false
		}
		statuses, err := a.fetchActivityContainerStatuses(ctx, entry)
		if err != nil {
			return true
		}
		a.activityQueue.updateContainers(entry.ID, statuses)
		return true
	}
	if !pollOnce() {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !pollOnce() {
				return
			}
		}
	}
}

// fetchActivityContainerStatuses returns an error rather than an empty snapshot
// on a transient kubectl outage, so the frontend keeps showing the last-known
// state until the next successful poll.
func (a *App) fetchActivityContainerStatuses(ctx context.Context, entry activityQueueEntry) ([]activityQueueContainerStatus, error) {
	args := []string{"get", "pods", "-l", "app=" + entry.Release, "-o", "json"}
	if strings.TrimSpace(entry.KubernetesContext) != "" {
		args = append([]string{"--context", entry.KubernetesContext}, args...)
	}
	if strings.TrimSpace(entry.Namespace) != "" {
		args = append(args, "--namespace", entry.Namespace)
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	core.HideConsoleWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseActivityContainerStatuses(out)
}

// kubectlPodList is the subset of `kubectl get pods -o json` the container-status
// poller decodes.
type kubectlPodList struct {
	Items []kubectlPodItem `json:"items"`
}

type kubectlPodItem struct {
	Spec struct {
		Containers []struct {
			Name  string `json:"name"`
			Image string `json:"image"`
		} `json:"containers"`
	} `json:"spec"`
	Status struct {
		ContainerStatuses []kubectlContainerStatus `json:"containerStatuses"`
	} `json:"status"`
}

type kubectlContainerStatus struct {
	Name         string `json:"name"`
	Image        string `json:"image"`
	Ready        bool   `json:"ready"`
	RestartCount int    `json:"restartCount"`
	State        struct {
		Waiting *struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"waiting,omitempty"`
		Running *struct {
			StartedAt string `json:"startedAt"`
		} `json:"running,omitempty"`
		Terminated *struct {
			Reason   string `json:"reason"`
			Message  string `json:"message"`
			ExitCode int    `json:"exitCode"`
		} `json:"terminated,omitempty"`
	} `json:"state"`
}

func parseActivityContainerStatuses(raw []byte) ([]activityQueueContainerStatus, error) {
	var parsed kubectlPodList
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	out := make([]activityQueueContainerStatus, 0)
	seen := make(map[string]bool)
	for _, item := range parsed.Items {
		// Spec containers give us images even when Status hasn't reported
		// them yet (very early in pod lifecycle).
		imageByName := make(map[string]string, len(item.Spec.Containers))
		for _, c := range item.Spec.Containers {
			imageByName[c.Name] = c.Image
		}
		for _, cs := range item.Status.ContainerStatuses {
			if seen[cs.Name] {
				continue
			}
			seen[cs.Name] = true
			out = append(out, convertActivityContainerStatus(cs, imageByName))
		}
	}
	return out, nil
}

func convertActivityContainerStatus(cs kubectlContainerStatus, imageByName map[string]string) activityQueueContainerStatus {
	image := strings.TrimSpace(cs.Image)
	if image == "" {
		image = imageByName[cs.Name]
	}
	status := activityQueueContainerStatus{
		Name:     cs.Name,
		Image:    image,
		Ready:    cs.Ready,
		Restarts: cs.RestartCount,
	}
	switch {
	case cs.State.Running != nil:
		status.Phase = "Running"
	case cs.State.Waiting != nil:
		status.Phase = "Waiting"
		status.Reason = cs.State.Waiting.Reason
		status.Message = cs.State.Waiting.Message
	case cs.State.Terminated != nil:
		status.Phase = "Terminated"
		status.Reason = cs.State.Terminated.Reason
		status.Message = fmt.Sprintf("exited with code %d", cs.State.Terminated.ExitCode)
		if msg := strings.TrimSpace(cs.State.Terminated.Message); msg != "" {
			status.Message += ": " + msg
		}
	default:
		status.Phase = "Pending"
	}
	return status
}

func activityTargetForRuntime(entry activityQueueEntry) string {
	target := strings.TrimSpace(entry.Tenant) + "/" + strings.TrimSpace(entry.Environment)
	if version := strings.TrimSpace(entry.Version); version != "" {
		target += " " + version
	}
	return target
}
