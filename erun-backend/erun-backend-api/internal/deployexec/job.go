// Package deployexec runs a hosted environment's deploy server-side. The backend
// itself has no helm/kubectl, so instead of embedding the deploy tooling it
// creates a Kubernetes Job that runs `erun deploy` inside the erun-devops
// runtime image — which already carries erun + helm + kubectl — under a
// cluster-admin ServiceAccount, and watches that Job to completion. The Job
// launch, watch and failure read-back are the shared jobexec machinery; this
// package owns only the deploy's own Job shape. The durable DBOS workflow that
// moves the environment's status around it lives in the provision package and
// drives this launcher through the service coordinator.
package deployexec

import (
	"context"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/jobexec"
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

// Outcome and Result are the shared terminal shapes; deploy callers keep naming
// them through this package.
type Outcome = jobexec.Outcome

const (
	OutcomeSucceeded = jobexec.OutcomeSucceeded
	OutcomeFailed    = jobexec.OutcomeFailed
)

type Result = jobexec.Result

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
	name := jobexec.SanitizeName(tenant + "-" + environment + "-" + version)
	suffix := ""
	if deployID != "" {
		suffix = "-" + jobexec.ShortID(deployID)
	}
	// Kubernetes caps an object name at 63 characters; trim the descriptive
	// middle rather than the attempt suffix, which is what keeps attempts apart.
	if budget := jobexec.MaxJobNameLength - len(jobNamePrefix) - len(suffix); len(name) > budget {
		name = strings.Trim(name[:budget], "-")
	}
	return jobNamePrefix + name + suffix
}

const jobNamePrefix = "erun-deploy-"

// Launcher creates and watches deploy Jobs.
type Launcher struct {
	runner *jobexec.Runner
}

func NewLauncher(kube kubernetes.Interface) *Launcher {
	return &Launcher{runner: jobexec.NewRunner(kube, jobexec.Options{
		Kind:      "deploy",
		Container: deployContainerName,
	})}
}

// PollEvery sets how often the watch re-reads the Job's status.
func (l *Launcher) PollEvery(every time.Duration) { l.runner.PollEvery = every }

// Run creates the deploy Job and blocks until it reaches a terminal state,
// returning the result.
func (l *Launcher) Run(ctx context.Context, params DeployJobParams) (Result, error) {
	return l.runner.Run(ctx, buildDeployJob(params))
}
