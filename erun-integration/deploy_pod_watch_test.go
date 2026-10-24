package integration

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testDeployDryRunUnaffectedByKubectlPodWatchLibraryExecutionMode(t *testing.T) {
	// Locks the dry-run/audit contract for kubectl-pod-watch: the plan must
	// stay byte-identical to the subprocess-mode golden
	// (dry_run_from_devops_cwd), including the "kubectl ... get pods -o
	// json" trace line, even with execution.modes.kubectl-pod-watch=library,
	// since tracePodWatchAction only ever renders the trace and the watcher
	// itself (watchReleasePods -> runKubectlGetPods) never runs until a real
	// helm upgrade actually starts.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	seedExecutionMode(t, setup, "kubectl-pod-watch", "library")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "deploy/dry_run_from_devops_cwd", normalize.Apply(result.Combined))
}

func testDeployRealRunKubectlPodWatchLibraryExecutionModeReachesTheAPIServerDirectly(t *testing.T) {
	// Proves the library path produces the same observable result as the
	// subprocess path (a clean rollout logs the same summary line and lets
	// helm finish) via a real client-go List against a fake API server
	// instead of shelling out to kubectl. The kubectl stub fails loudly and
	// distinctively only for the "get pods -o json" argv shape (the
	// namespace-existence check deploy also runs still succeeds), so a
	// fallback to the subprocess path for the watch surfaces as a
	// recognizable failure instead of silently passing.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	seedExecutionMode(t, setup, "kubectl-pod-watch", "library")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "kubectl", strings.Join([]string{
		`case "$*" in`,
		`  *"get pods -o json"*) printf '%s\n' "fell through to the kubectl subprocess for the pod watch" >&2; exit 1 ;;`,
		`  *"get namespace "*) printf 'namespace/team-dev\n' ;;`,
		`esac`,
		`exit 0`,
	}, "\n"))
	fixture.StubBinaryWithScript(t, stubs, "helm", "sleep 0.5\nexit 0\n")
	fixture.StubBinary(t, stubs, "docker", "")

	var apiHits atomic.Int64
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits.Add(1)
		if r.URL.Path != "/api/v1/namespaces/team-dev/pods" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, cleanRolloutPodJSON)
	}))
	defer apiServer.Close()
	writeFakeAPIServerKubeconfig(t, setup, apiServer.URL)

	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	envVars = append(envVars, "ERUN_DEPLOY_POD_WATCH_INTERVAL=100ms")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	out := normalize.Apply(result.Combined)
	if strings.Contains(out, "fell through to the kubectl subprocess") {
		t.Fatalf("pod watch used the kubectl subprocess despite library execution mode:\n%s", out)
	}
	if !strings.Contains(out, "    pod team-devops-aaaaaa: erun-devops Running (Ready), erun-dind Running (Ready)") {
		t.Fatalf("missing pod-watch summary line in output:\n%s", out)
	}
	if !strings.Contains(out, "==> Deployed team/dev <VERSION>") {
		t.Fatalf("expected clean deploy completion in output:\n%s", out)
	}
	if got := apiHits.Load(); got == 0 {
		t.Fatalf("fake API server received no requests; library path never ran")
	}
}

func testDeployRealRunPodWatchLogsCleanRollout(t *testing.T) {
	// Exercises the in-flight pod watcher started by DeployHelmChart.
	// The kubectl stub returns a pod owned by this helm release with
	// every container Running+Ready, so the watcher prints a single
	// status line and lets helm finish naturally. Locks the dry-run
	// trace's "watching pods" promise to a real-run summary line.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{Stdout: cleanRolloutPodJSON})
	fixture.StubBinaryWithScript(t, stubs, "helm", "sleep 0.5\nexit 0\n")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	envVars = append(envVars, "ERUN_DEPLOY_POD_WATCH_INTERVAL=100ms")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	out := normalize.Apply(result.Combined)
	if !strings.Contains(out, "    pod team-devops-aaaaaa: erun-devops Running (Ready), erun-dind Running (Ready)") {
		t.Fatalf("missing pod-watch summary line in output:\n%s", out)
	}
	if !strings.Contains(out, "==> Deployed team/dev <VERSION>") {
		t.Fatalf("expected clean deploy completion in output:\n%s", out)
	}
}

func testDeployRealRunPodWatchWaitsThroughImagePullBackoff(t *testing.T) {
	// kubectl stub reports a pod with one container in ImagePullBackOff
	// whose message is the generic "Back-off pulling image" (no permanent
	// rejection). A slow/large pull legitimately cycles through this state,
	// so the watcher must NOT abort: it keeps waiting and lets helm finish.
	// helm exits 0 quickly to stand in for the pull eventually succeeding.
	// Proves the "wait while the image is still pulling" contract and the
	// "Pulling image (...)" status labelling.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{Stdout: imagePullBackOffPodJSON})
	fixture.StubBinaryWithScript(t, stubs, "helm", "sleep 0.5\nexit 0\n")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	envVars = append(envVars, "ERUN_DEPLOY_POD_WATCH_INTERVAL=100ms")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("expected clean exit (pull-in-progress must not abort), got %d:\n%s", result.ExitCode, result.Combined)
	}
	out := normalize.Apply(result.Combined)
	if !strings.Contains(out, "    pod team-devops-7d4b4c: erun-dind Pulling image (ImagePullBackOff)") {
		t.Fatalf("missing pull-in-progress status line in output:\n%s", out)
	}
	if strings.Contains(out, "deploy failed early") {
		t.Fatalf("watcher must not abort while the image is still pulling:\n%s", out)
	}
	if !strings.Contains(out, "==> Deployed team/dev <VERSION>") {
		t.Fatalf("expected deploy to complete once helm finished:\n%s", out)
	}
}

func testDeployRealRunReportsADeadlineThatExpiredWhileTheImageWasStillPulling(t *testing.T) {
	// The reported outage: a cold pull of the runtime image outlasted the
	// rollout wait, so the rollout was declared failed mid-download -- and
	// because every runtime chart replaces its pod with the Recreate
	// strategy, the previous pod was already gone and the environment was
	// left running nothing at all. helm's own words are the same whether
	// the rollout failed or was still working, so the fact that has to
	// reach the operator is the pod watcher's: at the moment the wait
	// expired, the new pod's image was still downloading.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{Stdout: imagePullBackOffPodJSON})
	// helm outlasts enough polls for the watcher to observe the pull, then
	// fails the way a rollout whose deadline expired does.
	fixture.StubBinaryWithScript(t, stubs, "helm", strings.Join([]string{
		`sleep 1`,
		`printf '%s\n' 'Error: UPGRADE FAILED: resource Deployment/team-dev/team-devops not ready. status: InProgress, message: Available: 0/1' >&2`,
		`exit 1`,
	}, "\n"))
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	envVars = append(envVars, "ERUN_DEPLOY_POD_WATCH_INTERVAL=100ms")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected the failed rollout to fail the deploy, got 0:\n%s", result.Combined)
	}
	out := normalize.Apply(result.Combined)
	for _, want := range []string{
		// The image, not only the container: the wait ending mid-pull is
		// answered by a longer deploy.timeout when the image is genuinely
		// still downloading, and by deploying the right image when the tag
		// was never published -- and the container name alone cannot tell
		// those apart.
		"team-devops-7d4b4c/erun-dind (ghcr.io/sophium/erun-dind:<VERSION>) was still pulling its image",
		"this is the deploy's own timeout ending the rollout, not a container failure",
		"the previous pod was already torn down and this environment is running no pod",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in the failed deploy's output, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "deploy failed early") {
		t.Fatalf("a pull in progress is not an early failure:\n%s", out)
	}
	golden.Equal(t, "deploy/real_run_reports_a_deadline_that_expired_while_the_image_was_still_pulling", out)
}

func testDeployRealRunPodWatchAbortsOnPermanentImagePullFailure(t *testing.T) {
	// kubectl stub reports a container in ErrImagePull whose message is a
	// permanent registry rejection ("manifest unknown") — retrying will
	// never succeed (typo'd/absent tag, bad auth). helm sleeps so the
	// watcher fires first and kills it. Proves the "fail fast on a real
	// failure" half of the pull-aware policy.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{Stdout: permanentImagePullFailurePodJSON})
	fixture.StubBinaryWithScript(t, stubs, "helm", "exec sleep 30\n")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	envVars = append(envVars, "ERUN_DEPLOY_POD_WATCH_INTERVAL=100ms")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit on a permanent pull failure, got 0:\n%s", result.Combined)
	}
	out := normalize.Apply(result.Combined)
	if !strings.Contains(out, "deploy failed early: pod team-devops-7d4b4c container erun-dind ErrImagePull") {
		t.Fatalf("missing structured early-fail error in output:\n%s", out)
	}
	if !strings.Contains(out, "manifest unknown") {
		t.Fatalf("missing permanent pull-failure message in output:\n%s", out)
	}
}

func testDeployRealRunPodWatchAbortsOnCrashloopAfterThreshold(t *testing.T) {
	// kubectl stub reports a CrashLoopBackOff with restartCount above
	// the threshold. The watcher kills helm and surfaces the last
	// terminated message so the user sees why the container is
	// crashing, not just helm's generic timeout.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{Stdout: crashLoopPodJSON})
	fixture.StubBinaryWithScript(t, stubs, "helm", "exec sleep 30\n")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	envVars = append(envVars, "ERUN_DEPLOY_POD_WATCH_INTERVAL=100ms")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0:\n%s", result.Combined)
	}
	out := normalize.Apply(result.Combined)
	if !strings.Contains(out, "deploy failed early: pod team-devops-crash container erun-devops CrashLoopBackOff") {
		t.Fatalf("missing structured early-fail error in output:\n%s", out)
	}
	if !strings.Contains(out, "exited with code 137") {
		t.Fatalf("missing last-terminated message in output:\n%s", out)
	}
}

func testDeployRealRunPodWatchWaitsOutTheUnschedulableGracePeriod(t *testing.T) {
	// kubectl stub reports a pod stuck PodScheduled=False/Unschedulable on
	// every poll. A brief unschedulable window is normal (the scheduler
	// re-evaluates on cluster changes), so the watcher must not abort on the
	// first observation — only once the grace period elapses. The grace is
	// shrunk via ERUN_DEPLOY_POD_WATCH_UNSCHEDULED_GRACE (the production
	// default is 30s, too slow for a test) rather than asserting a wall-clock
	// sleep tied to that default; see resolveUnscheduledGracePeriod.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{Stdout: unschedulablePodJSON})
	fixture.StubBinaryWithScript(t, stubs, "helm", "exec sleep 30\n")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	envVars = append(envVars, "ERUN_DEPLOY_POD_WATCH_INTERVAL=50ms", "ERUN_DEPLOY_POD_WATCH_UNSCHEDULED_GRACE=150ms")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit once the grace period elapses, got 0:\n%s", result.Combined)
	}
	out := normalize.Apply(result.Combined)
	if !strings.Contains(out, "deploy failed early: pod team-devops-pending Unschedulable") {
		t.Fatalf("missing structured early-fail error naming the pod-level (no container) reason, got:\n%s", out)
	}
	if !strings.Contains(out, "0/1 nodes are available: 1 Insufficient cpu, 1 Insufficient memory") {
		t.Fatalf("missing the scheduler's own message verbatim, got:\n%s", out)
	}
	if !strings.Contains(out, "    pod team-devops-pending: Pending (Unschedulable: 0/1 nodes are available: 1 Insufficient cpu, 1 Insufficient memory)") {
		t.Fatalf("missing the pod-status summary line surfacing the reason during the grace period, got:\n%s", out)
	}
}

const cleanRolloutPodJSON = `{
  "items": [
    {
      "metadata": {
        "name": "team-devops-aaaaaa",
        "annotations": {"meta.helm.sh/release-name": "team-devops"}
      },
      "status": {
        "phase": "Running",
        "containerStatuses": [
          {"name": "erun-devops", "ready": true, "restartCount": 0, "state": {"running": {"startedAt": "2026-05-09T12:00:00Z"}}},
          {"name": "erun-dind", "ready": true, "restartCount": 0, "state": {"running": {"startedAt": "2026-05-09T12:00:00Z"}}}
        ]
      }
    }
  ]
}`

const imagePullBackOffPodJSON = `{
  "items": [
    {
      "metadata": {
        "name": "team-devops-7d4b4c",
        "annotations": {"meta.helm.sh/release-name": "team-devops"}
      },
      "status": {
        "phase": "Pending",
        "containerStatuses": [
          {
            "name": "erun-dind",
            "image": "ghcr.io/sophium/erun-dind:1.0.0",
            "ready": false,
            "restartCount": 0,
            "state": {"waiting": {"reason": "ImagePullBackOff", "message": "Back-off pulling image \"ghcr.io/sophium/erun-dind:1.0.0\""}}
          }
        ]
      }
    }
  ]
}`

const permanentImagePullFailurePodJSON = `{
  "items": [
    {
      "metadata": {
        "name": "team-devops-7d4b4c",
        "annotations": {"meta.helm.sh/release-name": "team-devops"}
      },
      "status": {
        "phase": "Pending",
        "containerStatuses": [
          {
            "name": "erun-dind",
            "ready": false,
            "restartCount": 0,
            "state": {"waiting": {"reason": "ErrImagePull", "message": "rpc error: code = NotFound desc = failed to pull and unpack image \"ghcr.io/sophium/erun-dind:1.0.0\": failed to resolve reference: ghcr.io/sophium/erun-dind:1.0.0: manifest unknown"}}
          }
        ]
      }
    }
  ]
}`

const crashLoopPodJSON = `{
  "items": [
    {
      "metadata": {
        "name": "team-devops-crash",
        "annotations": {"meta.helm.sh/release-name": "team-devops"}
      },
      "status": {
        "phase": "Running",
        "containerStatuses": [
          {
            "name": "erun-devops",
            "ready": false,
            "restartCount": 3,
            "state": {"waiting": {"reason": "CrashLoopBackOff", "message": "back-off 5m restarting failed container"}},
            "lastState": {"terminated": {"reason": "Error", "exitCode": 137, "message": "exited with code 137"}}
          }
        ]
      }
    }
  ]
}`

// unschedulablePodJSON has no containerStatuses at all: the pod was never
// admitted to a node, so the only place its failure reason lives is
// status.conditions.
const unschedulablePodJSON = `{
  "items": [
    {
      "metadata": {
        "name": "team-devops-pending",
        "annotations": {"meta.helm.sh/release-name": "team-devops"}
      },
      "status": {
        "phase": "Pending",
        "conditions": [
          {"type": "PodScheduled", "status": "False", "reason": "Unschedulable", "message": "0/1 nodes are available: 1 Insufficient cpu, 1 Insufficient memory"}
        ]
      }
    }
  ]
}`
