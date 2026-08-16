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

// TestRunWatchesTheDeployJob holds the launcher to creating and watching the Job
// its params describe; the watch and failure read-back themselves belong to
// jobexec and are covered there.
func TestRunWatchesTheDeployJob(t *testing.T) {
	p := testParams()
	kube := fake.NewSimpleClientset(&batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DeployJobName(p.Tenant, p.Environment, p.Version, p.DeployID),
			Namespace: p.Namespace,
		},
		Status: batchv1.JobStatus{Succeeded: 1},
	})
	launcher := NewLauncher(kube)
	launcher.PollEvery(0)
	result, err := launcher.Run(context.Background(), p)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Outcome != OutcomeSucceeded {
		t.Fatalf("outcome = %q, want succeeded", result.Outcome)
	}
}
