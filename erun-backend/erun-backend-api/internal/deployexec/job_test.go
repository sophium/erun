package deployexec

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
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
	got := pod.Containers[0].Command
	want := []string{"erun", "deploy", "acme", "prod", "--version", "1.0.149"}
	if len(got) != len(want) {
		t.Fatalf("command = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("command[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// No in-Job retries: a failed deploy must surface, not silently retry.
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 0 {
		t.Fatalf("backoffLimit = %v, want 0", job.Spec.BackoffLimit)
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

// seededJob is the deploy Job as it would be after the cluster ran it, with a
// terminal status, so Run's watch returns immediately (the fake client has no
// Job controller to move status on its own).
func seededJob(status batchv1.JobStatus) *batchv1.Job {
	p := testParams()
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: DeployJobName(p.Tenant, p.Environment, p.Version), Namespace: p.Namespace},
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
// exist; a status-updater goroutine simulates the cluster completing it.
func TestRunCreatesWhenAbsent(t *testing.T) {
	kube := fake.NewSimpleClientset()
	launcher := NewLauncher(kube)
	launcher.pollEvery = 0 // don't wait between polls in the test

	p := testParams()
	done := make(chan struct{})
	go func() {
		defer close(done)
		outcome, err := launcher.Run(context.Background(), p)
		if err != nil || outcome != OutcomeSucceeded {
			t.Errorf("run = (%q, %v), want succeeded", outcome, err)
		}
	}()

	// Wait for Run to create the Job, then drive it to Succeeded.
	name := DeployJobName(p.Tenant, p.Environment, p.Version)
	waitForJob(t, kube, p.Namespace, name)
	job := seededJob(batchv1.JobStatus{Succeeded: 1})
	if _, err := kube.BatchV1().Jobs(p.Namespace).UpdateStatus(context.Background(), job, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update status: %v", err)
	}
	<-done
}

func waitForJob(t *testing.T, kube *fake.Clientset, namespace, name string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if _, err := kube.BatchV1().Jobs(namespace).Get(context.Background(), name, metav1.GetOptions{}); err == nil {
			return
		}
	}
	t.Fatalf("deploy job %s was not created", name)
}
