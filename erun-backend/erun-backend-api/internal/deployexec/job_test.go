package deployexec

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func testParams() DeployJobParams {
	return DeployJobParams{
		Tenant:         "acme",
		Environment:    "prod",
		Version:        "1.0.149",
		Namespace:      "acme-platform",
		Image:          "ghcr.io/sophium/acme-devops:1.0.149",
		ServiceAccount: "acme-api-deployer",
	}
}

func TestBuildDeployJobSpec(t *testing.T) {
	job := buildDeployJob(testParams())

	if job.Namespace != "acme-platform" {
		t.Fatalf("namespace = %q", job.Namespace)
	}
	if job.Name != "erun-deploy-acme-prod-1-0-149" {
		t.Fatalf("name = %q, want erun-deploy-acme-prod-1-0-149 (dots sanitized)", job.Name)
	}
	pod := job.Spec.Template.Spec
	if pod.ServiceAccountName != "acme-api-deployer" {
		t.Fatalf("service account = %q", pod.ServiceAccountName)
	}
	if pod.RestartPolicy != corev1.RestartPolicyNever {
		t.Fatalf("restart policy = %q, want Never", pod.RestartPolicy)
	}
	if len(pod.Containers) != 1 || pod.Containers[0].Image != "ghcr.io/sophium/acme-devops:1.0.149" {
		t.Fatalf("container image = %+v", pod.Containers)
	}
	assertCommand(t, pod.Containers[0].Command, []string{"erun", "deploy", "acme", "prod", "--version", "1.0.149"})
	// No in-Job retries: a failed deploy must surface, not silently retry.
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 0 {
		t.Fatalf("backoffLimit = %v, want 0", job.Spec.BackoffLimit)
	}
}

// assertCommand compares the deploy argv element by element, so a mismatch names
// the position that differs.
func assertCommand(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("command = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("command[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestJobOutcome(t *testing.T) {
	cases := []struct {
		name    string
		status  batchv1.JobStatus
		outcome Outcome
		done    bool
	}{
		{"succeeded", batchv1.JobStatus{Succeeded: 1}, OutcomeSucceeded, true},
		{"failed", batchv1.JobStatus{Failed: 1}, OutcomeFailed, true},
		{"running", batchv1.JobStatus{Active: 1}, "", false},
		{"pending", batchv1.JobStatus{}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outcome, done := jobOutcome(&batchv1.Job{Status: tc.status})
			if done != tc.done || outcome != tc.outcome {
				t.Fatalf("jobOutcome = (%q,%v), want (%q,%v)", outcome, done, tc.outcome, tc.done)
			}
		})
	}
}

// TestDeployJobNameSeparatesAttempts: a re-deploy must be a new Job, otherwise
// it would re-read the previous attempt's terminal outcome instead of running.
func TestDeployJobNameSeparatesAttempts(t *testing.T) {
	first := DeployJobName("acme", "prod", "1.0.149", "3f2a91cc-1111-2222-3333-444455556666")
	second := DeployJobName("acme", "prod", "1.0.149", "9b7d40aa-1111-2222-3333-444455556666")
	if first == second {
		t.Fatalf("two attempts share the job name %q", first)
	}
	// The create path passes no attempt id and keeps its stable name, so a
	// resumed workflow still re-watches the Job it already created.
	if got := DeployJobName("acme", "prod", "1.0.149", ""); got != "erun-deploy-acme-prod-1-0-149" {
		t.Fatalf("name without an attempt id = %q", got)
	}
	if !strings.HasPrefix(first, "erun-deploy-acme-prod-1-0-149-") {
		t.Fatalf("attempt name %q lost its readable prefix", first)
	}
}

// TestDeployJobNameFitsKubernetesLimit: names are trimmed in the descriptive
// middle, never in the attempt suffix that keeps two deploys apart.
func TestDeployJobNameFitsKubernetesLimit(t *testing.T) {
	longEnv := strings.Repeat("e", 63)
	first := DeployJobName("verylongtenantname", longEnv, "1.0.149-rc.20260816", "3f2a91cc-aaaa")
	second := DeployJobName("verylongtenantname", longEnv, "1.0.149-rc.20260816", "9b7d40aa-bbbb")
	for _, name := range []string{first, second} {
		if len(name) > 63 {
			t.Fatalf("job name %q is %d characters, over the 63 Kubernetes allows", name, len(name))
		}
	}
	if first == second {
		t.Fatal("truncation collapsed two attempts onto one job name")
	}
}

// seededJob is the deploy Job as it would be after the cluster ran it, with a
// terminal status, so Run's watch returns immediately (the fake client has no
// Job controller to move status on its own).
func seededJob(status batchv1.JobStatus) *batchv1.Job {
	p := testParams()
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: DeployJobName(p.Tenant, p.Environment, p.Version, p.DeployID), Namespace: p.Namespace},
		Status:     status,
	}
}

func TestRunWatchesToSuccess(t *testing.T) {
	kube := fake.NewSimpleClientset(seededJob(batchv1.JobStatus{Succeeded: 1}))
	outcome, err := NewLauncher(kube).Run(context.Background(), testParams())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if outcome != OutcomeSucceeded {
		t.Fatalf("outcome = %q, want succeeded", outcome)
	}
}

func TestRunWatchesToFailure(t *testing.T) {
	kube := fake.NewSimpleClientset(seededJob(batchv1.JobStatus{Failed: 1}))
	outcome, err := NewLauncher(kube).Run(context.Background(), testParams())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if outcome != OutcomeFailed {
		t.Fatalf("outcome = %q, want failed", outcome)
	}
}

// TestRunCreatesWhenAbsent proves the Job is actually created when it doesn't
// exist, and that Run keeps polling until it turns terminal. The reactions below
// stand in for the cluster's Job controller: the Job is admitted Active and only
// completes once Run has already observed it running. Driving the transition off
// the client calls, rather than off a status update racing the watch, keeps the
// test free of any timing assumption.
func TestRunCreatesWhenAbsent(t *testing.T) {
	kube := fake.NewSimpleClientset()
	launcher := NewLauncher(kube)
	launcher.pollEvery = 0 // the reactions advance the Job, so nothing waits on the clock

	jobsResource := batchv1.SchemeGroupVersion.WithResource("jobs")
	var created *batchv1.Job
	kube.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		created = action.(k8stesting.CreateAction).GetObject().(*batchv1.Job).DeepCopy()
		admitted := created.DeepCopy()
		admitted.Status.Active = 1
		if err := kube.Tracker().Add(admitted); err != nil {
			return true, nil, err
		}
		return true, admitted, nil
	})
	polls := 0
	kube.PrependReactor("get", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		polls++
		if polls > 1 {
			completed := created.DeepCopy()
			completed.Status.Succeeded = 1
			if err := kube.Tracker().Update(jobsResource, completed, completed.Namespace); err != nil {
				return true, nil, err
			}
		}
		return false, nil, nil
	})

	p := testParams()
	outcome, err := launcher.Run(context.Background(), p)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if outcome != OutcomeSucceeded {
		t.Fatalf("outcome = %q, want succeeded", outcome)
	}
	if created == nil {
		t.Fatal("deploy job was not created")
	}
	if want := DeployJobName(p.Tenant, p.Environment, p.Version, p.DeployID); created.Name != want {
		t.Fatalf("created job name = %q, want %q", created.Name, want)
	}
	if created.Namespace != p.Namespace {
		t.Fatalf("created job namespace = %q, want %q", created.Namespace, p.Namespace)
	}
	if polls < 2 {
		t.Fatalf("polls = %d, want Run to poll past the still-active Job", polls)
	}
}
