package activity

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// resolveActivityKubeContext prefers the selection's context, falling back to the
// env config's so pollers never target kubectl's current-context.
func (s *Service) resolveActivityKubeContext(selection uimodel.Selection, tenant, environment string) string {
	if ctx := strings.TrimSpace(selection.KubernetesContext); ctx != "" {
		return ctx
	}
	envConfig, _, err := s.d.Store.LoadEnvConfig(tenant, environment)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(envConfig.KubernetesContext)
}

// startStatusPoller polls the entry's containers until it leaves the active set.
func (s *Service) startStatusPoller(entry Entry) {
	go s.pollContainerStatuses(context.Background(), entry)
}

// pollContainerStatuses is display-only and never finalizes: pods can
// report Ready before helm --wait returns.
func (s *Service) pollContainerStatuses(ctx context.Context, entry Entry) {
	ticker := time.NewTicker(containerPollInterval)
	defer ticker.Stop()
	pollOnce := func() bool {
		if _, ok := s.queue.findActive(entry.Tenant, entry.Environment); !ok {
			return false
		}
		statuses, err := s.d.FetchContainerStatuses(ctx, entry)
		if err != nil {
			return true
		}
		s.queue.updateContainers(entry.ID, statuses)
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

// FetchContainerStatusesViaKubectl reads the entry's release pods; it errors on a
// kubectl outage so the frontend keeps the last-known state.
func FetchContainerStatusesViaKubectl(ctx context.Context, entry Entry) ([]ContainerStatus, error) {
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

func parseActivityContainerStatuses(raw []byte) ([]ContainerStatus, error) {
	var parsed kubectlPodList
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	out := make([]ContainerStatus, 0)
	seen := make(map[string]bool)
	for _, item := range parsed.Items {
		// Spec containers carry images before Status reports them.
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

func convertActivityContainerStatus(cs kubectlContainerStatus, imageByName map[string]string) ContainerStatus {
	image := strings.TrimSpace(cs.Image)
	if image == "" {
		image = imageByName[cs.Name]
	}
	status := ContainerStatus{
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

func activityTargetForRuntime(entry Entry) string {
	target := strings.TrimSpace(entry.Tenant) + "/" + strings.TrimSpace(entry.Environment)
	if version := strings.TrimSpace(entry.Version); version != "" {
		target += " " + version
	}
	return target
}
