// Package deployexec runs a hosted environment's deploy server-side. The backend
// itself has no helm/kubectl, so instead of embedding the deploy tooling it
// creates a Kubernetes Job that runs `erun deploy` inside the erun-devops
// runtime image — which already carries erun + helm + kubectl — under a
// cluster-admin ServiceAccount, and watches that Job to completion. This is the
// executor half of the #605 provisioning control plane; the durable DBOS
// workflow that moves the environment's status around it lives in the provision
// package and drives this launcher through the service coordinator.
package deployexec

import (
	"context"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// A deploy that runs longer than this is treated as wedged.
	jobActiveDeadlineSeconds int64 = 30 * 60
	// No in-Job retries: the provisioning workflow owns re-runs, so a failed
	// deploy surfaces as failed rather than silently retrying under a stale spec.
	jobBackoffLimit int32 = 0
	// Jobs are cleaned up by the workflow after it reads the outcome; keep them
	// briefly for status/logs.
	jobTTLSecondsAfterFinished int32 = 10 * 60
)

// Outcome is the terminal state of a deploy Job.
type Outcome string

const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
)

// Result is the terminal result of a deploy Job. Failure carries the deploy's
// own account of why it did not succeed, read back from the Job's pod, so the
// control plane records something an operator can act on instead of the fact
// that a Job exited. Empty on success, and on a failure whose pod left nothing
// behind.
type Result struct {
	Outcome Outcome
	Failure string
}

// deployContainerName is the Job's single container, named so the log read can
// address it explicitly.
const deployContainerName = "deploy"

// DeployJobParams is the input to one hosted-env deploy Job.
type DeployJobParams struct {
	Tenant      string
	Environment string
	Version     string
	// DeployID scopes the Job to one deploy attempt. It is carried in the durable
	// workflow input, so a resumed workflow rebuilds the same Job name and
	// re-watches its in-flight Job, while a fresh attempt gets its own Job rather
	// than re-reading the previous attempt's terminal outcome. Empty keeps the
	// version-scoped name the create path has always used.
	DeployID string
	// Namespace the Job runs in (the platform namespace where the backend lives).
	Namespace string
	// Image is the erun-devops runtime image carrying erun + helm + kubectl.
	Image string
	// ServiceAccount is a cluster-admin SA so the in-Job `erun deploy` can create
	// the target namespace and install the runtime chart.
	ServiceAccount string
}

// buildDeployJob renders the deploy Job. The container runs a non-interactive
// `erun deploy <tenant> <env> --version <version>`; in-cluster, erun resolves
// the kube context from the mounted ServiceAccount.
func buildDeployJob(params DeployJobParams) *batchv1.Job {
	backoff := jobBackoffLimit
	deadline := jobActiveDeadlineSeconds
	ttl := jobTTLSecondsAfterFinished
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DeployJobName(params.Tenant, params.Environment, params.Version, params.DeployID),
			Namespace: params.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "erun-deploy-executor",
				"erun.io/tenant":               params.Tenant,
				"erun.io/environment":          params.Environment,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			ActiveDeadlineSeconds:   &deadline,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: params.ServiceAccount,
					Containers: []corev1.Container{{
						Name:    deployContainerName,
						Image:   params.Image,
						Command: []string{"erun", "deploy", params.Tenant, params.Environment, "--version", params.Version},
					}},
				},
			},
		},
	}
}

// DeployJobName is deterministic in its inputs, so a resumed workflow watches
// the Job it already created (a create conflict is treated as "watch the
// existing one") rather than starting a second rollout. An explicit deploy
// passes a per-attempt deployID so a retry of the same version is a new Job
// instead of a re-read of the previous attempt's outcome.
func DeployJobName(tenant, environment, version, deployID string) string {
	name := sanitizeLabel(tenant + "-" + environment + "-" + version)
	suffix := ""
	if deployID != "" {
		suffix = "-" + shortID(deployID)
	}
	// Kubernetes caps an object name at 63 characters; trim the descriptive
	// middle rather than the attempt suffix, which is what keeps attempts apart.
	if budget := maxJobNameLength - len(jobNamePrefix) - len(suffix); len(name) > budget {
		name = strings.Trim(name[:budget], "-")
	}
	return jobNamePrefix + name + suffix
}

const (
	jobNamePrefix    = "erun-deploy-"
	maxJobNameLength = 63
	// Enough of a random attempt id to keep concurrent and successive attempts
	// apart without crowding out the readable tenant/env/version part.
	shortIDLength = 8
)

func shortID(deployID string) string {
	id := sanitizeLabel(deployID)
	if len(id) > shortIDLength {
		id = id[:shortIDLength]
	}
	return id
}

// sanitizeLabel lowercases and replaces every character outside [a-z0-9-] so the
// result is a DNS-safe Job name component (versions carry dots, etc.).
func sanitizeLabel(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// Launcher creates and watches deploy Jobs.
type Launcher struct {
	kube      kubernetes.Interface
	pollEvery time.Duration
}

func NewLauncher(kube kubernetes.Interface) *Launcher {
	return &Launcher{kube: kube, pollEvery: 5 * time.Second}
}

// Run creates the deploy Job and blocks until it reaches a terminal state,
// returning the result. A create conflict (the Job already exists) is tolerated
// so a resumed workflow watches the in-flight Job rather than erroring.
func (l *Launcher) Run(ctx context.Context, params DeployJobParams) (Result, error) {
	job := buildDeployJob(params)
	_, err := l.kube.BatchV1().Jobs(params.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return Result{}, fmt.Errorf("create deploy job %s: %w", job.Name, err)
	}
	return l.watch(ctx, params.Namespace, job.Name)
}

func (l *Launcher) watch(ctx context.Context, namespace, name string) (Result, error) {
	for {
		job, err := l.kube.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return Result{}, fmt.Errorf("get deploy job %s: %w", name, err)
		}
		if outcome, done := jobOutcome(job); done {
			result := Result{Outcome: outcome}
			if outcome != OutcomeSucceeded {
				// Read the reason while the Job's pod is still around: the TTL
				// reaps it shortly after, and the reason is unrecoverable then.
				result.Failure = l.failureDetail(ctx, job)
			}
			return result, nil
		}
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-time.After(l.pollEvery):
		}
	}
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
