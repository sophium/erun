// lifecycle_job.go extends the deploy executor to the two other environment
// lifecycle actions that need the tenant's own erun toolchain: stopping a
// runtime (scale to zero) and deleting one (tear down its namespace). Both
// reuse the same jobexec machinery as deploy — only the Job's command and name
// differ — so a Job's create/watch/failure-read-back behavior never diverges
// between the three.
package deployexec

import (
	"context"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/jobexec"
)

const (
	stopContainerName   = "stop"
	deleteContainerName = "delete"
	stopJobNamePrefix   = "erun-stop-"
	deleteJobNamePrefix = "erun-delete-"
)

// StopJobParams is the input to one hosted-env stop Job.
type StopJobParams struct {
	Tenant         string
	Environment    string
	Namespace      string
	Image          string
	ServiceAccount string
}

// DeleteJobParams is the input to one hosted-env delete Job.
type DeleteJobParams struct {
	Tenant         string
	Environment    string
	Namespace      string
	Image          string
	ServiceAccount string
}

// StopJobName is deterministic in its inputs, so a retried stop watches the
// Job it already created rather than starting a second one.
func StopJobName(tenant, environment string) string {
	return lifecycleJobName(stopJobNamePrefix, tenant, environment)
}

// DeleteJobName is deterministic in its inputs, so a retried delete watches
// the Job it already created rather than starting a second one.
func DeleteJobName(tenant, environment string) string {
	return lifecycleJobName(deleteJobNamePrefix, tenant, environment)
}

func lifecycleJobName(prefix, tenant, environment string) string {
	name := jobexec.SanitizeName(tenant + "-" + environment)
	if budget := jobexec.MaxJobNameLength - len(prefix); len(name) > budget {
		name = strings.Trim(name[:budget], "-")
	}
	return prefix + name
}

func lifecycleJobSpec(container corev1.Container, serviceAccount string) batchv1.JobSpec {
	backoff := jobBackoffLimit
	deadline := jobActiveDeadlineSeconds
	ttl := jobTTLSecondsAfterFinished
	return batchv1.JobSpec{
		BackoffLimit:            &backoff,
		ActiveDeadlineSeconds:   &deadline,
		TTLSecondsAfterFinished: &ttl,
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				RestartPolicy:      corev1.RestartPolicyNever,
				ServiceAccountName: serviceAccount,
				Containers:         []corev1.Container{container},
			},
		},
	}
}

func lifecycleJobLabels(tenant, environment string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by": "erun-deploy-executor",
		"erun.io/tenant":               tenant,
		"erun.io/environment":          environment,
	}
}

// buildLifecycleCommand wraps a non-interactive erun verb the same way
// buildDeployCommand wraps `erun deploy`: seed the in-cluster kubeconfig and
// on-disk env config first, then run the real command. Deploy composes extra
// flags and an optional chained `erun expose`, so it builds its own `sh -c`
// string; stop and delete need nothing beyond one command and share this.
func buildLifecycleCommand(tenant, environment string, argv []string) []string {
	return []string{"sh", "-c", bootstrapEnvironmentScript(tenant, environment) + shellJoin(argv)}
}

// buildStopJob's container runs a non-interactive `erun stop <tenant> <env>`.
func buildStopJob(params StopJobParams) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      StopJobName(params.Tenant, params.Environment),
			Namespace: params.Namespace,
			Labels:    lifecycleJobLabels(params.Tenant, params.Environment),
		},
		Spec: lifecycleJobSpec(corev1.Container{
			Name:    stopContainerName,
			Image:   params.Image,
			Command: buildLifecycleCommand(params.Tenant, params.Environment, []string{"erun", "stop", params.Tenant, params.Environment}),
		}, params.ServiceAccount),
	}
}

// buildDeleteJob's container runs a non-interactive `erun delete <tenant>
// <env> -y`, skipping the CLI's interactive confirmation the same way the
// deploy Job's command is already non-interactive.
func buildDeleteJob(params DeleteJobParams) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DeleteJobName(params.Tenant, params.Environment),
			Namespace: params.Namespace,
			Labels:    lifecycleJobLabels(params.Tenant, params.Environment),
		},
		Spec: lifecycleJobSpec(corev1.Container{
			Name:    deleteContainerName,
			Image:   params.Image,
			Command: buildLifecycleCommand(params.Tenant, params.Environment, []string{"erun", "delete", params.Tenant, params.Environment, "-y"}),
		}, params.ServiceAccount),
	}
}

// RunStop creates the stop Job and blocks until it reaches a terminal state.
func (l *Launcher) RunStop(ctx context.Context, params StopJobParams) (Result, error) {
	return l.stopRunner.Run(ctx, buildStopJob(params))
}

// RunDelete creates the delete Job and blocks until it reaches a terminal state.
func (l *Launcher) RunDelete(ctx context.Context, params DeleteJobParams) (Result, error) {
	return l.deleteRunner.Run(ctx, buildDeleteJob(params))
}
