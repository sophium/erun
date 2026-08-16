// Package releaseexec cuts a tenant's release server-side. The backend has no
// erun toolchain and no checkout of the tenant's repository, so it runs the real
// `erun release` as a Kubernetes Job in the tenant's runtime image — the same
// shape the deploy executor uses, through the shared jobexec runner — and reads
// the version the release published back out of the run's own output.
//
// The Job is keyed by the release attempt, never by the branch or the commit: a
// retry has to run, and a Job named after something terminal would be re-read
// instead of re-run. The durable workflow that moves the release row around this
// launcher lives in the provision package.
package releaseexec

import (
	"context"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/jobexec"
)

const (
	// A release builds both architectures, publishes every image and chart, and
	// reads them back, so its deadline is much wider than a deploy's. Past this it
	// is wedged, not slow.
	jobActiveDeadlineSeconds int64 = 3 * 60 * 60
	// No in-Job retries. A release that failed halfway has already written to the
	// repository or the registry, so re-running it under the same Job would be a
	// second uncontrolled attempt; the queue owns retries.
	jobBackoffLimit int32 = 0
	// Long enough for the workflow to read the outcome and for an operator to look
	// at a failed run afterwards.
	jobTTLSecondsAfterFinished int32 = 30 * 60
	releaseContainerName             = "release"
	jobNamePrefix                    = "erun-release-"
)

// versionMarker is how the run hands the version it minted back. `erun release`
// prints the version on stdout and everything else on stderr, but a pod's log
// interleaves both streams, so the Job re-emits the captured version under a
// marker the executor can find unambiguously.
const versionMarker = "erun-release-version="

// Outcome is the terminal state of a release Job.
type Outcome = jobexec.Outcome

const (
	OutcomeSucceeded = jobexec.OutcomeSucceeded
	OutcomeFailed    = jobexec.OutcomeFailed
)

// Result is the terminal result of one release attempt. Version is the version
// the run published, empty unless the run succeeded and said so. Failure carries
// the release's own account of why it did not succeed.
type Result struct {
	Outcome Outcome
	Version string
	Failure string
}

// ReleaseJobParams is the input to one release Job.
type ReleaseJobParams struct {
	Tenant       string
	TargetBranch string
	// CommitID is the merge commit being released. The run fast-forwards the
	// target branch to it before releasing, so a branch that diverged refuses
	// rather than releasing something else.
	CommitID string
	// ReleaseID and Attempt scope the Job to one attempt. A resumed workflow
	// rebuilds the same name and re-watches its own in-flight Job; a retry gets a
	// new name and actually runs.
	ReleaseID string
	Attempt   int
	// Namespace the Job runs in. Pointing it at the agent environment's own
	// namespace is what puts the release next to that environment's warm
	// fingerprint cache and BuildKit state.
	Namespace string
	// Image is the tenant's runtime image, carrying erun and its toolchain.
	Image string
	// ServiceAccount the Job runs as.
	ServiceAccount string
	// HomeClaim and WorkspaceClaim, when set, mount the agent environment's own
	// volumes, so the release runs against that environment's warm caches and its
	// existing checkout instead of a cold image. Empty leaves the Job on the
	// image-baked project root.
	HomeClaim      string
	WorkspaceClaim string
	// RepoPath is the in-pod checkout the mounted workspace lands at. Set together
	// with WorkspaceClaim.
	RepoPath string
	// DryRun resolves the release without publishing anything or moving any public
	// ref. It is what makes the executor exercisable against a scoped target.
	DryRun bool
}

// ReleaseJobName is deterministic in its inputs so a resumed workflow re-watches
// the Job it already created, and attempt-scoped so a retry is a new Job rather
// than a re-read of the previous attempt's terminal outcome.
func ReleaseJobName(tenant, targetBranch, releaseID string, attempt int) string {
	name := jobexec.SanitizeName(tenant + "-" + targetBranch)
	suffix := fmt.Sprintf("-%s-%d", jobexec.ShortID(releaseID), attempt)
	// Kubernetes caps an object name at 63 characters; trim the descriptive
	// middle rather than the attempt suffix, which is what keeps attempts apart.
	if budget := jobexec.MaxJobNameLength - len(jobNamePrefix) - len(suffix); len(name) > budget {
		name = strings.Trim(name[:budget], "-")
	}
	return jobNamePrefix + name + suffix
}

// releaseScript is what the Job actually runs. It moves the checkout to the
// merge commit being released — fast-forward only, so a branch that moved
// underneath refuses instead of releasing something else — runs the real
// `erun release`, and re-emits the version it printed under a marker the
// executor can find in the interleaved pod log.
func releaseScript(params ReleaseJobParams) string {
	var script strings.Builder
	script.WriteString("set -eu\n")
	if params.RepoPath != "" {
		script.WriteString("cd " + shellQuote(params.RepoPath) + "\n")
		script.WriteString("git fetch --tags origin " + shellQuote(params.TargetBranch) + "\n")
		script.WriteString("git checkout " + shellQuote(params.TargetBranch) + "\n")
		script.WriteString("git merge --ff-only " + shellQuote(params.CommitID) + "\n")
	}
	command := "erun release"
	if params.DryRun {
		command += " --dry-run"
	}
	// `erun release` writes its resolution trace to stdout ahead of the version and
	// redirects everything after the version to stderr, so the version is the last
	// line of what it printed. Captured through an assignment rather than a pipe so
	// `set -e` still aborts on a failing release.
	script.WriteString("output=\"$(" + command + ")\"\n")
	script.WriteString("printf '" + versionMarker + "%s\\n' \"$(printf '%s\\n' \"$output\" | tail -n 1)\"\n")
	return script.String()
}

// shellQuote wraps a value for the Job's `sh -c` script. Branch names and paths
// are operator-authored, so they are quoted rather than trusted.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func buildReleaseJob(params ReleaseJobParams) *batchv1.Job {
	backoff := jobBackoffLimit
	deadline := jobActiveDeadlineSeconds
	ttl := jobTTLSecondsAfterFinished
	volumes, mounts := workspaceVolumes(params)
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ReleaseJobName(params.Tenant, params.TargetBranch, params.ReleaseID, params.Attempt),
			Namespace: params.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": ManagedByLabel,
				"erun.io/tenant":               jobexec.SanitizeName(params.Tenant),
				"erun.io/release":              jobexec.SanitizeName(params.ReleaseID),
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
					Volumes:            volumes,
					Containers: []corev1.Container{{
						Name:         releaseContainerName,
						Image:        params.Image,
						Command:      []string{"sh", "-c", releaseScript(params)},
						VolumeMounts: mounts,
						Env:          releaseEnv(params),
					}},
				},
			},
		},
	}
}

// ManagedByLabel marks the Jobs this executor owns, so a cleanup or an operator
// listing can find exactly the release runs.
const ManagedByLabel = "erun-release-executor"

// releaseEnv is the environment the release runs under. The Job replaces the
// image's entrypoint, so nothing else sets ERUN_REPO_PATH — without it erun
// resolves no project and the release fails with "cannot find git project"
// instead of releasing the checkout that was mounted for it.
func releaseEnv(params ReleaseJobParams) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{Name: "ERUN_TENANT", Value: params.Tenant},
		{Name: "ERUN_RELEASE_COMMIT", Value: params.CommitID},
	}
	if params.RepoPath != "" {
		env = append(env, corev1.EnvVar{Name: "ERUN_REPO_PATH", Value: params.RepoPath})
	}
	return env
}

// workspaceVolumes attaches the agent environment's own home and worktree
// volumes when the caller named them. Running beside the environment's warm
// fingerprint cache and existing checkout is the point of releasing here rather
// than on an ephemeral runner.
func workspaceVolumes(params ReleaseJobParams) ([]corev1.Volume, []corev1.VolumeMount) {
	var volumes []corev1.Volume
	var mounts []corev1.VolumeMount
	if params.HomeClaim != "" {
		volumes = append(volumes, corev1.Volume{
			Name:         "erun-home",
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: params.HomeClaim}},
		})
		mounts = append(mounts, corev1.VolumeMount{Name: "erun-home", MountPath: "/home/erun"})
	}
	if params.WorkspaceClaim != "" && params.RepoPath != "" {
		volumes = append(volumes, corev1.Volume{
			Name:         "repo-worktree",
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: params.WorkspaceClaim}},
		})
		mounts = append(mounts, corev1.VolumeMount{Name: "repo-worktree", MountPath: params.RepoPath})
	}
	return volumes, mounts
}

// Launcher creates and watches release Jobs.
type Launcher struct {
	runner *jobexec.Runner
}

func NewLauncher(kube kubernetes.Interface) *Launcher {
	return &Launcher{runner: jobexec.NewRunner(kube, jobexec.Options{
		Kind:      "release",
		Container: releaseContainerName,
		// The release's result is the version it printed, so the output has to be
		// pulled back while the pod is still there.
		CaptureOutput: true,
	})}
}

// PollEvery sets how often the watch re-reads the Job's status.
func (l *Launcher) PollEvery(every time.Duration) { l.runner.PollEvery = every }

// Run creates the release Job and blocks until it reaches a terminal state.
func (l *Launcher) Run(ctx context.Context, params ReleaseJobParams) (Result, error) {
	result, err := l.runner.Run(ctx, buildReleaseJob(params))
	if err != nil {
		return Result{}, err
	}
	return Result{
		Outcome: result.Outcome,
		Version: versionFromOutput(result.Output),
		Failure: result.Failure,
	}, nil
}

// versionFromOutput picks the version out of the run's log. The last marker line
// wins, so a version echoed earlier in the run's own trace cannot be mistaken for
// the one it published.
func versionFromOutput(output string) string {
	version := ""
	for _, line := range strings.Split(output, "\n") {
		if rest, found := strings.CutPrefix(strings.TrimSpace(line), versionMarker); found {
			version = strings.TrimSpace(rest)
		}
	}
	return version
}
