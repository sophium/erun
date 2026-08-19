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
	// ExposeTargetIP is the platform's ingress IP the per-env wildcard DNS record
	// points at. Empty (the default, unset ERUN_ENV_EXPOSE_TARGET_IP) leaves the
	// Job at the plain `erun deploy` it has always run — no attempt to expose,
	// no new failure mode. Set, the Job chains `erun expose <tenant> <environment>
	// mcp --ip <ExposeTargetIP> --skip-if-unconfigured` after a successful
	// deploy: --skip-if-unconfigured makes the chain a no-op success for any
	// tenant whose own project declares no platform block, so this only ever
	// wires DNS+Ingress for an actual erunpaas platform deployment.
	ExposeTargetIP string
}

// mcpExposeService is the logical service name the deploy Job exposes: the
// env's MCP edge, reachable at mcp.<tenant>-<environment>.<services zone> —
// the hostname #605's acceptance criterion names.
const mcpExposeService = "mcp"

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
						Command: buildDeployCommand(params),
					}},
				},
			},
		},
	}
}

// buildDeployCommand composes the Job's argv. Plain `erun deploy` when no
// ExposeTargetIP is configured — byte-for-byte what this Job has always run,
// so an unconfigured platform (no ERUN_ENV_EXPOSE_TARGET_IP) deploys exactly
// as before. Configured, it chains a second, independently-skippable primitive
// after a successful deploy rather than teaching deploy itself about exposure:
// this Job is the caller composing pure primitives (root AGENTS.md § "Command
// primitives vs orchestration"), not a new deploy behavior.
func buildDeployCommand(params DeployJobParams) []string {
	deploy := []string{"erun", "deploy", params.Tenant, params.Environment, "--version", params.Version}
	ip := strings.TrimSpace(params.ExposeTargetIP)
	if ip == "" {
		return deploy
	}
	expose := []string{"erun", "expose", params.Tenant, params.Environment, mcpExposeService, "--ip", ip, "--skip-if-unconfigured"}
	return []string{"sh", "-c", shellJoin(deploy) + " && " + shellJoin(expose)}
}

// shellJoin renders argv as a POSIX shell command line, single-quoting every
// argument so tenant/environment/version/IP values (already validated as
// DNS-1123 labels or IPs upstream, but not this function's job to trust) can
// never be interpreted as shell syntax.
func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
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

// Launcher creates and watches deploy, stop, and delete Jobs. One instance
// covers all three lifecycle actions; each gets its own jobexec.Runner only
// because Kind/Container differ, not a second launcher.
type Launcher struct {
	runner       *jobexec.Runner
	stopRunner   *jobexec.Runner
	deleteRunner *jobexec.Runner
}

func NewLauncher(kube kubernetes.Interface) *Launcher {
	return &Launcher{
		runner:       jobexec.NewRunner(kube, jobexec.Options{Kind: "deploy", Container: deployContainerName}),
		stopRunner:   jobexec.NewRunner(kube, jobexec.Options{Kind: "stop", Container: stopContainerName}),
		deleteRunner: jobexec.NewRunner(kube, jobexec.Options{Kind: "delete", Container: deleteContainerName}),
	}
}

// PollEvery sets how often every runner's watch re-reads its Job's status.
func (l *Launcher) PollEvery(every time.Duration) {
	l.runner.PollEvery = every
	l.stopRunner.PollEvery = every
	l.deleteRunner.PollEvery = every
}

// Run creates the deploy Job and blocks until it reaches a terminal state,
// returning the result.
func (l *Launcher) Run(ctx context.Context, params DeployJobParams) (Result, error) {
	return l.runner.Run(ctx, buildDeployJob(params))
}
