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
	// Single-pass real-run shell on a local env with a git repo and ~/.ssh, so ExecShell
	// seeds the SSH key over `kubectl exec -i` stdin (never argv).
	skipIfPortsBusy(t, 26100, 26133)
	setup := env.New(t)
	fixture.SeedTenantEnvWithLocalPortRangeStart(t, setup, "team", "dev", 26100)
	// A real devops chart keeps real-run open off the interactive chart-creation prompt.
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.RunGit(t, setup.Cwd, "remote", "add", "origin", "git@github.com:acme/widgets.git")
	sshDir := filepath.Join(setup.Home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("mkdir ~/.ssh: %v", err)
	}
	// Comment, blank line and non-matching Host exercise parseSSHConfig's skip branches.
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
	// env.New names the cwd leaf "cwd", so the advertised worktree is /home/erun/git/cwd.
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubsDir, fixture.KubectlDeployedStubSpec{
		DeploymentName: "team-devops",
		ContainerName:  "team-devops",
		RepoPath:       "/home/erun/git/cwd",
		MCPPort:        26100,
		SSHPort:        26122,
		ExecExitCodes:  []int{0},
		SeedKeyFile:    seededKeyFile,
	})...)
	// Forces the interactive path; harness stdin is non-TTY.
	envVars = append(envVars, "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/shell_real_run_single_pass_via_kubectl_stub", normalize.Apply(result.Combined))
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
	// A non-interactive open must not start a shell that exits on EOF and kills the
	// forwards; redialing the stub's bound port after exit proves the forward survived.
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
	// Exit 76 means another window took the session over: end cleanly with the takeover notice.
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
	// An --app-session tab runs inside a real dtach pty.
	envVars = append(envVars, "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev", "--app-session", "open-0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("takeover must end the shell loop cleanly, exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/shell_real_run_session_taken_over_exits_with_notice", normalize.Apply(result.Combined))
}

func testOpenShellRealRunFailedRolloutPrintsPodDiagnostics(t *testing.T) {
	// A failed availability wait must render pod/event diagnostics instead of kubectl's
	// bare "exit status 1"; the canned JSON drives the formatter's branches.
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
	// Forces the interactive path; harness stdin is non-TTY.
	envVars = append(envVars, "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"open", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 1 {
		t.Fatalf("expected exit 1 from the failed rollout wait, got %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "open/shell_real_run_failed_rollout_prints_pod_diagnostics", normalize.Apply(result.Combined))
}

func testOpenShellRealRunReattachDeployCyclesOnce(t *testing.T) {
	// Exit 75 requests an in-shell deploy: deploy, then re-enter exec (exec-calls has two lines).
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
	// The managed deploy needs erun-devops reported as published.
	envVars = append(envVars, "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0")
	// Forces the interactive path; harness stdin is non-TTY.
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
	// Exit 137 with a healthy replacement pod reattaches silently; exec-calls proves the second exec.
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
	// Forces the interactive path; harness stdin is non-TTY.
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
