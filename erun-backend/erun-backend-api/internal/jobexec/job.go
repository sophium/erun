// Package jobexec runs server-side work as a Kubernetes Job in the tenant's
// runtime image and watches it to a terminal outcome.
package jobexec

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type Outcome string

const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
)

// Result is a Job's terminal result; Failure holds the run's own failure output.
type Result struct {
	Outcome Outcome
	Failure string
	// Output is the run's output tail, set only when CaptureOutput is requested.
	Output string
}

// Options describes a supervised Job; Container is the one the log read addresses.
type Options struct {
	Kind          string
	Container     string
	CaptureOutput bool
}

type Runner struct {
	kube      kubernetes.Interface
	options   Options
	PollEvery time.Duration
}

func NewRunner(kube kubernetes.Interface, options Options) *Runner {
	return &Runner{kube: kube, options: options, PollEvery: 5 * time.Second}
}

// Run creates the Job and blocks until it is terminal. An existing Job is
// watched, so a resumed workflow does not error.
func (r *Runner) Run(ctx context.Context, job *batchv1.Job) (Result, error) {
	_, err := r.kube.BatchV1().Jobs(job.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return Result{}, fmt.Errorf("create %s job %s: %w", r.options.Kind, job.Name, err)
	}
	return r.watch(ctx, job.Namespace, job.Name)
}

// Watch blocks on an already-created Job until it is terminal.
func (r *Runner) Watch(ctx context.Context, namespace, name string) (Result, error) {
	return r.watch(ctx, namespace, name)
}

func (r *Runner) watch(ctx context.Context, namespace, name string) (Result, error) {
	for {
		job, err := r.kube.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return Result{}, fmt.Errorf("get %s job %s: %w", r.options.Kind, name, err)
		}
		if outcome, done := jobOutcome(job); done {
			return r.terminal(ctx, job, outcome), nil
		}
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-time.After(r.PollEvery):
		}
	}
}

// The TTL reaps the pod shortly after, so its log must be read now.
func (r *Runner) terminal(ctx context.Context, job *batchv1.Job, outcome Outcome) Result {
	if outcome != OutcomeSucceeded {
		return Result{Outcome: outcome, Failure: r.failureDetail(ctx, job)}
	}
	result := Result{Outcome: outcome}
	if r.options.CaptureOutput {
		if pod := r.newestJobPod(ctx, job); pod != nil {
			result.Output = r.podLog(ctx, pod)
		}
	}
	return result
}

func jobOutcome(job *batchv1.Job) (Outcome, bool) {
	if job.Status.Succeeded > 0 {
		return OutcomeSucceeded, true
	}
	if job.Status.Failed > 0 {
		return OutcomeFailed, true
	}
	return "", false
}

// IsTerminal reports whether a Job has reached a terminal outcome.
func IsTerminal(job *batchv1.Job) bool {
	_, done := jobOutcome(job)
	return done
}
