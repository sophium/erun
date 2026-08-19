package deployexec

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func testStopParams() StopJobParams {
	return StopJobParams{
		Tenant:         "acme",
		Environment:    "prod",
		Namespace:      "acme-platform",
		Image:          "ghcr.io/sophium/acme-devops:1.0.149",
		ServiceAccount: "acme-api-deployer",
	}
}

func testDeleteParams() DeleteJobParams {
	return DeleteJobParams{
		Tenant:         "acme",
		Environment:    "prod",
		Namespace:      "acme-platform",
		Image:          "ghcr.io/sophium/acme-devops:1.0.149",
		ServiceAccount: "acme-api-deployer",
	}
}

func TestBuildStopJobSpec(t *testing.T) {
	job := buildStopJob(testStopParams())

	if job.Namespace != "acme-platform" {
		t.Fatalf("namespace = %q", job.Namespace)
	}
	if job.Name != "erun-stop-acme-prod" {
		t.Fatalf("name = %q, want erun-stop-acme-prod", job.Name)
	}
	pod := job.Spec.Template.Spec
	if pod.ServiceAccountName != "acme-api-deployer" {
		t.Fatalf("service account = %q", pod.ServiceAccountName)
	}
	if pod.RestartPolicy != corev1.RestartPolicyNever {
		t.Fatalf("restart policy = %q, want Never", pod.RestartPolicy)
	}
	assertCommand(t, pod.Containers[0].Command, []string{"erun", "stop", "acme", "prod"})
}

func TestBuildDeleteJobSpec(t *testing.T) {
	job := buildDeleteJob(testDeleteParams())

	if job.Name != "erun-delete-acme-prod" {
		t.Fatalf("name = %q, want erun-delete-acme-prod", job.Name)
	}
	pod := job.Spec.Template.Spec
	// -y skips the CLI's interactive confirmation: the Job has no terminal to
	// answer a prompt on.
	assertCommand(t, pod.Containers[0].Command, []string{"erun", "delete", "acme", "prod", "-y"})
}

// TestLauncherRunsStopAndDelete holds the launcher to creating and watching
// each lifecycle Job under its own Kind/Container; the watch and
// failure-read-back machinery itself is jobexec's, covered there.
func TestLauncherRunsStopAndDelete(t *testing.T) {
	stopParams := testStopParams()
	deleteParams := testDeleteParams()
	kube := fake.NewSimpleClientset(
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: StopJobName(stopParams.Tenant, stopParams.Environment), Namespace: stopParams.Namespace},
			Status:     batchv1.JobStatus{Succeeded: 1},
		},
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: DeleteJobName(deleteParams.Tenant, deleteParams.Environment), Namespace: deleteParams.Namespace},
			Status:     batchv1.JobStatus{Succeeded: 1},
		},
	)
	launcher := NewLauncher(kube)
	launcher.PollEvery(0)

	stopResult, err := launcher.RunStop(context.Background(), stopParams)
	if err != nil {
		t.Fatalf("run stop: %v", err)
	}
	if stopResult.Outcome != OutcomeSucceeded {
		t.Fatalf("stop outcome = %q, want succeeded", stopResult.Outcome)
	}

	deleteResult, err := launcher.RunDelete(context.Background(), deleteParams)
	if err != nil {
		t.Fatalf("run delete: %v", err)
	}
	if deleteResult.Outcome != OutcomeSucceeded {
		t.Fatalf("delete outcome = %q, want succeeded", deleteResult.Outcome)
	}
}

func TestLifecycleJobNameSanitizesAndTruncates(t *testing.T) {
	long := "a-very-long-tenant-name-that-pushes-past-the-kubernetes-object-name-limit"
	name := StopJobName(long, "prod")
	if len(name) > 63 {
		t.Fatalf("job name length = %d, want <= 63", len(name))
	}
	deleteName := DeleteJobName(long, "prod")
	if len(deleteName) > 63 {
		t.Fatalf("job name length = %d, want <= 63", len(deleteName))
	}
}
