package deployexec

import (
	"context"
	"strings"
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
	assertLifecycleBootstrapScript(t, "stop", pod.Containers[0].Command, "'erun' 'stop' 'acme' 'prod'")
}

func TestBuildDeleteJobSpec(t *testing.T) {
	job := buildDeleteJob(testDeleteParams())

	if job.Name != "erun-delete-acme-prod" {
		t.Fatalf("name = %q, want erun-delete-acme-prod", job.Name)
	}
	pod := job.Spec.Template.Spec
	// -y skips the CLI's interactive confirmation: the Job has no terminal to
	// answer a prompt on.
	assertLifecycleBootstrapScript(t, "delete", pod.Containers[0].Command, "'erun' 'delete' 'acme' 'prod' '-y'")
}

// assertLifecycleBootstrapScript checks a lifecycle Job's command seeds the
// in-cluster kubeconfig and the environment's config before running the real
// command — the same prelude assertDeployBootstrapScript checks for deploy,
// since #1077 was exactly this prelude missing from stop and delete.
func assertLifecycleBootstrapScript(t *testing.T, name string, command []string, wantCommand string) {
	t.Helper()
	assertCommand(t, command[:2], []string{"sh", "-c"})
	if len(command) != 3 {
		t.Fatalf("%s command = %v, want sh -c '<script>'", name, command)
	}
	script := command[2]
	for _, want := range []string{
		"$HOME/.kube/config",
		"name: in-cluster",
		"$HOME/.config/erun/acme/config.yaml",
		"$HOME/.config/erun/acme/prod/config.yaml",
		"kubernetescontext: in-cluster",
		wantCommand,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("%s script %q missing %q", name, script, want)
		}
	}
}

// TestAllLifecycleJobsShareTheBootstrapPrelude is the structural regression
// test for #1077: every Job builder's command must be `sh -c` carrying the
// bootstrap prelude, checked generically over the builders rather than one
// hand-written assertion per Job, so a lifecycle Job added later cannot ship
// without it.
func TestAllLifecycleJobsShareTheBootstrapPrelude(t *testing.T) {
	builders := []struct {
		name    string
		command []string
	}{
		{"deploy", buildDeployJob(testParams()).Spec.Template.Spec.Containers[0].Command},
		{"stop", buildStopJob(testStopParams()).Spec.Template.Spec.Containers[0].Command},
		{"delete", buildDeleteJob(testDeleteParams()).Spec.Template.Spec.Containers[0].Command},
	}
	for _, b := range builders {
		assertCommand(t, b.command[:2], []string{"sh", "-c"})
		if len(b.command) != 3 {
			t.Fatalf("%s command = %v, want sh -c '<script>'", b.name, b.command)
		}
		script := b.command[2]
		if !strings.Contains(script, "$HOME/.kube/config") || !strings.Contains(script, "$HOME/.config/erun/acme/prod/config.yaml") {
			t.Fatalf("%s script %q missing the bootstrap prelude", b.name, script)
		}
		// The seed script carries no version: nothing it seeds a config for
		// reads RuntimeVersion back, so it stays out to avoid a second,
		// unused place to keep in sync.
		if strings.Contains(script, "runtimeversion") {
			t.Fatalf("%s script %q should not seed a runtime version", b.name, script)
		}
	}
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
