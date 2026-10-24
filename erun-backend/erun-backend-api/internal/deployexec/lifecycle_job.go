package deployexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

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
	// StopID gives a fresh stop its own Job; empty keeps the bare name.
	StopID    string
	Placement PlacementParams
}

// DeleteJobParams is the input to one hosted-env delete Job.
type DeleteJobParams struct {
	Tenant         string
	Environment    string
	Namespace      string
	Image          string
	ServiceAccount string
	// DeleteID gives a retried delete its own Job.
	DeleteID  string
	Placement PlacementParams
	// Both set chains `erun unexpose` after the delete; either empty skips it.
	ExposeServicesZone      string
	ExposePlatformNamespace string
}

// StopJobName appends a per-attempt suffix so a fresh stop never replays a
// terminal Job's outcome.
func StopJobName(tenant, environment, stopID string) string {
	return lifecycleJobName(stopJobNamePrefix, tenant, environment, stopID)
}

// DeleteJobName appends a per-attempt suffix so a retry never replays a terminal
// Job's outcome.
func DeleteJobName(tenant, environment, deleteID string) string {
	return lifecycleJobName(deleteJobNamePrefix, tenant, environment, deleteID)
}

func lifecycleJobName(prefix, tenant, environment, attemptID string) string {
	name := jobexec.SanitizeName(tenant + "-" + environment)
	suffix := ""
	if attemptID != "" {
		suffix = "-" + jobexec.ShortID(attemptID)
	}
	// Trim the middle, not the attempt suffix, to fit the 63-character name limit.
	if budget := jobexec.MaxJobNameLength - len(prefix) - len(suffix); len(name) > budget {
		name = strings.Trim(name[:budget], "-")
	}
	return prefix + name + suffix
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

func buildLifecycleCommand(tenant, environment string, placement PlacementParams, argv []string) []string {
	return []string{"sh", "-c", bootstrapEnvironmentScript(tenant, environment, placement) + shellJoin(argv)}
}

func buildStopJob(params StopJobParams) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      StopJobName(params.Tenant, params.Environment, params.StopID),
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

func buildDeleteJob(params DeleteJobParams) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DeleteJobName(params.Tenant, params.Environment, params.DeleteID),
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

// --output json is needed because `erun delete` exits 0 even when namespace
// teardown fails; unexpose is chained best-effort.
func buildDeleteCommand(params DeleteJobParams) []string {
	deleteArgv := []string{"erun", "delete", params.Tenant, params.Environment, "-y", "--output", "json"}
	script := bootstrapEnvironmentScript(params.Tenant, params.Environment, params.Placement) + shellJoin(deleteArgv)
	if zone, ns := strings.TrimSpace(params.ExposeServicesZone), strings.TrimSpace(params.ExposePlatformNamespace); zone != "" && ns != "" {
		unexpose := []string{"erun", "unexpose", params.Tenant, params.Environment, "--skip-if-unconfigured", "--services-zone", zone, "--platform-namespace", ns}
		script += unexposeChainScript(unexpose)
	}
	return []string{"sh", "-c", script}
}

// The environment row is gone when the Job finishes, so the Job log is the only
// record of an unexpose failure.
const unexposeFailureMarker = "ERUN_UNEXPOSE_FAILED"

func unexposeChainScript(unexpose []string) string {
	return " && { unexpose_out=$(" + shellJoin(unexpose) + " 2>&1) || printf '" + unexposeFailureMarker + ": %s\\n' \"$unexpose_out\"; }"
}

// UnexposeFailureFromOutput extracts the chained unexpose failure, or "" if none.
func UnexposeFailureFromOutput(output string) string {
	marker := unexposeFailureMarker + ": "
	idx := strings.Index(output, marker)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(output[idx+len(marker):])
}

type deleteJobResult struct {
	NamespaceDeleteError string `json:"namespaceDeleteError"`
}

// NamespaceDeleteFailureFromOutput extracts the namespace teardown failure, or "".
// It is checked even on a successful Job because `erun delete` exits 0 on that
// failure; the JSON is pretty-printed, so decoding starts at the first '{'.
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

// RunStop creates the stop Job and blocks until it is terminal. Stop has no
// durable workflow or DB claim, so it re-watches an in-flight stop Job rather
// than starting a second.
func (l *Launcher) RunStop(ctx context.Context, params StopJobParams) (Result, error) {
	if err := ensurePlacementSecret(ctx, l.kube, params.Namespace, params.Placement); err != nil {
		return Result{}, fmt.Errorf("provision placement credential: %w", err)
	}
	activeName, err := l.activeStopJobName(ctx, params)
	if err != nil {
		return Result{}, err
	}
	if activeName != "" {
		return l.stopRunner.Watch(ctx, params.Namespace, activeName)
	}
	return l.stopRunner.Run(ctx, buildStopJob(params))
}

// Lifecycle Jobs share labels, so stop Jobs are told apart by name prefix.
func (l *Launcher) activeStopJobName(ctx context.Context, params StopJobParams) (string, error) {
	selector := labels.SelectorFromSet(lifecycleJobLabels(params.Tenant, params.Environment)).String()
	jobs, err := l.kube.BatchV1().Jobs(params.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return "", fmt.Errorf("list stop jobs: %w", err)
	}
	for i := range jobs.Items {
		job := &jobs.Items[i]
		if !strings.HasPrefix(job.Name, stopJobNamePrefix) {
			continue
		}
		if !jobexec.IsTerminal(job) {
			return job.Name, nil
		}
	}
	return "", nil
}

// RunDelete creates the delete Job and blocks until it is terminal.
func (l *Launcher) RunDelete(ctx context.Context, params DeleteJobParams) (Result, error) {
	if err := ensurePlacementSecret(ctx, l.kube, params.Namespace, params.Placement); err != nil {
		return Result{}, fmt.Errorf("provision placement credential: %w", err)
	}
	return l.deleteRunner.Run(ctx, buildDeleteJob(params))
}
