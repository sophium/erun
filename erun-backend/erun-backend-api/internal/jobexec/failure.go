package jobexec

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
	// erun's errors span several lines, and tooling appends its own output.
	failureLogTailLines int64 = 200
	// erun writes its failure last, so leading lines are dropped first.
	failureDetailLines           = 40
	maxFailureDetailLength       = 4000
	maxFailureLogBytes     int64 = 256 * 1024
)

// failureDetail returns the run's own failure output, falling back to the
// pod's status and then the Job's terminal condition when there is no log.
func (r *Runner) failureDetail(ctx context.Context, job *batchv1.Job) string {
	pod := r.newestJobPod(ctx, job)
	if pod != nil {
		if detail := failureFromLog(r.podLog(ctx, pod)); detail != "" {
			return detail
		}
		if detail := podFailureDetail(r.options.Kind, pod); detail != "" {
			return detail
		}
	}
	return jobFailureDetail(r.options.Kind, job)
}

// A Job never admitted (or a test fake) has no selector, so the per-Job pod label stands in.
func (r *Runner) newestJobPod(ctx context.Context, job *batchv1.Job) *corev1.Pod {
	selector := "job-name=" + job.Name
	if job.Spec.Selector != nil {
		if formatted := metav1.FormatLabelSelector(job.Spec.Selector); formatted != "" && formatted != "<none>" {
			selector = formatted
		}
	}
	pods, err := r.kube.CoreV1().Pods(job.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil || len(pods.Items) == 0 {
		return nil
	}
	items := pods.Items
	sort.Slice(items, func(a, b int) bool {
		return items[a].CreationTimestamp.Time.Before(items[b].CreationTimestamp.Time)
	})
	return &items[len(items)-1]
}

// A read failure yields nothing; the pod may never have started a container.
func (r *Runner) podLog(ctx context.Context, pod *corev1.Pod) string {
	tail := failureLogTailLines
	stream, err := r.kube.CoreV1().Pods(pod.Namespace).
		GetLogs(pod.Name, &corev1.PodLogOptions{Container: r.options.Container, TailLines: &tail}).
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

func failureFromLog(logText string) string {
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

func truncateFailureDetail(detail string) string {
	if len(detail) <= maxFailureDetailLength {
		return detail
	}
	return "…\n" + detail[len(detail)-maxFailureDetailLength:]
}

func podFailureDetail(kind string, pod *corev1.Pod) string {
	statuses := append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...)
	for _, status := range statuses {
		if waiting := status.State.Waiting; waiting != nil && waiting.Reason != "" {
			return fmt.Sprintf("%s pod %s: container %s is waiting: %s", kind, pod.Name, status.Name, joinReasonMessage(waiting.Reason, waiting.Message))
		}
		if terminated := status.State.Terminated; terminated != nil && terminated.ExitCode != 0 {
			return fmt.Sprintf("%s pod %s: container %s exited %d: %s", kind, pod.Name, status.Name, terminated.ExitCode, joinReasonMessage(terminated.Reason, terminated.Message))
		}
	}
	if pod.Status.Reason != "" || pod.Status.Message != "" {
		return fmt.Sprintf("%s pod %s is %s: %s", kind, pod.Name, pod.Status.Phase, joinReasonMessage(pod.Status.Reason, pod.Status.Message))
	}
	return ""
}

// jobFailureDetail covers a Job whose pods were already reclaimed, e.g. after a deadline.
func jobFailureDetail(kind string, job *batchv1.Job) string {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			if detail := joinReasonMessage(condition.Reason, condition.Message); detail != "" {
				return kind + " job " + job.Name + " failed: " + detail
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
