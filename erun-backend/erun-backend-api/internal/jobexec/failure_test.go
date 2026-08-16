package jobexec

import (
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// chartNotFoundLog is what the deploy Job actually leaves behind when the version
// it was asked for has no published runtime chart: erun's trace of the ladder it
// walked, then the error naming every coordinate probed and the ways out. This is
// the text the environment must end up recording.
const chartNotFoundLog = `deploy: resolving erun-devops for acme/prod at 1.2.3
deploy: runtime chart acme-devops 1.2.3 not found in ghcr.io/acme (the tenant's own umbrella)
deploy: runtime chart erun-devops 1.2.3 not found in ghcr.io/acme (the shared platform chart)
runtime chart oci://ghcr.io/acme/charts/erun-devops version 1.2.3 could not be pulled from ghcr.io/acme: no chart is published at 1.2.3 at any coordinate the deploy probed — ghcr.io/acme/charts/acme-devops (the tenant's own umbrella), ghcr.io/acme/charts/erun-devops (the shared platform chart). The erun-devops platform chart is published only beside the runtime image erun releases.
Error: failed to download oci://ghcr.io/acme/charts/erun-devops
`

const testContainerName = "deploy"

func TestFailureFromLogCarriesTheActionableError(t *testing.T) {
	detail := failureFromLog(chartNotFoundLog)
	for _, want := range []string{"1.2.3", "ghcr.io/acme/charts/erun-devops", "ghcr.io/acme/charts/acme-devops", "could not be pulled"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail = %q, want it to carry %q", detail, want)
		}
	}
}

// TestFailureFromLogKeepsTheEnd: erun prints its failure last, so a long run must
// give up its leading trace rather than its error.
func TestFailureFromLogKeepsTheEnd(t *testing.T) {
	noise := strings.Repeat("deploy: a trace line that says nothing about the failure\n", failureDetailLines*3)
	detail := failureFromLog(noise + chartNotFoundLog)
	if !strings.Contains(detail, "could not be pulled") {
		t.Fatalf("detail = %q, want the trailing failure to survive the trace", detail)
	}
	if lines := strings.Count(detail, "\n") + 1; lines > failureDetailLines {
		t.Fatalf("detail kept %d lines, want at most %d", lines, failureDetailLines)
	}
}

// TestFailureFromLogIsBounded: the reason is read over the API, so one noisy run
// must not write an unbounded blob onto the resource it is recorded against.
func TestFailureFromLogIsBounded(t *testing.T) {
	long := strings.Repeat("x", maxFailureDetailLength*2) + "\nthe failure"
	detail := failureFromLog(long)
	if len(detail) > maxFailureDetailLength+len("…\n") {
		t.Fatalf("detail is %d bytes, want it bounded at %d", len(detail), maxFailureDetailLength)
	}
	if !strings.HasSuffix(detail, "the failure") {
		t.Fatalf("truncation dropped the failure: %q", detail)
	}
}

func TestFailureFromLogIgnoresEmptyOutput(t *testing.T) {
	if detail := failureFromLog("\n  \n\t\n"); detail != "" {
		t.Fatalf("detail = %q, want empty so the pod status can explain instead", detail)
	}
}

// TestPodFailureDetailNamesAnUnpullableImage: a Job whose runtime image the
// cluster cannot pull never logs a line, so its pod status is the only account of
// the failure.
func TestPodFailureDetailNamesAnUnpullableImage(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "erun-deploy-acme-prod-1-2-3-abcde"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  testContainerName,
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: `pull access denied for ghcr.io/acme/acme-devops:1.2.3`}},
			}},
		},
	}
	detail := podFailureDetail("deploy", pod)
	for _, want := range []string{"ImagePullBackOff", "ghcr.io/acme/acme-devops:1.2.3", pod.Name} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail = %q, want it to carry %q", detail, want)
		}
	}
}

func TestPodFailureDetailReportsANonZeroExit(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "erun-deploy-acme-prod-1-2-3-abcde"},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  testContainerName,
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error"}},
			}},
		},
	}
	if detail := podFailureDetail("deploy", pod); !strings.Contains(detail, "exited 1") {
		t.Fatalf("detail = %q, want the exit code", detail)
	}
}

// TestPodFailureDetailSaysNothingAboutAHealthyPod keeps the fallback ordering
// honest: a pod that ran fine has no status to report, so the log stays the
// source of the reason.
func TestPodFailureDetailSaysNothingAboutAHealthyPod(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "erun-deploy-acme-prod-1-2-3-abcde"},
		Status: corev1.PodStatus{
			Phase: corev1.PodSucceeded,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  testContainerName,
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}},
			}},
		},
	}
	if detail := podFailureDetail("deploy", pod); detail != "" {
		t.Fatalf("detail = %q, want empty", detail)
	}
}

func TestJobFailureDetailReportsTheTerminalCondition(t *testing.T) {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "erun-deploy-acme-prod-1-2-3"},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
			Type:    batchv1.JobFailed,
			Status:  corev1.ConditionTrue,
			Reason:  "BackoffLimitExceeded",
			Message: "Job has reached the specified backoff limit",
		}}},
	}
	detail := jobFailureDetail("deploy", job)
	if !strings.Contains(detail, "BackoffLimitExceeded") || !strings.Contains(detail, job.Name) {
		t.Fatalf("detail = %q, want the job name and its terminal condition", detail)
	}
	if jobFailureDetail("deploy", &batchv1.Job{}) != "" {
		t.Fatal("a job with no terminal condition reported one")
	}
}

// TestJobFailureDetailNamesTheKind: the recorded reason has to say which kind of
// run failed, because a release and a deploy land on different resources.
func TestJobFailureDetailNamesTheKind(t *testing.T) {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "erun-release-acme-main-1"},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
			Type:   batchv1.JobFailed,
			Status: corev1.ConditionTrue,
			Reason: "DeadlineExceeded",
		}}},
	}
	if detail := jobFailureDetail("release", job); !strings.HasPrefix(detail, "release job ") {
		t.Fatalf("detail = %q, want it to name the release kind", detail)
	}
}
