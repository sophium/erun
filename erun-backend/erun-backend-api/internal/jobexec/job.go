// Package jobexec runs one unit of server-side work as a Kubernetes Job and
// watches it to a terminal outcome. The backend carries no erun toolchain of its
// own, so work that needs one — deploying an environment, cutting a release —
// runs in the tenant's runtime image under a scoped ServiceAccount rather than
// being embedded here. What a caller gets back is the run's own account of
// itself: a failure's reason is read off the pod while the pod still exists, and
// a run whose result is carried in what it printed can have that captured too.
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

// Outcome is the terminal state of a Job.
type Outcome string

const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
)

// Result is the terminal result of a Job. Failure carries the run's own account
// of why it did not succeed, so a caller records something an operator can act
// on instead of the fact that a Job exited. Empty on success, and on a failure
// whose pod left nothing behind.
type Result struct {
	Outcome Outcome
	Failure string
	// Output is the tail of the run's own output, present only when the runner
	// was asked to capture it. It is how a run hands back a value it minted.
	Output string
}

// Options describes the work a Runner supervises: Kind names it in recorded
// reasons, Container is the Job's single container (addressed explicitly by the
// log read), and CaptureOutput pulls the output back on success too.
type Options struct {
	Kind          string
	Container     string
	CaptureOutput bool
}

// Runner creates and watches Jobs.
type Runner struct {
	kube    kubernetes.Interface
	options Options
	// PollEvery is how often the watch re-reads the Job's status.
	PollEvery time.Duration
}

func NewRunner(kube kubernetes.Interface, options Options) *Runner {
	return &Runner{kube: kube, options: options, PollEvery: 5 * time.Second}
}

// Run creates the Job and blocks until it reaches a terminal state, returning
// the result. A create conflict (the Job already exists) is tolerated so a
// resumed workflow watches the in-flight Job rather than erroring.
func (r *Runner) Run(ctx context.Context, job *batchv1.Job) (Result, error) {
	_, err := r.kube.BatchV1().Jobs(job.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return Result{}, fmt.Errorf("create %s job %s: %w", r.options.Kind, job.Name, err)
	}
	return r.watch(ctx, job.Namespace, job.Name)
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

// terminal reads whatever the caller needs off the pod while it is still around:
// the TTL reaps it shortly after, and neither the failure reason nor the run's
// output is recoverable then.
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

// jobOutcome maps a Job's status to a terminal outcome, or done=false while it is
// still running.
func jobOutcome(job *batchv1.Job) (Outcome, bool) {
	if job.Status.Succeeded > 0 {
		return OutcomeSucceeded, true
	}
	if job.Status.Failed > 0 {
		return OutcomeFailed, true
	}
	return "", false
}
