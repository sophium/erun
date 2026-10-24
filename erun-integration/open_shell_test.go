package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testOpenShellRealRunSinglePassViaKubectlStub(t *testing.T) {
	// Real-run (no --dry-run) shell flow, single pass: the kubectl stub
	// reports the runtime deployment as deployed-and-matching, the
	// port-forward simulators come up on the pinned 26100 range, and the
	// interactive `exec -it` bootstrap exits 0, so runShellLoop's happy
	// path ends after one iteration with exit 0. The env is deliberately
	// LOCAL (remote: false) with a real git repo + ~/.ssh seedables:
	// that is the only shape that drives ExecShell's seedRemoteSSHKey
	// end to end (resolveGitRemote → parseSSHConfig →
	// loadPrivateKeyMaterial → `kubectl exec -i` with the key on stdin)
	// plus loadKnownHostsLines for the bootstrap's known_hosts seeding.
	// The exec stub exit code is the decision input runShellLoop branches
	// on; side-effect asserts cover what the captured streams cannot:
	// the key bytes that flowed to the stub's stdin (never argv) and the
	// RecordEnvironmentActivity cli.json marker.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	// A real tenant devops chart keeps the runtime spec off the default
	// chart path, so real-run open does not stop at the interactive
	// "create team-devops chart?" prompt (AGENTS.md: prefer fixtures
	// that bypass prompts over scripted stdin).
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.RunGit(t, setup.Cwd, "remote", "add", "origin", "git@github.com:acme/widgets.git")
	sshDir := filepath.Join(setup.Home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("mkdir ~/.ssh: %v", err)
	}
	// ssh config with a comment, a blank line, and a non-matching Host
	// entry so parseSSHConfig's skip/flush branches run, plus the
	// matching github.com entry whose ~-prefixed IdentityFile exercises
	// expandSSHPath.
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(
		"# integration test ssh config\n"+
			"\n"+
			"Host *.example.org\n"+
			"  IdentityFile ~/.ssh/other_key\n"+
			"Host github.com\n"+
			"  IdentityFile ~/.ssh/test_ed25519\n"), 0o600); err != nil {
		t.Fatalf("write ssh config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "test_ed25519"), []byte(
		"-----BEGIN OPENSSH PRIVATE KEY-----\nFAKEKEYMATERIAL\n-----END OPENSSH PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(
		"github.com ssh-ed25519 AAAAGITHUBHOSTKEY=\n"+
			"unrelated.example.net ssh-rsa AAAAOTHERKEY=\n"), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}
	stubsDir := filepath.Join(setup.Cwd, "stubs")
	seededKeyFile := filepath.Join(setup.Cwd, "seeded-key")
	// env.New always names the cwd leaf "cwd", so the remote worktree
	// the deployment must advertise is the deterministic
	// /home/erun/git/cwd (RemoteShellWorktreePath of the local repo).
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
		DeploymentName: "team-devops",
		ContainerName:  "team-devops",
		RepoPath:       "/home/erun/git/cwd",
		MCPPort:        26100,
		SSHPort:        26122,
		ExecExitCodes:  []int{0},
		SeedKeyFile:    seededKeyFile,
	})...)
	// ERUN_FORCE_TTY=1: locks the interactive shell path an operator at a
	// real terminal takes; this harness's stdin is otherwise non-TTY, which
	// would now take the no-shell branch instead.
	envVars = append(envVars, "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/shell_real_run_single_pass_via_kubectl_stub", normalize.Apply(result.Combined))
	// The private key must have reached the pod on the seeding exec's
	// stdin (side effect outside the captured streams).
	seeded, err := os.ReadFile(seededKeyFile)
	if err != nil {
		t.Fatalf("read seeded key (seedRemoteSSHKey did not stream the key): %v", err)
	}
	if !strings.Contains(string(seeded), "FAKEKEYMATERIAL") {
		t.Errorf("expected the private key material on the seed exec's stdin, got:\n%s", seeded)
	}
	execCalls := waitForFile(t, filepath.Join(stubsDir, "exec-calls"), 2*time.Second)
	if got := strings.Count(execCalls, "call"); got != 1 {
		t.Errorf("expected 1 interactive exec call, got %d", got)
	}
	if _, err := os.Stat(filepath.Join(setup.CacheHome, "erun", "activity", "team", "dev", "cli.json")); err != nil {
		t.Errorf("expected RecordEnvironmentActivity to write cli.json: %v", err)
	}
}

func testOpenNoTtyRealRunKeepsForwardsWithoutAShell(t *testing.T) {
	// The reported failure, end to end and for real (not a dry-run trace):
	// a non-interactive `erun open` must never drop into an interactive
	// shell that reads EOF and exits almost instantly, taking the
	// port-forwards it just started down with it. This harness's default
	// stdin is already non-TTY, standing in for the MCP client/orchestrator/
	// script that hits this in practice. The kubectl port-forward stub
	// really binds 127.0.0.1:26100, so dialing it again after open has
	// already exited is the proof that the forward outlived the command —
	// not just that no error was printed.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithPortRange(t, setup, "team", "dev", 26100)
	stubsDir := filepath.Join(setup.Cwd, "stubs")
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
		DeploymentName: "team-devops",
		ContainerName:  "team-devops",
		RepoPath:       "/home/erun/git/team",
		MCPPort:        26100,
		SSHPort:        26122,
		ExecExitCodes:  []int{0},
	})...)
	result := erun.Run(t, []string{"open", "team", "dev", "--no-alias-prompt"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/no_tty_real_run_keeps_forwards_without_a_shell", normalize.Apply(result.Combined))
	if _, err := os.Stat(filepath.Join(stubsDir, "exec-calls")); err == nil {
		t.Fatal("a non-interactive open must never invoke the interactive shell exec")
	}
	conn, err := netDialTimeout("tcp", "127.0.0.1:26100", time.Second)
	if err != nil {
		t.Fatalf("MCP port-forward must still be listening after open returns without a shell: %v", err)
	}
	_ = conn.Close()
}

func testOpenShellRealRunSessionTakenOverExitsWithNotice(t *testing.T) {
	// Takeover handover, end to end: the persistent session's exec
	// wrapper exits 76 when another ERun window re-attaches the session.
	// ExecShell must map that to ErrShellSessionTakenOver and
	// runShellLoop must end cleanly — exit 0, no relaunch — after
	// printing the stable ShellSessionTakenOverNotice line the desktop
	// matches to stop its reconnect loop. The exec stub's exit code 76
	// is the decision input that drives the branch. Replaces the deleted
	// unit test cmd.TestRunShellLoopEndsCleanlyWhenSessionTakenOver.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithPortRange(t, setup, "team", "dev", 26100)
	stubsDir := filepath.Join(setup.Cwd, "stubs")
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
		DeploymentName: "team-devops",
		ContainerName:  "team-devops",
		RepoPath:       "/home/erun/git/team",
		MCPPort:        26100,
		SSHPort:        26122,
		ExecExitCodes:  []int{76},
	})...)
	// ERUN_FORCE_TTY=1: see app_session_dry_run_pure_open_does_not_deploy —
	// an --app-session tab runs inside a real dtach pty.
	envVars = append(envVars, "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "open-0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("takeover must end the shell loop cleanly, exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/shell_real_run_session_taken_over_exits_with_notice", normalize.Apply(result.Combined))
}

func testOpenShellRealRunFailedRolloutPrintsPodDiagnostics(t *testing.T) {
	// When the deployment-availability wait fails in real-run, the open
	// flow must not stop at kubectl's bare "exit status 1": ExecShell's
	// enrichShellDeploymentError loads `get pods -o json` + `get events
	// -o json` and renders the runtime pod diagnostics so the user sees
	// why the rollout is stuck. The canned pods JSON drives the
	// formatter's branches (waiting/terminated/running container states,
	// init containers, conditions with reason+message, pod
	// status reason/message, >3 pods omitted); the canned events JSON
	// drives the warning filter, per-pod matching via involvedObject and
	// regarding, every timestamp-field fallback, count/series/deprecated
	// counts, and the >5 events omission line. Exit code 1 is cobra's
	// RunE error exit — asserted exactly so a silent success can never
	// pass.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithPortRange(t, setup, "team", "dev", 26100)
	stubsDir := filepath.Join(setup.Cwd, "stubs")
	podsJSON := `{
  "items": [
    {
      "metadata": {"name": "team-devops-aaa"},
      "status": {
        "phase": "Pending",
        "conditions": [
          {"type": "PodScheduled", "status": "False", "reason": "Unschedulable", "message": "0/3 nodes are available: 3 Insufficient memory."}
        ]
      }
    },
    {
      "metadata": {"name": "team-devops-bbb"},
      "status": {
        "phase": "Running",
        "conditions": [
          {"type": "Ready", "status": "False", "reason": "ContainersNotReady", "message": "containers with unready status: [team-devops]"},
          {"type": "ContainersReady", "status": "False"}
        ],
        "initContainerStatuses": [
          {"name": "init-workspace", "ready": true, "restartCount": 0, "state": {"terminated": {"exitCode": 0, "reason": "Completed", "startedAt": "2026-06-01T09:58:00Z", "finishedAt": "2026-06-01T09:58:05Z"}}}
        ],
        "containerStatuses": [
          {"name": "team-devops", "ready": false, "restartCount": 4, "state": {"waiting": {"reason": "CrashLoopBackOff", "message": "back-off 2m40s restarting failed container=team-devops"}}, "lastState": {"terminated": {"exitCode": 1, "reason": "Error", "startedAt": "2026-06-01T09:59:00Z", "finishedAt": "2026-06-01T09:59:10Z"}}}
        ]
      }
    },
    {
      "metadata": {"name": "team-devops-ccc"},
      "status": {
        "phase": "Running",
        "reason": "SandboxChanged",
        "message": "Pod sandbox changed, it will be killed and re-created.",
        "containerStatuses": [
          {"name": "team-devops", "ready": true, "restartCount": 1, "state": {"running": {"startedAt": "2026-06-01T10:01:00Z"}}}
        ]
      }
    },
    {
      "metadata": {"name": "team-devops-ddd"},
      "status": {"phase": "Pending"}
    }
  ]
}`
	eventsJSON := `{
  "items": [
    {"involvedObject": {"name": "team-devops-aaa"}, "type": "Warning", "reason": "FailedScheduling", "message": "0/3 nodes are available: 3 Insufficient memory.", "count": 3, "lastTimestamp": "2026-06-01T10:06:00Z"},
    {"involvedObject": {"name": "team-devops-aaa"}, "type": "Warning", "reason": "BackOff", "note": "Back-off pulling image registry.example/test/team-devops", "eventTime": "2026-06-01T10:05:00Z", "series": {"count": 7, "lastObservedTime": "2026-06-01T10:05:30Z"}},
    {"regarding": {"name": "team-devops-aaa"}, "type": "Warning", "reason": "FailedMount", "message": "MountVolume.SetUp failed for volume workspace", "deprecatedCount": 2, "deprecatedLastTimestamp": "2026-06-01T10:04:00Z"},
    {"involvedObject": {"name": "team-devops-aaa"}, "type": "Warning", "reason": "Unhealthy", "message": "Readiness probe failed: connection refused", "metadata": {"creationTimestamp": "2026-06-01T10:03:00Z"}},
    {"involvedObject": {"name": "team-devops-aaa"}, "type": "Warning", "reason": "FailedCreatePodSandBox", "message": "Failed to create pod sandbox", "firstTimestamp": "2026-06-01T10:02:00Z"},
    {"involvedObject": {"name": "team-devops-aaa"}, "type": "Warning", "reason": "Evicted", "message": "Low memory"},
    {"involvedObject": {"name": "team-devops-aaa"}, "type": "Normal", "reason": "Pulled", "message": "Container image already present"},
    {"involvedObject": {"name": "some-other-pod"}, "type": "Warning", "reason": "Unrelated", "message": "must be filtered out"}
  ]
}`
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
		DeploymentName: "team-devops",
		ContainerName:  "team-devops",
		RepoPath:       "/home/erun/git/team",
		MCPPort:        26100,
		SSHPort:        26122,
		WaitExitCode:   1,
		WaitStderr:     "error: timed out waiting for the condition on deployments/team-devops",
		PodsJSON:       podsJSON,
		EventsJSON:     eventsJSON,
	})...)
	// ERUN_FORCE_TTY=1: locks the interactive shell path an operator at a
	// real terminal takes; this harness's stdin is otherwise non-TTY.
	envVars = append(envVars, "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 1 {
		t.Fatalf("expected exit 1 from the failed rollout wait, got %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/shell_real_run_failed_rollout_prints_pod_diagnostics", normalize.Apply(result.Combined))
}

func testOpenShellRealRunReattachDeployCyclesOnce(t *testing.T) {
	// The in-shell `erun deploy` handoff: the bootstrap exits 75 when the
	// user requests a deploy from inside the remote shell. runShellLoop
	// must map that to ErrShellReattachDeploy, run the managed deploy
	// (remote env → embedded chart, helm/docker stubbed to succeed), and
	// re-enter the loop; the second exec exits 0 and the run ends 0. The
	// stateful exec stub (first call 75, then 0, tracked in the
	// exec-calls file) is the decision input; the file's two lines prove
	// the loop really re-entered kubectl exec after the deploy.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithPortRange(t, setup, "team", "dev", 26100)
	stubsDir := filepath.Join(setup.Cwd, "stubs")
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
		DeploymentName: "team-devops",
		ContainerName:  "team-devops",
		RepoPath:       "/home/erun/git/team",
		MCPPort:        26100,
		SSHPort:        26122,
		ExecExitCodes:  []int{75, 0},
	})...)
	fixture.StubBinary(t, stubsDir, "helm", "")
	fixture.StubBinary(t, stubsDir, "docker", "")
	envVars = append(envVars, fixture.StubEnv(stubsDir, "helm", "docker")...)
	// The reattach handoff's managed deploy resolves the runtime chart ladder;
	// the seam confirms erun-devops published so it installs it instead of
	// refusing.
	envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	// ERUN_FORCE_TTY=1: locks the interactive shell path an operator at a
	// real terminal takes; this harness's stdin is otherwise non-TTY.
	envVars = append(envVars, "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/shell_real_run_reattach_deploy_cycles_once", normalize.Apply(result.Combined))
	execCalls := waitForFile(t, filepath.Join(stubsDir, "exec-calls"), 2*time.Second)
	if got := strings.Count(execCalls, "call"); got != 2 {
		t.Errorf("expected 2 interactive exec calls (handoff + reattach), got %d:\n%s", got, execCalls)
	}
}

func testOpenShellRealRunPodReplacedReattaches(t *testing.T) {
	// kubectl exec dying with 137 (SIGKILL — the runtime pod was
	// replaced under the shell) must not surface as an error when the
	// replacement pod is already healthy: ExecShell probes `get pods`,
	// runtimePodLooksLikeCleanReplacement accepts the Running/Ready/
	// restartCount=0 pod, and runShellLoop silently reattaches
	// (continue). The second exec exits 0 → overall exit 0. The silence
	// is the contract — the golden locks that no error or notice line
	// appears — so the exec-calls file is the proof that a second exec
	// actually happened.
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedRemoteTenantEnvWithPortRange(t, setup, "team", "dev", 26100)
	stubsDir := filepath.Join(setup.Cwd, "stubs")
	healthyPodsJSON := `{
  "items": [
    {
      "metadata": {"name": "team-devops-fresh"},
      "status": {
        "phase": "Running",
        "conditions": [
          {"type": "Ready", "status": "True"},
          {"type": "ContainersReady", "status": "True"}
        ],
        "containerStatuses": [
          {"name": "team-devops", "ready": true, "restartCount": 0, "state": {"running": {"startedAt": "2026-06-01T10:10:00Z"}}}
        ]
      }
    }
  ]
}`
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
		DeploymentName: "team-devops",
		ContainerName:  "team-devops",
		RepoPath:       "/home/erun/git/team",
		MCPPort:        26100,
		SSHPort:        26122,
		ExecExitCodes:  []int{137, 0},
		PodsJSON:       healthyPodsJSON,
	})...)
	// ERUN_FORCE_TTY=1: locks the interactive shell path an operator at a
	// real terminal takes; this harness's stdin is otherwise non-TTY.
	envVars = append(envVars, "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("pod replacement must reattach cleanly, exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/shell_real_run_pod_replaced_reattaches", normalize.Apply(result.Combined))
	execCalls := waitForFile(t, filepath.Join(stubsDir, "exec-calls"), 2*time.Second)
	if got := strings.Count(execCalls, "call"); got != 2 {
		t.Errorf("expected 2 interactive exec calls (replaced + reattach), got %d:\n%s", got, execCalls)
	}
}
