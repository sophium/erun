package jobexec

import (
	"context"
	"fmt"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const (
	testNamespace = "acme-platform"
	testJobName   = "erun-deploy-acme-prod-1-0-149"
)

func testJob(status batchv1.JobStatus) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: testJobName, Namespace: testNamespace},
		Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: testContainerName, Image: "ghcr.io/sophium/acme-devops:1.0.149"}},
		}}},
		Status: status,
	}
}

func testRunner(kube *fake.Clientset, captureOutput bool) *Runner {
	runner := NewRunner(kube, Options{Kind: "deploy", Container: testContainerName, CaptureOutput: captureOutput})
	runner.PollEvery = 0
	return runner
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

func TestRunWatchesToSuccess(t *testing.T) {
	kube := fake.NewSimpleClientset(testJob(batchv1.JobStatus{Succeeded: 1}))
	result, err := testRunner(kube, false).Run(context.Background(), testJob(batchv1.JobStatus{}))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Outcome != OutcomeSucceeded {
		t.Fatalf("outcome = %q, want succeeded", result.Outcome)
	}
	if result.Failure != "" {
		t.Fatalf("a succeeded run reported a failure: %q", result.Failure)
	}
	if result.Output != "" {
		t.Fatalf("output = %q, want empty when capture is off", result.Output)
	}
}

func TestRunCapturesOutputOnSuccess(t *testing.T) {
	job := testJob(batchv1.JobStatus{Succeeded: 1})
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:      job.Name + "-abcde",
		Namespace: testNamespace,
		Labels:    map[string]string{"job-name": job.Name},
	}}
	kube := fake.NewSimpleClientset(job, pod)
	result, err := testRunner(kube, true).Run(context.Background(), testJob(batchv1.JobStatus{}))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Output == "" {
		t.Fatal("output is empty, so a run whose result is what it printed has nothing to hand back")
	}
}

// TestRunWatchesToFailure asserts only that the log was read; the fake's body is fixed.
func TestRunWatchesToFailure(t *testing.T) {
	job := testJob(batchv1.JobStatus{Failed: 1})
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:      job.Name + "-abcde",
		Namespace: testNamespace,
		Labels:    map[string]string{"job-name": job.Name},
	}}
	kube := fake.NewSimpleClientset(job, pod)
	result, err := testRunner(kube, false).Run(context.Background(), testJob(batchv1.JobStatus{}))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Outcome != OutcomeFailed {
		t.Fatalf("outcome = %q, want failed", result.Outcome)
	}
	if result.Failure == "" {
		t.Fatal("a failed run reported no reason, so the caller would record a bare job exit")
	}
}

func TestRunReportsTheJobConditionWhenThePodIsGone(t *testing.T) {
	kube := fake.NewSimpleClientset(testJob(batchv1.JobStatus{
		Failed: 1,
		Conditions: []batchv1.JobCondition{{
			Type:    batchv1.JobFailed,
			Status:  corev1.ConditionTrue,
			Reason:  "DeadlineExceeded",
			Message: "Job was active longer than specified deadline",
		}},
	}))
	result, err := testRunner(kube, false).Run(context.Background(), testJob(batchv1.JobStatus{}))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(result.Failure, "DeadlineExceeded") {
		t.Fatalf("failure = %q, want the job's terminal condition", result.Failure)
	}
}

func admitCreatedJob(tracker k8stesting.ObjectTracker, action k8stesting.Action) (*batchv1.Job, *batchv1.Job, error) {
	create, ok := action.(k8stesting.CreateAction)
	if !ok {
		return nil, nil, fmt.Errorf("create action is %T", action)
	}
	job, ok := create.GetObject().(*batchv1.Job)
	if !ok {
		return nil, nil, fmt.Errorf("created object is %T, not a Job", create.GetObject())
	}
	created := job.DeepCopy()
	admitted := created.DeepCopy()
	admitted.Status.Active = 1
	if err := tracker.Add(admitted); err != nil {
		return nil, nil, err
	}
	return created, admitted, nil
}

// TestRunCreatesWhenAbsent drives the Job controller off client calls, so it
// has no timing assumption.
func TestRunCreatesWhenAbsent(t *testing.T) {
	kube := fake.NewSimpleClientset()

	jobsResource := batchv1.SchemeGroupVersion.WithResource("jobs")
	var created *batchv1.Job
	kube.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		job, admitted, err := admitCreatedJob(kube.Tracker(), action)
		if err != nil {
			return true, nil, err
		}
		created = job
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

	result, err := testRunner(kube, false).Run(context.Background(), testJob(batchv1.JobStatus{}))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Outcome != OutcomeSucceeded {
		t.Fatalf("outcome = %q, want succeeded", result.Outcome)
	}
	if created == nil {
		t.Fatal("job was not created")
	}
	if created.Name != testJobName || created.Namespace != testNamespace {
		t.Fatalf("created job = %s/%s, want %s/%s", created.Namespace, created.Name, testNamespace, testJobName)
	}
	if polls < 2 {
		t.Fatalf("polls = %d, want Run to poll past the still-active Job", polls)
	}
}

func TestRunToleratesAnExistingJob(t *testing.T) {
	kube := fake.NewSimpleClientset(testJob(batchv1.JobStatus{Succeeded: 1}))
	result, err := testRunner(kube, false).Run(context.Background(), testJob(batchv1.JobStatus{}))
	if err != nil {
		t.Fatalf("run over an existing job: %v", err)
	}
	if result.Outcome != OutcomeSucceeded {
		t.Fatalf("outcome = %q, want the existing job's outcome", result.Outcome)
	}
}

func TestSanitizeNameAndShortID(t *testing.T) {
	if got := SanitizeName("Acme/Main_1.0.149"); got != "acme-main-1-0-149" {
		t.Fatalf("SanitizeName = %q", got)
	}
	if got := SanitizeName("--trimmed--"); got != "trimmed" {
		t.Fatalf("SanitizeName left surrounding hyphens: %q", got)
	}
	if got := ShortID("3f2a91cc-1111-2222-3333-444455556666"); len(got) != shortIDLength {
		t.Fatalf("ShortID = %q, want %d characters", got, shortIDLength)
	}
}

// TestShortIDSeparatesIDsThatShareAPrefix: UUIDv7 prefixes are timestamps.
func TestShortIDSeparatesIDsThatShareAPrefix(t *testing.T) {
	first := ShortID("01a00b41-0ce6-781d-8525-eb44c54da079")
	second := ShortID("01a00b41-0cfb-75bc-9b97-51ceb20a1752")
	if first == second {
		t.Fatalf("two ids minted in the same millisecond share the short id %q", first)
	}
	if ShortID("aaaaaaaa-1111-2222-3333-444455556666") == ShortID("bbbbbbbb-1111-2222-3333-444455556666") {
		t.Fatal("two ids sharing a suffix share the short id")
	}
	const resumed = "01a00b41-0ce6-781d-8525-eb44c54da079"
	if ShortID(resumed) != first {
		t.Fatal("ShortID is not deterministic, so a resumed workflow would not find its own job")
	}
}
