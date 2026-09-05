package deployexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
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
	assertLifecycleBootstrapScript(t, "delete", pod.Containers[0].Command, "'erun' 'delete' 'acme' 'prod' '-y' '--output' 'json'")
	if strings.Contains(pod.Containers[0].Command[2], "unexpose") {
		t.Fatalf("script %q should not chain unexpose without platform coordinates", pod.Containers[0].Command[2])
	}
}

// TestBuildDeleteJobSpecWithUnexpose: when the control plane supplies the
// platform coordinates it already knows, the delete Job chains a best-effort
// `erun unexpose` after a successful delete, so the per-env wildcard DNS
// record `erun expose` created does not outlive the namespace it pointed at.
func TestBuildDeleteJobSpecWithUnexpose(t *testing.T) {
	params := testDeleteParams()
	params.ExposeServicesZone = "services.erunpaas.com"
	params.ExposePlatformNamespace = "frs-prod"
	script := buildDeleteCommand(params)[2]
	wantDelete := "'erun' 'delete' 'acme' 'prod' '-y' '--output' 'json'"
	wantUnexpose := "'erun' 'unexpose' 'acme' 'prod' '--skip-if-unconfigured' '--services-zone' 'services.erunpaas.com' '--platform-namespace' 'frs-prod'"
	if !strings.Contains(script, wantDelete) {
		t.Fatalf("script %q missing delete stage %q", script, wantDelete)
	}
	if !strings.Contains(script, wantUnexpose) {
		t.Fatalf("script %q missing unexpose stage %q", script, wantUnexpose)
	}
	deleteIdx := strings.Index(script, wantDelete)
	unexposeIdx := strings.Index(script, wantUnexpose)
	if deleteIdx < 0 || unexposeIdx < 0 || deleteIdx > unexposeIdx {
		t.Fatalf("script %q must run delete before unexpose", script)
	}
	if !strings.Contains(script, " && ") {
		t.Fatalf("script %q must short-circuit unexpose on a failed delete", script)
	}

	// Half the pair configured is the same as neither.
	partial := testDeleteParams()
	partial.ExposeServicesZone = "services.erunpaas.com"
	partialScript := buildDeleteCommand(partial)[2]
	if strings.Contains(partialScript, "unexpose") {
		t.Fatalf("script %q should not chain unexpose from a partial platform-coordinates override", partialScript)
	}
}

// TestUnexposeChainScriptIsBestEffort mirrors TestExposeChainScriptIsBestEffort:
// the chained unexpose step must never fail the delete Job it rides on — the
// namespace already tore down successfully — and on failure prints a marker
// line UnexposeFailureFromOutput reads back out of the Job's captured output.
func TestUnexposeChainScriptIsBestEffort(t *testing.T) {
	params := testDeleteParams()
	params.ExposeServicesZone = "services.erunpaas.com"
	params.ExposePlatformNamespace = "frs-prod"
	script := buildDeleteCommand(params)[2]
	if !strings.Contains(script, "|| printf '"+unexposeFailureMarker+": %s\\n' \"$unexpose_out\"") {
		t.Fatalf("script %q must swallow a failing unexpose behind the marker", script)
	}
}

func TestUnexposeFailureFromOutput(t *testing.T) {
	output := "==> Deleted acme/prod\n" +
		"audit: erun unexpose --skip-if-unconfigured --services-zone services.erunpaas.com --platform-namespace frs-prod acme prod\n" +
		unexposeFailureMarker + ": zone services.erunpaas.com does not exist\n"
	if got := UnexposeFailureFromOutput(output); got != "zone services.erunpaas.com does not exist" {
		t.Fatalf("UnexposeFailureFromOutput = %q, want %q", got, "zone services.erunpaas.com does not exist")
	}
	if got := UnexposeFailureFromOutput("==> Deleted acme/prod\n"); got != "" {
		t.Fatalf("UnexposeFailureFromOutput = %q, want empty when no marker is present", got)
	}
}

// TestNamespaceDeleteFailureFromOutput pins the interaction #1140 is about:
// `erun delete` exits 0 (and prints "deleted") even when the remote namespace
// teardown itself failed, so the API cannot trust the Job's own exit code —
// it has to read the --output json result back out of the captured output.
// eruncommon.Context.WriteResult pretty-prints (json.MarshalIndent), and the
// Job's combined stdout+stderr log carries the plain-text audit/trace lines
// around it, so the fixture mirrors both: multi-line JSON, not a single line.
func TestNamespaceDeleteFailureFromOutput(t *testing.T) {
	blocked := `namespace "acme-prod" did not finish terminating within 20m0s:` + "\n" +
		"NamespaceContentRemaining=True     challenges.acme.cert-manager.io has 1 resource instances\n" +
		"NamespaceFinalizersRemaining=True  acme.cert-manager.io/finalizer in 1 resource instances"
	encoded, err := json.MarshalIndent(struct {
		Tenant               string `json:"tenant"`
		Environment          string `json:"environment"`
		Namespace            string `json:"namespace"`
		NamespaceDeleteError string `json:"namespaceDeleteError"`
	}{Tenant: "acme", Environment: "prod", Namespace: "acme-prod", NamespaceDeleteError: blocked}, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	output := "audit: erun delete acme prod -y --output json\n" + string(encoded) + "\n"

	if got := NamespaceDeleteFailureFromOutput(output); got != blocked {
		t.Fatalf("NamespaceDeleteFailureFromOutput = %q, want %q", got, blocked)
	}

	succeeded := `{"tenant":"acme","environment":"prod","namespace":"acme-prod"}` + "\n"
	if got := NamespaceDeleteFailureFromOutput(succeeded); got != "" {
		t.Fatalf("NamespaceDeleteFailureFromOutput = %q, want empty when the namespace was torn down", got)
	}

	if got := NamespaceDeleteFailureFromOutput("not json at all\n"); got != "" {
		t.Fatalf("NamespaceDeleteFailureFromOutput = %q, want empty when the Job predates --output json", got)
	}
}

// assertLifecycleBootstrapScript checks a lifecycle Job's command seeds the
// in-cluster kubeconfig and the environment's config before running the real
// command — the same prelude assertDeployBootstrapScript checks for deploy,
// since a missing prelude on stop and delete was exactly this kind of gap.
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
// test ensuring every Job builder's command is `sh -c` carrying the
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
// failure-read-back machinery itself is jobexec's, covered there. The delete
// side is given an explicit attempt id (as every real caller supplies one),
// so this exercises the same DeleteJobName path a live delete takes rather
// than the bare tenant+environment name.
func TestLauncherRunsStopAndDelete(t *testing.T) {
	stopParams := testStopParams()
	deleteParams := testDeleteParams()
	deleteParams.DeleteID = "3f2a91cc-1111-2222-3333-444455556666"
	kube := fake.NewSimpleClientset(
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: StopJobName(stopParams.Tenant, stopParams.Environment, stopParams.StopID), Namespace: stopParams.Namespace},
			Status:     batchv1.JobStatus{Succeeded: 1},
		},
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: DeleteJobName(deleteParams.Tenant, deleteParams.Environment, deleteParams.DeleteID), Namespace: deleteParams.Namespace},
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

// TestLauncherDeleteAttemptsGetDistinctJobs is the regression test for the
// replay this Job name once had: two delete attempts against the same
// environment must land on two distinct Jobs, and the second attempt must
// surface its own outcome rather than a still-live prior attempt's cached
// terminal result. Each attempt's Job is seeded with a different outcome
// specifically so a wrongly-shared name would be caught by the assertion on
// the second result, not just by the name comparison.
func TestLauncherDeleteAttemptsGetDistinctJobs(t *testing.T) {
	params := testDeleteParams()
	firstID := "3f2a91cc-1111-2222-3333-444455556666"
	secondID := "9b7d40aa-1111-2222-3333-444455556666"

	firstName := DeleteJobName(params.Tenant, params.Environment, firstID)
	secondName := DeleteJobName(params.Tenant, params.Environment, secondID)
	if firstName == secondName {
		t.Fatalf("two delete attempts share the job name %q", firstName)
	}

	kube := fake.NewSimpleClientset(
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: firstName, Namespace: params.Namespace},
			Status:     batchv1.JobStatus{Failed: 1},
		},
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: secondName, Namespace: params.Namespace},
			Status:     batchv1.JobStatus{Succeeded: 1},
		},
	)
	launcher := NewLauncher(kube)
	launcher.PollEvery(0)

	first := params
	first.DeleteID = firstID
	firstResult, err := launcher.RunDelete(context.Background(), first)
	if err != nil {
		t.Fatalf("run first delete: %v", err)
	}
	if firstResult.Outcome != OutcomeFailed {
		t.Fatalf("first delete outcome = %q, want failed", firstResult.Outcome)
	}

	second := params
	second.DeleteID = secondID
	secondResult, err := launcher.RunDelete(context.Background(), second)
	if err != nil {
		t.Fatalf("run second delete: %v", err)
	}
	if secondResult.Outcome != OutcomeSucceeded {
		t.Fatalf("second delete outcome = %q, want succeeded — got the first attempt's cached outcome instead of running its own Job", secondResult.Outcome)
	}

	jobs, err := kube.BatchV1().Jobs(params.Namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs.Items) != 2 {
		t.Fatalf("jobs in namespace = %d, want 2 (one per attempt)", len(jobs.Items))
	}
}

// TestDeleteJobNameSeparatesAttempts mirrors TestDeployJobNameSeparatesAttempts:
// a retried delete must be a new Job, otherwise it would re-read the previous
// attempt's terminal outcome instead of running.
func TestDeleteJobNameSeparatesAttempts(t *testing.T) {
	first := DeleteJobName("acme", "prod", "3f2a91cc-1111-2222-3333-444455556666")
	second := DeleteJobName("acme", "prod", "9b7d40aa-1111-2222-3333-444455556666")
	if first == second {
		t.Fatalf("two attempts share the job name %q", first)
	}
	// The create path passes no attempt id and keeps its stable name, so a
	// resumed workflow still re-watches the Job it already created.
	if got := DeleteJobName("acme", "prod", ""); got != "erun-delete-acme-prod" {
		t.Fatalf("name without an attempt id = %q", got)
	}
	if !strings.HasPrefix(first, "erun-delete-acme-prod-") {
		t.Fatalf("attempt name %q lost its readable prefix", first)
	}
}

// TestDeleteJobNameFitsKubernetesLimit mirrors TestDeployJobNameFitsKubernetesLimit:
// names are trimmed in the descriptive middle, never in the attempt suffix
// that keeps two deletes apart.
func TestDeleteJobNameFitsKubernetesLimit(t *testing.T) {
	longEnv := strings.Repeat("e", 63)
	first := DeleteJobName("verylongtenantname", longEnv, "3f2a91cc-aaaa")
	second := DeleteJobName("verylongtenantname", longEnv, "9b7d40aa-bbbb")
	for _, name := range []string{first, second} {
		if len(name) > 63 {
			t.Fatalf("job name %q is %d characters, over the 63 Kubernetes allows", name, len(name))
		}
	}
	if first == second {
		t.Fatal("truncation collapsed two attempts onto one job name")
	}
}

func TestLifecycleJobNameSanitizesAndTruncates(t *testing.T) {
	long := "a-very-long-tenant-name-that-pushes-past-the-kubernetes-object-name-limit"
	name := StopJobName(long, "prod", "")
	if len(name) > 63 {
		t.Fatalf("job name length = %d, want <= 63", len(name))
	}
	deleteName := DeleteJobName(long, "prod", "3f2a91cc-1111-2222-3333-444455556666")
	if len(deleteName) > 63 {
		t.Fatalf("job name length = %d, want <= 63", len(deleteName))
	}
}

// admitAndCompleteJobReactors installs the same pair of fake-clientset
// reactors TestRunCreatesWhenAbsent uses: "create" admits the Job Active
// (standing in for the cluster's own Job controller) and records what was
// created, then "get" flips it to Succeeded once it has been observed
// running at least once — driving the transition off the client calls
// themselves keeps the test free of any timing assumption. Only reacts to
// Jobs named watchName, so a pre-seeded, unrelated Job already in the fake
// clientset is left alone.
func admitAndCompleteJobReactors(t *testing.T, kube *fake.Clientset, watchName, namespace string) (created func() *batchv1.Job) {
	t.Helper()
	jobsResource := batchv1.SchemeGroupVersion.WithResource("jobs")
	var createdJob *batchv1.Job
	kube.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		obj := action.(k8stesting.CreateAction).GetObject().(*batchv1.Job).DeepCopy()
		if obj.Name != watchName {
			return false, nil, nil
		}
		createdJob = obj
		admitted := obj.DeepCopy()
		admitted.Status.Active = 1
		if err := kube.Tracker().Add(admitted); err != nil {
			return true, nil, err
		}
		return true, admitted, nil
	})
	polls := 0
	kube.PrependReactor("get", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		getAction := action.(k8stesting.GetAction)
		if getAction.GetNamespace() != namespace || getAction.GetName() != watchName {
			return false, nil, nil
		}
		polls++
		if polls > 1 && createdJob != nil {
			completed := createdJob.DeepCopy()
			completed.Status.Succeeded = 1
			if err := kube.Tracker().Update(jobsResource, completed, namespace); err != nil {
				return true, nil, err
			}
		}
		return false, nil, nil
	})
	return func() *batchv1.Job { return createdJob }
}

// TestRunStopAfterPreviousTerminalJobCreatesANewJobAndStops is the
// regression test for erun#1678: a stop issued after a previous stop Job already
// went terminal must run a fresh Job to a fresh outcome, never replay the
// old one's cached result. The old Job is seeded Failed specifically so a
// wrongly-replayed outcome is caught by the outcome assertion, not just by
// the job count.
func TestRunStopAfterPreviousTerminalJobCreatesANewJobAndStops(t *testing.T) {
	params := testStopParams()
	oldID := "3f2a91cc-1111-2222-3333-444455556666"
	newID := "9b7d40aa-1111-2222-3333-444455556666"
	oldName := StopJobName(params.Tenant, params.Environment, oldID)
	newName := StopJobName(params.Tenant, params.Environment, newID)
	if oldName == newName {
		t.Fatalf("old and new stop attempts share the job name %q", oldName)
	}

	kube := fake.NewSimpleClientset(&batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: oldName, Namespace: params.Namespace, Labels: lifecycleJobLabels(params.Tenant, params.Environment)},
		Status:     batchv1.JobStatus{Failed: 1},
	})
	created := admitAndCompleteJobReactors(t, kube, newName, params.Namespace)

	launcher := NewLauncher(kube)
	launcher.PollEvery(0)
	params.StopID = newID
	result, err := launcher.RunStop(context.Background(), params)
	if err != nil {
		t.Fatalf("run stop: %v", err)
	}
	if result.Outcome != OutcomeSucceeded {
		t.Fatalf("stop outcome = %q, want succeeded — got the previous terminal attempt's cached outcome instead of running a new Job", result.Outcome)
	}
	if created() == nil {
		t.Fatal("a fresh stop job was not created for the new attempt")
	}

	jobs, err := kube.BatchV1().Jobs(params.Namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs.Items) != 2 {
		t.Fatalf("jobs in namespace = %d, want 2 (the old terminal attempt plus the new one)", len(jobs.Items))
	}
}

// TestRunStopWhileStopJobStillRunningReWatchesIt is the dedup test erun#1678
// asks to be preserved: a stop issued while a previous stop Job is still in
// flight must re-watch that same Job rather than starting a second one. The
// "create" reactor fails the test outright if a second Job is ever created,
// so this catches the fix regressing into "always mint a fresh Job".
func TestRunStopWhileStopJobStillRunningReWatchesIt(t *testing.T) {
	params := testStopParams()
	inFlightID := "3f2a91cc-1111-2222-3333-444455556666"
	inFlightName := StopJobName(params.Tenant, params.Environment, inFlightID)

	kube := fake.NewSimpleClientset(&batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: inFlightName, Namespace: params.Namespace, Labels: lifecycleJobLabels(params.Tenant, params.Environment)},
		Status:     batchv1.JobStatus{Active: 1},
	})

	jobsResource := batchv1.SchemeGroupVersion.WithResource("jobs")
	polls := 0
	kube.PrependReactor("get", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		getAction := action.(k8stesting.GetAction)
		if getAction.GetName() != inFlightName {
			return false, nil, nil
		}
		polls++
		if polls > 1 {
			completed := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: inFlightName, Namespace: params.Namespace, Labels: lifecycleJobLabels(params.Tenant, params.Environment)},
				Status:     batchv1.JobStatus{Succeeded: 1},
			}
			if err := kube.Tracker().Update(jobsResource, completed, params.Namespace); err != nil {
				return true, nil, err
			}
		}
		return false, nil, nil
	})
	kube.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		obj := action.(k8stesting.CreateAction).GetObject().(*batchv1.Job)
		t.Fatalf("a second stop job %q was created while %q was still in flight", obj.Name, inFlightName)
		return true, nil, nil
	})

	launcher := NewLauncher(kube)
	launcher.PollEvery(0)
	// A fresh explicit stop request still mints its own attempt id, exactly
	// as routes.EnvironmentRoutes.stopEnvironment does — the point of this
	// test is that RunStop finds the in-flight Job before that id is ever
	// used to build a Job name.
	params.StopID = "9b7d40aa-1111-2222-3333-444455556666"
	result, err := launcher.RunStop(context.Background(), params)
	if err != nil {
		t.Fatalf("run stop: %v", err)
	}
	if result.Outcome != OutcomeSucceeded {
		t.Fatalf("stop outcome = %q, want succeeded", result.Outcome)
	}

	jobs, err := kube.BatchV1().Jobs(params.Namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs.Items) != 1 {
		t.Fatalf("jobs in namespace = %d, want 1 — the fresh attempt should have re-watched the in-flight job instead of starting a second", len(jobs.Items))
	}
	if jobs.Items[0].Name != inFlightName {
		t.Fatalf("job name = %q, want the original in-flight job %q to have been re-watched, not replaced", jobs.Items[0].Name, inFlightName)
	}
}

// TestStopAfterRedeploySequenceEndsStopped reproduces the exact sequence
// erun#1678 reported: stop, redeploy, stop again. The redeploy's own Job shares
// the identical tenant+environment labels every lifecycle Job carries (and
// is left running, to prove it), so this also pins that activeStopJobName's
// name-prefix filter tells a deploy Job apart from a stop Job rather than
// mistaking one for an in-flight stop.
func TestStopAfterRedeploySequenceEndsStopped(t *testing.T) {
	stopParams := testStopParams()
	firstStopID := "3f2a91cc-1111-2222-3333-444455556666"
	secondStopID := "9b7d40aa-1111-2222-3333-444455556666"
	firstStopName := StopJobName(stopParams.Tenant, stopParams.Environment, firstStopID)
	secondStopName := StopJobName(stopParams.Tenant, stopParams.Environment, secondStopID)

	deployParams := testParams()
	deployName := DeployJobName(deployParams.Tenant, deployParams.Environment, deployParams.Version, "")

	// The first stop already ran to completion; the environment was then
	// redeployed and that Job is still active when the second stop runs. The
	// first stop is seeded Failed, not Succeeded: a genuine second stop must
	// end up Succeeded, so if it instead replayed the first attempt's cached
	// outcome, the outcome assertion below would catch it — seeding the first
	// attempt Succeeded too would let a replay slip through undetected, since
	// "succeeded" is what both the replay and a real run would report.
	kube := fake.NewSimpleClientset(
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: firstStopName, Namespace: stopParams.Namespace, Labels: lifecycleJobLabels(stopParams.Tenant, stopParams.Environment)},
			Status:     batchv1.JobStatus{Failed: 1},
		},
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: deployName, Namespace: deployParams.Namespace, Labels: lifecycleJobLabels(deployParams.Tenant, deployParams.Environment)},
			Status:     batchv1.JobStatus{Active: 1},
		},
	)
	created := admitAndCompleteJobReactors(t, kube, secondStopName, stopParams.Namespace)

	launcher := NewLauncher(kube)
	launcher.PollEvery(0)
	stopParams.StopID = secondStopID
	result, err := launcher.RunStop(context.Background(), stopParams)
	if err != nil {
		t.Fatalf("run second stop: %v", err)
	}
	if result.Outcome != OutcomeSucceeded {
		t.Fatalf("second stop outcome = %q, want succeeded — the environment must end up stopped, not replay the first attempt's failed outcome", result.Outcome)
	}
	if created() == nil {
		t.Fatal("the second stop did not create its own fresh job — it was confused by, or replayed, an unrelated job")
	}

	jobs, err := kube.BatchV1().Jobs(stopParams.Namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs.Items) != 3 {
		t.Fatalf("jobs in namespace = %d, want 3 (the failed first stop, the active redeploy, and the new second stop)", len(jobs.Items))
	}
}
