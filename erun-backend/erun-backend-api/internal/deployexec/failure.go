package deployexec

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// How much of the deploy's output to pull back. An actionable erun error runs
	// to several lines — a runtime chart that cannot be pulled enumerates every
	// registry and chart the deploy probed — and helm appends its own output under
	// it, so the window has to be wider than a one-line error.
	failureLogTailLines int64 = 200
	// What survives into the environment's recorded reason. `erun deploy` writes
	// its failure last, so the tail of the log is the failure; the leading lines
	// are the trace that led there and are dropped first when the cap bites.
	failureDetailLines     = 40
	maxFailureDetailLength = 4000
)

// failureDetail explains why a deploy Job did not succeed, in the words of the
// deploy itself. The Job runs `erun deploy` in the tenant's runtime image, so the
// operator-actionable error — the version, the registries probed, the way out —
// is already printed there; without pulling it back the control plane can only
// record that a Job exited, which names nothing an operator can act on. When the
// pod produced no output (it never ran, or the Job outlived it) the pod's own
// status and then the Job's terminal condition stand in.
func (l *Launcher) failureDetail(ctx context.Context, job *batchv1.Job) string {
	pod := l.newestJobPod(ctx, job)
	if pod != nil {
		if detail := deployFailureFromLog(l.podLog(ctx, pod)); detail != "" {
			return detail
		}
		if detail := podFailureDetail(pod); detail != "" {
			return detail
		}
	}
	return jobFailureDetail(job)
}

// newestJobPod finds the pod that ran this attempt. The Job's own selector is
// authoritative once the API server has defaulted it; a Job that was never
// admitted (or a fake in tests) has none, so the conventional per-Job pod label
// stands in.
func (l *Launcher) newestJobPod(ctx context.Context, job *batchv1.Job) *corev1.Pod {
	selector := "job-name=" + job.Name
	if job.Spec.Selector != nil {
		if formatted := metav1.FormatLabelSelector(job.Spec.Selector); formatted != "" && formatted != "<none>" {
			selector = formatted
		}
	}
	pods, err := l.kube.CoreV1().Pods(job.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil || len(pods.Items) == 0 {
		return nil
	}
	items := pods.Items
	sort.Slice(items, func(a, b int) bool {
		return items[a].CreationTimestamp.Time.Before(items[b].CreationTimestamp.Time)
	})
	return &items[len(items)-1]
}

// podLog reads the tail of the deploy container's output. A read failure is not
// itself reportable — the pod may never have started a container — so it yields
// nothing and lets the pod status explain instead.
func (l *Launcher) podLog(ctx context.Context, pod *corev1.Pod) string {
	tail := failureLogTailLines
	stream, err := l.kube.CoreV1().Pods(pod.Namespace).
		GetLogs(pod.Name, &corev1.PodLogOptions{Container: deployContainerName, TailLines: &tail}).
		Stream(ctx)
	if err != nil {
		return ""
	}
	defer func() { _ = stream.Close() }()
	content, err := io.ReadAll(io.LimitReader(stream, maxFailureLogBytes))
	if err != nil {
		return ""
	}
	return string(content)
}

// maxFailureLogBytes bounds what one failing deploy can pull into the control
// plane's memory; the detail is drawn from the end of the window regardless.
const maxFailureLogBytes int64 = 256 * 1024

// deployFailureFromLog distils the recorded reason out of the deploy's output.
// `erun deploy` prints its trace as it works and its failure last, so the
// trailing lines are the failure plus just enough of the run to place it.
func deployFailureFromLog(logText string) string {
	lines := make([]string, 0, failureDetailLines)
	for _, line := range strings.Split(logText, "\n") {
		if trimmed := strings.TrimRight(line, " \t\r"); strings.TrimSpace(trimmed) != "" {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > failureDetailLines {
		lines = lines[len(lines)-failureDetailLines:]
	}
	return truncateFailureDetail(strings.Join(lines, "\n"))
}

// truncateFailureDetail keeps the end of the detail, which is where the failure
// is, and marks that something was dropped so a reader knows the record is a
// tail rather than the whole run.
func truncateFailureDetail(detail string) string {
	if len(detail) <= maxFailureDetailLength {
		return detail
	}
	return "…\n" + detail[len(detail)-maxFailureDetailLength:]
}

// podFailureDetail reports why the deploy pod produced no output — an image the
// cluster cannot pull, a scheduling refusal, a container that died before
// writing. Without it those failures are indistinguishable from a silent deploy.
func podFailureDetail(pod *corev1.Pod) string {
	statuses := append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...)
	for _, status := range statuses {
		if waiting := status.State.Waiting; waiting != nil && waiting.Reason != "" {
			return fmt.Sprintf("deploy pod %s: container %s is waiting: %s", pod.Name, status.Name, joinReasonMessage(waiting.Reason, waiting.Message))
		}
		if terminated := status.State.Terminated; terminated != nil && terminated.ExitCode != 0 {
			return fmt.Sprintf("deploy pod %s: container %s exited %d: %s", pod.Name, status.Name, terminated.ExitCode, joinReasonMessage(terminated.Reason, terminated.Message))
		}
	}
	if pod.Status.Reason != "" || pod.Status.Message != "" {
		return fmt.Sprintf("deploy pod %s is %s: %s", pod.Name, pod.Status.Phase, joinReasonMessage(pod.Status.Reason, pod.Status.Message))
	}
	return ""
}

// jobFailureDetail is the last resort: a Job whose pods the cluster already
// reclaimed (a deadline overrun is the usual one) still carries its terminal
// condition, and naming it beats recording nothing.
func jobFailureDetail(job *batchv1.Job) string {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			if detail := joinReasonMessage(condition.Reason, condition.Message); detail != "" {
				return "deploy job " + job.Name + " failed: " + detail
			}
		}
	}
	return ""
}

func joinReasonMessage(reason, message string) string {
	reason = strings.TrimSpace(reason)
	message = strings.TrimSpace(message)
	switch {
	case reason != "" && message != "":
		return reason + ": " + message
	case reason != "":
		return reason
	default:
		return message
	}
}
