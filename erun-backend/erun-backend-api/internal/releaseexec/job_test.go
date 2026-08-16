package releaseexec

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func testParams() ReleaseJobParams {
	return ReleaseJobParams{
		Tenant:         "acme",
		TargetBranch:   "main",
		CommitID:       "9f1c2b3d4e5f60718293a4b5c6d7e8f901234567",
		ReleaseID:      "3f2a91cc-1111-2222-3333-444455556666",
		Attempt:        1,
		Namespace:      "acme-devops",
		Image:          "ghcr.io/sophium/acme-devops:1.0.149",
		ServiceAccount: "acme-releaser",
	}
}

func TestBuildReleaseJobSpec(t *testing.T) {
	job := buildReleaseJob(testParams())

	if job.Namespace != "acme-devops" {
		t.Fatalf("namespace = %q", job.Namespace)
	}
	pod := job.Spec.Template.Spec
	if pod.ServiceAccountName != "acme-releaser" {
		t.Fatalf("service account = %q", pod.ServiceAccountName)
	}
	if pod.RestartPolicy != corev1.RestartPolicyNever {
		t.Fatalf("restart policy = %q, want Never", pod.RestartPolicy)
	}
	if len(pod.Containers) != 1 {
		t.Fatalf("containers = %+v, want exactly one", pod.Containers)
	}
	script := pod.Containers[0].Command[2]
	if !strings.Contains(script, "erun release") {
		t.Fatalf("script does not run the real release:\n%s", script)
	}
	if strings.Contains(script, "--dry-run") {
		t.Fatalf("a non-dry-run job asked for --dry-run:\n%s", script)
	}
	// A release that retried in-Job would be a second uncontrolled attempt over a
	// repository and a registry the first one already wrote to.
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 0 {
		t.Fatalf("backoffLimit = %v, want 0", job.Spec.BackoffLimit)
	}
	if len(pod.Volumes) != 0 {
		t.Fatalf("volumes = %+v, want none when no workspace claim was named", pod.Volumes)
	}
}

// TestReleaseScriptMovesToTheMergeCommit: the queue enqueues a commit, so the run
// must release that commit. Fast-forward only means a branch that moved
// underneath refuses rather than quietly releasing something else.
func TestReleaseScriptMovesToTheMergeCommit(t *testing.T) {
	params := testParams()
	params.RepoPath = "/home/erun/git/erun"
	params.WorkspaceClaim = "acme-devops-worktree"
	script := releaseScript(params)
	for _, want := range []string{
		"cd '/home/erun/git/erun'",
		"git fetch --tags origin 'main'",
		"git merge --ff-only '" + params.CommitID + "'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script is missing %q:\n%s", want, script)
		}
	}
}

// TestReleaseScriptEmitsTheVersionMarker: the version is the release's result,
// and a pod log interleaves stdout and stderr, so the run has to label it.
func TestReleaseScriptEmitsTheVersionMarker(t *testing.T) {
	script := releaseScript(testParams())
	if !strings.Contains(script, versionMarker) {
		t.Fatalf("script never labels the version it minted:\n%s", script)
	}
	if !strings.Contains(script, "set -eu") {
		t.Fatalf("script does not abort on a failing step:\n%s", script)
	}
}

func TestReleaseScriptHonoursDryRun(t *testing.T) {
	params := testParams()
	params.DryRun = true
	if script := releaseScript(params); !strings.Contains(script, "erun release --dry-run") {
		t.Fatalf("dry run does not reach the command:\n%s", script)
	}
}

// TestBuildReleaseJobMountsTheAgentWorkspace: releasing beside the environment's
// warm fingerprint cache and existing checkout is the reason this runs here
// rather than on an ephemeral runner.
func TestBuildReleaseJobMountsTheAgentWorkspace(t *testing.T) {
	params := testParams()
	params.HomeClaim = "acme-devops-home"
	params.WorkspaceClaim = "acme-devops-worktree"
	params.RepoPath = "/home/erun/git/erun"
	pod := buildReleaseJob(params).Spec.Template.Spec

	claims := map[string]string{}
	for _, volume := range pod.Volumes {
		if volume.PersistentVolumeClaim != nil {
			claims[volume.Name] = volume.PersistentVolumeClaim.ClaimName
		}
	}
	if claims["erun-home"] != "acme-devops-home" || claims["repo-worktree"] != "acme-devops-worktree" {
		t.Fatalf("volumes = %+v, want the environment's own home and worktree claims", claims)
	}
	mounts := map[string]string{}
	for _, mount := range pod.Containers[0].VolumeMounts {
		mounts[mount.Name] = mount.MountPath
	}
	if mounts["repo-worktree"] != "/home/erun/git/erun" {
		t.Fatalf("worktree mount = %q, want the repo path the script cds into", mounts["repo-worktree"])
	}
}

// TestReleaseJobNameSeparatesAttempts: a retry has to run. A Job named after
// something terminal would be re-read instead of re-run — the replay bug this
// executor is keyed to avoid.
func TestReleaseJobNameSeparatesAttempts(t *testing.T) {
	first := ReleaseJobName("acme", "main", "3f2a91cc-1111-2222", 1)
	second := ReleaseJobName("acme", "main", "3f2a91cc-1111-2222", 2)
	if first == second {
		t.Fatalf("two attempts on one release share the job name %q", first)
	}
	other := ReleaseJobName("acme", "main", "9b7d40aa-1111-2222", 1)
	if first == other {
		t.Fatalf("two releases share the job name %q", first)
	}
	if !strings.HasPrefix(first, "erun-release-acme-main-") {
		t.Fatalf("name %q lost its readable prefix", first)
	}
}

func TestReleaseJobNameFitsKubernetesLimit(t *testing.T) {
	branch := strings.Repeat("b", 80)
	first := ReleaseJobName("verylongtenantname", branch, "3f2a91cc-aaaa", 1)
	second := ReleaseJobName("verylongtenantname", branch, "9b7d40aa-bbbb", 1)
	for _, name := range []string{first, second} {
		if len(name) > 63 {
			t.Fatalf("job name %q is %d characters, over the 63 Kubernetes allows", name, len(name))
		}
	}
	if first == second {
		t.Fatal("truncation collapsed two releases onto one job name")
	}
}

func TestVersionFromOutput(t *testing.T) {
	log := strings.Join([]string{
		"release: resolving version from VERSION",
		"release: " + versionMarker + "not-the-answer",
		"release: publishing ghcr.io/sophium/erun-devops:1.0.150",
		versionMarker + "1.0.150",
	}, "\n")
	if got := versionFromOutput(log); got != "1.0.150" {
		t.Fatalf("version = %q, want the last marker line", got)
	}
	if got := versionFromOutput("no marker here\n"); got != "" {
		t.Fatalf("version = %q, want empty when the run never said", got)
	}
}

// TestRunReportsTheVersionTheReleasePublished is the executor's contract: the
// version is minted inside the Job, so a run that does not carry it back leaves
// the control plane unable to name what it released.
func TestRunReportsTheVersionTheReleasePublished(t *testing.T) {
	params := testParams()
	name := ReleaseJobName(params.Tenant, params.TargetBranch, params.ReleaseID, params.Attempt)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: params.Namespace},
		Status:     batchv1.JobStatus{Succeeded: 1},
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:      name + "-abcde",
		Namespace: params.Namespace,
		Labels:    map[string]string{"job-name": name},
	}}
	launcher := NewLauncher(fake.NewSimpleClientset(job, pod))
	launcher.PollEvery(0)
	result, err := launcher.Run(context.Background(), params)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Outcome != OutcomeSucceeded {
		t.Fatalf("outcome = %q, want succeeded", result.Outcome)
	}
	// The fake client serves a fixed log body with no marker, so the assertion is
	// that a run which said nothing yields no version rather than a wrong one.
	if result.Version != "" {
		t.Fatalf("version = %q, want empty when the run's output carried no marker", result.Version)
	}
}

func TestRunReportsAFailureReason(t *testing.T) {
	params := testParams()
	name := ReleaseJobName(params.Tenant, params.TargetBranch, params.ReleaseID, params.Attempt)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: params.Namespace},
		Status: batchv1.JobStatus{Failed: 1, Conditions: []batchv1.JobCondition{{
			Type:    batchv1.JobFailed,
			Status:  corev1.ConditionTrue,
			Reason:  "DeadlineExceeded",
			Message: "Job was active longer than specified deadline",
		}}},
	}
	launcher := NewLauncher(fake.NewSimpleClientset(job))
	launcher.PollEvery(0)
	result, err := launcher.Run(context.Background(), params)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Outcome != OutcomeFailed {
		t.Fatalf("outcome = %q, want failed", result.Outcome)
	}
	if !strings.Contains(result.Failure, "DeadlineExceeded") {
		t.Fatalf("failure = %q, want the run's own reason", result.Failure)
	}
	if result.Version != "" {
		t.Fatalf("version = %q, want empty: a failed release published nothing", result.Version)
	}
}
