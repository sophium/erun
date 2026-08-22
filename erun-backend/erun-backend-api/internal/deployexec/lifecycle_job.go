// lifecycle_job.go extends the deploy executor to the two other environment
// lifecycle actions that need the tenant's own erun toolchain: stopping a
// runtime (scale to zero) and deleting one (tear down its namespace). Both
// reuse the same jobexec machinery as deploy — only the Job's command and name
// differ — so a Job's create/watch/failure-read-back behavior never diverges
// between the three.
package deployexec

import (
	"context"
	"encoding/json"
	"fmt"
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
	// Placement names the cluster this stop targets (#1112); see
	// DeployJobParams.Placement.
	Placement PlacementParams
}

// DeleteJobParams is the input to one hosted-env delete Job.
type DeleteJobParams struct {
	Tenant         string
	Environment    string
	Namespace      string
	Image          string
	ServiceAccount string
	// Placement names the cluster this delete targets (#1112); see
	// DeployJobParams.Placement.
	Placement PlacementParams
	// ExposeServicesZone/ExposePlatformNamespace thread the same platform
	// coordinates as DeployJobParams' fields of the same name, so the delete
	// Job can chain `erun unexpose` — removing the per-env wildcard
	// DNS record `erun expose` created, symmetric with the deploy Job chaining
	// `erun expose` itself. Either left empty skips the chain entirely: the
	// delete Job stays exactly the plain `erun delete` it always ran.
	ExposeServicesZone      string
	ExposePlatformNamespace string
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
// buildDeployCommand wraps `erun deploy`: seed the kubeconfig and on-disk env
// config for the placed cluster first, then run the real command. Deploy
// composes extra flags and an optional chained `erun expose`, so it builds
// its own `sh -c` string; stop and delete need nothing beyond one command and
// share this.
func buildLifecycleCommand(tenant, environment string, placement PlacementParams, argv []string) []string {
	return []string{"sh", "-c", bootstrapEnvironmentScript(tenant, environment, placement) + shellJoin(argv)}
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
			Command: buildLifecycleCommand(params.Tenant, params.Environment, params.Placement, []string{"erun", "stop", params.Tenant, params.Environment}),
			Env:     placementEnvVars(params.Placement),
		}, params.ServiceAccount),
	}
}

// buildDeleteJob's container runs a non-interactive `erun delete <tenant>
// <env> -y`, skipping the CLI's interactive confirmation the same way the
// deploy Job's command is already non-interactive, then — when platform
// coordinates are configured — chains a best-effort `erun unexpose` so
// the per-env wildcard DNS record `erun expose` created does not outlive the
// namespace it pointed at.
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
			Command: buildDeleteCommand(params),
			Env:     placementEnvVars(params.Placement),
		}, params.ServiceAccount),
	}
}

// buildDeleteCommand composes the delete Job's argv: `erun delete --output
// json` (identical to buildLifecycleCommand's shape, plus --output json so
// NamespaceDeleteFailureFromOutput can read back whether the namespace
// teardown itself completed — `erun delete`'s local semantics keep going, and
// exit 0, even when that fails), then — when both platform coordinates are
// set — a best-effort chained `erun unexpose` so a leftover DNS record
// doesn't outlive the namespace it pointed at.
func buildDeleteCommand(params DeleteJobParams) []string {
	deleteArgv := []string{"erun", "delete", params.Tenant, params.Environment, "-y", "--output", "json"}
	script := bootstrapEnvironmentScript(params.Tenant, params.Environment, params.Placement) + shellJoin(deleteArgv)
	if zone, ns := strings.TrimSpace(params.ExposeServicesZone), strings.TrimSpace(params.ExposePlatformNamespace); zone != "" && ns != "" {
		unexpose := []string{"erun", "unexpose", params.Tenant, params.Environment, "--skip-if-unconfigured", "--services-zone", zone, "--platform-namespace", ns}
		script += unexposeChainScript(unexpose)
	}
	return []string{"sh", "-c", script}
}

// unexposeFailureMarker mirrors exposeFailureMarker: the chained
// `erun unexpose` step is best-effort, so a DNS cleanup failure must not fail
// the delete Job — the namespace already tore down successfully — but its
// reason is still worth capturing for an operator reading the Job's logs,
// since the environment row is gone by the time the Job finishes and there is
// nowhere else to record it.
const unexposeFailureMarker = "ERUN_UNEXPOSE_FAILED"

func unexposeChainScript(unexpose []string) string {
	return " && { unexpose_out=$(" + shellJoin(unexpose) + " 2>&1) || printf '" + unexposeFailureMarker + ": %s\\n' \"$unexpose_out\"; }"
}

// UnexposeFailureFromOutput extracts the chained unexpose step's failure
// detail from the delete Job's captured stdout, mirroring
// ExposeFailureFromOutput. "" means unexpose succeeded, was never attempted
// (no platform coordinates configured), or the Job predates chaining it at
// all.
func UnexposeFailureFromOutput(output string) string {
	marker := unexposeFailureMarker + ": "
	idx := strings.Index(output, marker)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(output[idx+len(marker):])
}

// deleteJobResult is the one field of eruncommon.DeleteEnvironmentResult the
// delete Job's --json output carries that the API needs back.
type deleteJobResult struct {
	NamespaceDeleteError string `json:"namespaceDeleteError"`
}

// NamespaceDeleteFailureFromOutput extracts the delete Job's own namespace
// teardown failure from its captured `erun delete --output json` result (see
// buildDeleteCommand). "" means the namespace was confirmed torn down (or the
// Job predates --output json). Checked regardless of the Job's own exit code:
// `erun delete` treats a namespace-teardown failure as non-fatal for itself
// (its own local config cleanup still runs and it still exits 0), so a Job
// the jobexec runner reports as succeeded can still have left the namespace
// stuck — this is the only place that failure is visible. The result is
// pretty-printed JSON (eruncommon.Context.WriteResult), so this decodes from
// the first '{' rather than scanning single lines, and ignores whatever
// non-JSON audit/trace text or chained unexpose output surrounds it.
func NamespaceDeleteFailureFromOutput(output string) string {
	idx := strings.IndexByte(output, '{')
	if idx < 0 {
		return ""
	}
	var result deleteJobResult
	if err := json.NewDecoder(strings.NewReader(output[idx:])).Decode(&result); err != nil {
		return ""
	}
	return result.NamespaceDeleteError
}

// RunStop creates the stop Job and blocks until it reaches a terminal state.
// When params.Placement targets a remote cluster (#1112), it first upserts
// the Secret the Job's container reads its admin token from.
func (l *Launcher) RunStop(ctx context.Context, params StopJobParams) (Result, error) {
	if err := ensurePlacementSecret(ctx, l.kube, params.Namespace, params.Placement); err != nil {
		return Result{}, fmt.Errorf("provision placement credential: %w", err)
	}
	return l.stopRunner.Run(ctx, buildStopJob(params))
}

// RunDelete creates the delete Job and blocks until it reaches a terminal
// state. When params.Placement targets a remote cluster (#1112), it first
// upserts the Secret the Job's container reads its admin token from.
func (l *Launcher) RunDelete(ctx context.Context, params DeleteJobParams) (Result, error) {
	if err := ensurePlacementSecret(ctx, l.kube, params.Namespace, params.Placement); err != nil {
		return Result{}, fmt.Errorf("provision placement credential: %w", err)
	}
	return l.deleteRunner.Run(ctx, buildDeleteJob(params))
}
