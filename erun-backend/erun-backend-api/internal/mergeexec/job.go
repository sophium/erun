// Package mergeexec runs a review's merge-queue gate server-side: it builds the
// prospective merge of the review's source branch onto its *current* target
// branch, gates that prospective merge with a real `erun build`, and pushes the
// result only when the gate is green. This is the piece a merge queue exists
// for — catching two reviews that are each green alone but broken together
// before the target branch ever moves — and it runs as a Kubernetes Job in the
// tenant's runtime image through the shared jobexec runner, the same shape
// releaseexec uses. The durable DBOS workflow that dispatches this launcher and
// records its outcome lives in the provision package.
package mergeexec

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
	// The gate is one project's `erun build`, not a multi-arch publish, so it
	// needs far less time than a release; past this it is wedged, not slow.
	jobActiveDeadlineSeconds int64 = 60 * 60
	// No in-Job retries: a merge Job that failed after pushing has already
	// changed the target branch, so a second uncontrolled attempt under the same
	// Job is not safe. The queue's own retry is a fresh promotion, not a Job
	// restart.
	jobBackoffLimit int32 = 0
	// Long enough for the workflow to read the outcome and for an operator to
	// inspect a failed run afterwards.
	jobTTLSecondsAfterFinished int32 = 30 * 60
	mergeContainerName               = "merge"
	jobNamePrefix                    = "erun-merge-"
)

// Outcome is the shared terminal-state type.
type Outcome = jobexec.Outcome

const (
	OutcomeSucceeded = jobexec.OutcomeSucceeded
	OutcomeFailed    = jobexec.OutcomeFailed
)

// The Job prints both markers from an EXIT trap installed right after it learns
// the source commit, so they survive as the very last lines of output
// regardless of how much `erun build` itself printed before failing — the
// pod-log tail jobexec reads back is bounded, and a marker printed only at
// success time (release's approach) would not survive a failure that happens
// after pages of build output.
const (
	sourceCommitMarker = "erun-merge-source-commit="
	mergeCommitMarker  = "erun-merge-commit="
)

// Result is the terminal result of one merge-gate attempt.
type Result struct {
	Outcome Outcome
	// MergeCommit is the squash-merge commit built on the target branch and
	// pushed only once the gate build passed. Empty when the merge itself never
	// produced a commit (an unresolved conflict) or the Job never got far enough
	// to attempt one.
	MergeCommit string
	// SourceCommit is the source branch's tip at the moment the Job fetched it —
	// the only artifact available to record a failed attempt against when no
	// merge commit exists.
	SourceCommit string
	// Failure carries the gate's own account of why it did not succeed.
	Failure string
}

// MergeJobParams is the input to one merge-gate Job.
type MergeJobParams struct {
	Tenant       string
	TargetBranch string
	SourceBranch string
	// MergeMessage is the review's name, the squash merge commit message.
	MergeMessage string
	// ReviewID scopes the Job name to this review's own attempt, keyed further by
	// the workflow's own build-id-derived key so a genuine retry gets a fresh
	// name rather than re-reading a previous attempt's terminal outcome.
	ReviewID string
	Key      string
	// Namespace the Job runs in — the agent environment's own namespace, so the
	// gate build hits that environment's warm fingerprint and BuildKit caches.
	Namespace string
	// Image is the tenant's runtime image, carrying erun and its toolchain.
	Image string
	// ServiceAccount the Job runs as.
	ServiceAccount string
	// HomeClaim and WorkspaceClaim mount the agent environment's own volumes, so
	// the gate runs against that environment's warm caches and existing
	// checkout. Unlike the release Job, the merge Job has no image-baked
	// fallback: it fetches, commits, and pushes, which needs a real writable
	// checkout with push credentials, not whatever happens to be baked into the
	// image at build time.
	HomeClaim      string
	WorkspaceClaim string
	// RepoPath is the in-pod checkout the mounted workspace lands at.
	RepoPath string
}

// MergeJobName is deterministic in its inputs so a resumed workflow re-watches
// the Job it already created, and key-scoped so a genuine retry (a new ready
// build promoted into MERGE) is a new Job rather than a re-read of the previous
// attempt's terminal outcome.
func MergeJobName(tenant, targetBranch, reviewID, key string) string {
	name := jobexec.SanitizeName(tenant + "-" + targetBranch)
	suffix := "-" + jobexec.ShortID(reviewID+":"+key)
	if budget := jobexec.MaxJobNameLength - len(jobNamePrefix) - len(suffix); len(name) > budget {
		name = strings.Trim(name[:budget], "-")
	}
	return jobNamePrefix + name + suffix
}

// mergeScript builds the prospective squash merge, gates it with `erun build`,
// and pushes only on green. The EXIT trap is installed as soon as the source
// commit is known, so both markers are the last thing the Job ever prints,
// whether it got through the whole gate or failed at any step after that point.
func mergeScript(params MergeJobParams) string {
	var script strings.Builder
	script.WriteString("set -eu\n")
	script.WriteString("cd " + shellQuote(params.RepoPath) + "\n")
	script.WriteString("mergeCommit=\"\"\n")
	script.WriteString("git fetch origin " + shellQuote(params.TargetBranch) + " " + shellQuote(params.SourceBranch) + "\n")
	script.WriteString("sourceCommit=\"$(git rev-parse " + shellQuote("origin/"+params.SourceBranch) + ")\"\n")
	script.WriteString("trap 'printf \"" + sourceCommitMarker + "%s\\n\" \"$sourceCommit\"; " +
		"if [ -n \"$mergeCommit\" ]; then printf \"" + mergeCommitMarker + "%s\\n\" \"$mergeCommit\"; fi' EXIT\n")
	script.WriteString("git checkout -B " + shellQuote(params.TargetBranch) + " " + shellQuote("origin/"+params.TargetBranch) + "\n")
	script.WriteString("git merge --squash " + shellQuote("origin/"+params.SourceBranch) + "\n")
	script.WriteString("git commit -m " + shellQuote(params.MergeMessage) + "\n")
	script.WriteString("mergeCommit=\"$(git rev-parse HEAD)\"\n")
	script.WriteString("erun build\n")
	script.WriteString("git push origin " + shellQuote("HEAD:"+params.TargetBranch) + "\n")
	return script.String()
}

// shellQuote wraps a value for the Job's `sh -c` script. Branch names, review
// names, and paths are operator- or author-authored, so they are quoted rather
// than trusted.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func buildMergeJob(params MergeJobParams) *batchv1.Job {
	backoff := jobBackoffLimit
	deadline := jobActiveDeadlineSeconds
	ttl := jobTTLSecondsAfterFinished
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      MergeJobName(params.Tenant, params.TargetBranch, params.ReviewID, params.Key),
			Namespace: params.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": ManagedByLabel,
				"erun.io/tenant":               jobexec.SanitizeName(params.Tenant),
				"erun.io/review":               jobexec.SanitizeName(params.ReviewID),
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
					Volumes:            workspaceVolumes(params),
					Containers: []corev1.Container{{
						Name:         mergeContainerName,
						Image:        params.Image,
						Command:      []string{"sh", "-c", mergeScript(params)},
						VolumeMounts: workspaceMounts(params),
						Env: []corev1.EnvVar{
							{Name: "ERUN_TENANT", Value: params.Tenant},
							{Name: "ERUN_REPO_PATH", Value: params.RepoPath},
						},
					}},
				},
			},
		},
	}
}

// ManagedByLabel marks the Jobs this executor owns.
const ManagedByLabel = "erun-merge-executor"

func workspaceVolumes(params MergeJobParams) []corev1.Volume {
	var volumes []corev1.Volume
	if params.HomeClaim != "" {
		volumes = append(volumes, corev1.Volume{
			Name:         "erun-home",
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: params.HomeClaim}},
		})
	}
	if params.WorkspaceClaim != "" && params.RepoPath != "" {
		volumes = append(volumes, corev1.Volume{
			Name:         "repo-worktree",
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: params.WorkspaceClaim}},
		})
	}
	return volumes
}

func workspaceMounts(params MergeJobParams) []corev1.VolumeMount {
	var mounts []corev1.VolumeMount
	if params.HomeClaim != "" {
		mounts = append(mounts, corev1.VolumeMount{Name: "erun-home", MountPath: "/home/erun"})
	}
	if params.WorkspaceClaim != "" && params.RepoPath != "" {
		mounts = append(mounts, corev1.VolumeMount{Name: "repo-worktree", MountPath: params.RepoPath})
	}
	return mounts
}

// Launcher creates and watches merge-gate Jobs.
type Launcher struct {
	runner *jobexec.Runner
}

func NewLauncher(kube kubernetes.Interface) *Launcher {
	return &Launcher{runner: jobexec.NewRunner(kube, jobexec.Options{
		Kind:      "merge",
		Container: mergeContainerName,
		// The result is the commit the Job merged and pushed, so the output has
		// to be pulled back while the pod is still there — on success and, since
		// the trap prints unconditionally, on failure too (jobexec's own failure
		// detail is drawn from the same pod log).
		CaptureOutput: true,
	})}
}

// PollEvery sets how often the watch re-reads the Job's status.
func (l *Launcher) PollEvery(every time.Duration) { l.runner.PollEvery = every }

// Run creates the merge-gate Job and blocks until it reaches a terminal state.
func (l *Launcher) Run(ctx context.Context, params MergeJobParams) (Result, error) {
	result, err := l.runner.Run(ctx, buildMergeJob(params))
	if err != nil {
		return Result{}, err
	}
	// On success the markers are in Output; on failure jobexec's own failure
	// detail is a filtered tail of the same pod log, which still carries them
	// since the trap prints them last regardless of outcome.
	log := result.Output
	if log == "" {
		log = result.Failure
	}
	return Result{
		Outcome:      result.Outcome,
		MergeCommit:  lastMarker(log, mergeCommitMarker),
		SourceCommit: lastMarker(log, sourceCommitMarker),
		Failure:      result.Failure,
	}, nil
}

// lastMarker picks the last matching marker line, so a value echoed earlier in
// the run's own trace cannot be mistaken for the one the trap reported.
func lastMarker(output, marker string) string {
	value := ""
	for _, line := range strings.Split(output, "\n") {
		if rest, found := strings.CutPrefix(strings.TrimSpace(line), marker); found {
			value = strings.TrimSpace(rest)
		}
	}
	return value
}
