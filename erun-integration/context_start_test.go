package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testContextStartHelpListsForceFlag(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"context", "start", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/start_help_lists_force_flag", normalize.Apply(result.Combined))
}

func testContextStartForceDryRunTracesAWSStartAndProfileSetup(t *testing.T) {
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	result := erun.Run(t, []string{"context", "start", "edge", "--force", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/start_force_dry_run_traces_aws_start_and_profile_setup", normalize.Apply(result.Combined))
}

func testContextStartWithoutForceBlocksOutsideWorkingHours(t *testing.T) {
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, "team")
	envDir := filepath.Join(tenantDir, "dev")
	for _, dir := range []string{tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(tenantDir, "config.yaml"),
		[]byte("name: team\nprojectroot: "+setup.Cwd+"\ndefaultenvironment: dev\n"), 0o644); err != nil {
		t.Fatalf("tenant cfg: %v", err)
	}
	// The windw must be outside the current minute for the gate to refuse
	// without --force, and it is derived rather than fixed for that reason:
	// a hardcoded window is outside every minute of the day except its own,
	// so the one scenario that exercises the refusal cleared once a day and
	// the suite failed then, with nothing about the code under test having
	// changed.
	window := workingHoursWindowOutsideNow(time.Now().UTC())
	envBody := "name: dev\nrepopath: " + setup.Cwd + "\nkubernetescontext: edge\ncontainerregistry: registry.example/test\nruntimeversion: 1.0.0\nmanagedcloud: true\ncloudprovideralias: dev\nidle:\n  workinghours: " + window + "\n  timezone: UTC\n"
	if err := os.WriteFile(filepath.Join(envDir, "config.yaml"), []byte(envBody), 0o644); err != nil {
		t.Fatalf("env cfg: %v", err)
	}
	result := erun.Run(t, []string{"context", "start", "edge", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when working-hours gate refuses, got 0:\n%s", result.Combined)
	}
	// The window itself is a function of the run's own clock, so it is
	// masked rather than compared: everything the scenario is about -- that
	// the gate refuses, and what it says when it does -- is unchanged.
	golden.Equal(t, "context/start_without_force_blocks_outside_working_hours",
		normalize.Apply(result.Combined, normalize.Replacement{Pattern: regexp.MustCompile(regexp.QuoteMeta(window)), Token: "<WORKING_HOURS>"}))
}

func testContextStartRealRunReusesExistingProfileAssociation(t *testing.T) {
	// Locks the reuse happy path: the instance profile already exists,
	// already carries the role, and its active association already
	// matches, so start reuses all three. Real-run, not dry-run:
	// dry-run short-circuits before invoking aws, so these decisions
	// never see the CLI output they branch on.
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	stubs := setup.Cwd + "/stubs"
	profileARN := "arn:aws:iam::123456789012:instance-profile/erun-edge-host-stop"
	envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
		RoleName:             "erun-edge-host-stop",
		InstanceProfileARN:   profileARN,
		ProfileRoleName:      "erun-edge-host-stop",
		ActiveAssociationID:  "iip-assoc-0aa11bb22cc33dd44",
		ActiveAssociationARN: profileARN,
	})...)
	fixture.StubBinary(t, stubs, "kubectl", "")
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"context", "start", "edge", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/start_real_run_reuses_existing_profile_association", normalize.Apply(result.Combined))
	// Persistence is a side effect outside the captured streams: the
	// refreshed public IP and the resolved instance-profile identity
	// must land in the root config.
	raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
	if err != nil {
		t.Fatalf("read root config: %v", err)
	}
	for _, want := range []string{
		"publicip: 203.0.113.10",
		"instanceprofilename: erun-edge-host-stop",
		"instanceprofilearn: " + profileARN,
		"instancerolename: erun-edge-host-stop",
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("expected persisted config to contain %q, got:\n%s", want, raw)
		}
	}
}

func testContextStartRealRunKubectlContextConfigureLibraryExecutionModeWritesKubeconfigDirectly(t *testing.T) {
	// Proves the library path genuinely never shells out: no kubectl stub
	// is declared at all (the scrubbed PATH holds nothing named kubectl),
	// so a fallback to the subprocess path would fail with "executable
	// file not found" instead of silently passing. The three kubeconfig
	// entries land via k8s.io/client-go/tools/clientcmd against a real
	// KUBECONFIG file this scenario points at, matching what the
	// subprocess-mode goldens above assert via persisted config instead.
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	seedExecutionMode(t, setup, "kubectl-context-configure", "library")
	kubeconfigPath := filepath.Join(setup.Cwd, "kubeconfig")
	stubs := setup.Cwd + "/stubs"
	profileARN := "arn:aws:iam::123456789012:instance-profile/erun-edge-host-stop"
	envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
		RoleName:             "erun-edge-host-stop",
		InstanceProfileARN:   profileARN,
		ProfileRoleName:      "erun-edge-host-stop",
		ActiveAssociationID:  "iip-assoc-0aa11bb22cc33dd44",
		ActiveAssociationARN: profileARN,
	})...)
	envVars = append(envVars, "KUBECONFIG="+kubeconfigPath)
	result := erun.Run(t, []string{"context", "start", "edge", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	raw, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		t.Fatalf("read kubeconfig: %v", err)
	}
	for _, want := range []string{
		"server: https://203.0.113.10:6443",
		"insecure-skip-tls-verify: true",
		"token: dummy-admin-token",
		"name: edge",
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("expected kubeconfig to contain %q, got:\n%s", want, raw)
		}
	}
}

func testContextStartRealRunRecoversAddRoleLimitExceeded(t *testing.T) {
	// Locks the AddRoleToInstanceProfile LimitExceeded recovery: the
	// profile is created fresh (get-role/get-instance-profile answer
	// NoSuchEntity), add-role then fails with LimitExceeded, and recovery
	// confirms the profile already carries the role. Real-run, not
	// dry-run: the classifier branches on the aws CLI's error output.
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	stubs := setup.Cwd + "/stubs"
	profileARN := "arn:aws:iam::123456789012:instance-profile/erun-edge-host-stop"
	envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
		RoleName:           "erun-edge-host-stop",
		InstanceProfileARN: profileARN,
		ProfileRoleName:    "erun-edge-host-stop",
		GetRoleError: &fixture.AWSStubError{
			Stderr: "An error occurred (NoSuchEntity) when calling the GetRole operation: The role with name erun-edge-host-stop cannot be found.",
		},
		GetInstanceProfileError: &fixture.AWSStubError{
			Stderr: "An error occurred (NoSuchEntity) when calling the GetInstanceProfile operation: Instance Profile erun-edge-host-stop cannot be found.",
		},
		AddRoleToInstanceProfileError: &fixture.AWSStubError{
			Stderr: "An error occurred (LimitExceeded) when calling the AddRoleToInstanceProfile operation: Cannot exceed quota for InstanceSessionsPerInstanceProfile: 1",
		},
	})...)
	fixture.StubBinary(t, stubs, "kubectl", "")
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"context", "start", "edge", "--force", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/start_real_run_recovers_add_role_limit_exceeded", normalize.Apply(result.Combined))
}

func testContextStartRealRunRecoversExistingAssociationIncorrectState(t *testing.T) {
	// Locks the associate-iam-instance-profile recovery when AWS answers
	// IncorrectState/"existing association": production absorbs the
	// failed associate call and continues the start. Real-run, not
	// dry-run: the branch keys off the aws CLI's error output.
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	stubs := setup.Cwd + "/stubs"
	profileARN := "arn:aws:iam::123456789012:instance-profile/erun-edge-host-stop"
	envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
		RoleName:           "erun-edge-host-stop",
		InstanceProfileARN: profileARN,
		ProfileRoleName:    "erun-edge-host-stop",
		AssociateInstanceProfileError: &fixture.AWSStubError{
			Stderr: "An error occurred (IncorrectState) when calling the AssociateIamInstanceProfile operation: There is an existing association for instance i-0123456789abcdef0",
		},
	})...)
	fixture.StubBinary(t, stubs, "kubectl", "")
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"context", "start", "edge", "--force", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/start_real_run_recovers_existing_association_incorrect_state", normalize.Apply(result.Combined))
}

func testContextStartRealRunRecoversAlreadyAssociated(t *testing.T) {
	// Same recovery as the IncorrectState scenario but via the
	// "already associated" classifier: the message lacks "existing
	// association", so only the second classifier admits it. Real-run,
	// not dry-run: the branch needs the aws CLI's error output.
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	stubs := setup.Cwd + "/stubs"
	profileARN := "arn:aws:iam::123456789012:instance-profile/erun-edge-host-stop"
	envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
		RoleName:           "erun-edge-host-stop",
		InstanceProfileARN: profileARN,
		ProfileRoleName:    "erun-edge-host-stop",
		AssociateInstanceProfileError: &fixture.AWSStubError{
			Stderr: "An error occurred (InvalidParameterValue) when calling the AssociateIamInstanceProfile operation: Instance i-0123456789abcdef0 is already associated with an IAM instance profile",
		},
	})...)
	fixture.StubBinary(t, stubs, "kubectl", "")
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"context", "start", "edge", "--force", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/start_real_run_recovers_already_associated", normalize.Apply(result.Combined))
}

func testContextStartRealRunRetriesAfterTransitionalState(t *testing.T) {
	// Locks the start-instances retry: the first call is rejected because
	// the instance is still stopping, so production waits for stopped and
	// retries once (the stub's Once flag fires the failure only first),
	// then continues the normal start. Real-run, not dry-run: the
	// recovery branches on the aws CLI's error output.
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	stubs := setup.Cwd + "/stubs"
	profileARN := "arn:aws:iam::123456789012:instance-profile/erun-edge-host-stop"
	envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
		RoleName:             "erun-edge-host-stop",
		InstanceProfileARN:   profileARN,
		ProfileRoleName:      "erun-edge-host-stop",
		ActiveAssociationID:  "iip-assoc-0aa11bb22cc33dd44",
		ActiveAssociationARN: profileARN,
		StartInstancesError: &fixture.AWSStubError{
			Stderr: "An error occurred (IncorrectInstanceState) when calling the StartInstances operation: The instance 'i-0123456789abcdef0' is not in a state from which it can be started.",
			Once:   true,
		},
	})...)
	fixture.StubBinary(t, stubs, "kubectl", "")
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"context", "start", "edge", "--force", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/start_real_run_retries_after_transitional_state", normalize.Apply(result.Combined))
}

func testContextStartRealRunReplacesMismatchedProfileAssociation(t *testing.T) {
	// Locks the association replace: the active association's profile ARN
	// differs from the erun host-stop profile, so production must replace
	// it, not add a second profile or reuse the wrong one. Real-run, not
	// dry-run: the mismatch needs the association ARN only real aws output
	// provides.
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	stubs := setup.Cwd + "/stubs"
	profileARN := "arn:aws:iam::123456789012:instance-profile/erun-edge-host-stop"
	envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
		RoleName:             "erun-edge-host-stop",
		InstanceProfileARN:   profileARN,
		ProfileRoleName:      "erun-edge-host-stop",
		ActiveAssociationID:  "iip-assoc-0aa11bb22cc33dd44",
		ActiveAssociationARN: "arn:aws:iam::123456789012:instance-profile/legacy-profile",
	})...)
	fixture.StubBinary(t, stubs, "kubectl", "")
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"context", "start", "edge", "--force", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/start_real_run_replaces_mismatched_profile_association", normalize.Apply(result.Combined))
}

func testContextStartDryRunUnconfiguredAliasFails(t *testing.T) {
	// The host-stop profile association is skipped with a trace (the
	// non-fatal branch), then the power-state change fails because the
	// alias has no configured provider. Reachable in dry-run: both happen
	// during config resolution, before any AWS call.
	setup := env.New(t)
	seedCloudConfigWithContexts(t, setup,
		contextYAMLItem("edge", "ghost", "us-east-1", "i-0123456789abcdef0"))
	result := erun.Run(t, []string{"context", "start", "edge", "--force", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for unconfigured alias, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "context/start_dry_run_unconfigured_alias_fails", normalize.Apply(result.Combined))
}

func testContextStartRealRunWaitRunningFailureFails(t *testing.T) {
	// Locks the running-wait failure: AWS accepts start-instances but the
	// wait never observes running, so start fails with the waiter's error
	// instead of reporting a running context. Real-run, not dry-run: the
	// wait result only exists in real execution.
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	stubs := setup.Cwd + "/stubs"
	profileARN := "arn:aws:iam::123456789012:instance-profile/erun-edge-host-stop"
	envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
		RoleName:             "erun-edge-host-stop",
		InstanceProfileARN:   profileARN,
		ProfileRoleName:      "erun-edge-host-stop",
		ActiveAssociationID:  "iip-assoc-0aa11bb22cc33dd44",
		ActiveAssociationARN: profileARN,
		WaitError: &fixture.AWSStubError{
			Stderr:   "Waiter InstanceRunning failed: Max attempts exceeded",
			ExitCode: 255,
		},
	})...)
	result := erun.Run(t, []string{"context", "start", "edge", "--force", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when the running wait fails, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "context/start_real_run_wait_running_failure_fails", normalize.Apply(result.Combined))
}

func testContextStartRealRunInsideWorkingHoursGateClears(t *testing.T) {
	// The permitting arm: an attached env inside its working window
	// (00:00-23:59, the all-day span, unconditional including the day's
	// last minute) lets a start without --force pass the gate and run the
	// normal start flow.
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	root := filepath.Join(setup.ConfigHome, "erun")
	tenantDir := filepath.Join(root, "team")
	envDir := filepath.Join(tenantDir, "dev")
	for _, dir := range []string{tenantDir, envDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(tenantDir, "config.yaml"),
		[]byte("name: team\nprojectroot: "+setup.Cwd+"\ndefaultenvironment: dev\n"), 0o644); err != nil {
		t.Fatalf("tenant cfg: %v", err)
	}
	envBody := "name: dev\nrepopath: " + setup.Cwd + "\nkubernetescontext: edge\ncontainerregistry: registry.example/test\nruntimeversion: 1.0.0\nmanagedcloud: true\ncloudprovideralias: dev\nidle:\n  workinghours: 00:00-23:59\n  timezone: UTC\n"
	if err := os.WriteFile(filepath.Join(envDir, "config.yaml"), []byte(envBody), 0o644); err != nil {
		t.Fatalf("env cfg: %v", err)
	}
	stubs := setup.Cwd + "/stubs"
	profileARN := "arn:aws:iam::123456789012:instance-profile/erun-edge-host-stop"
	envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
		RoleName:             "erun-edge-host-stop",
		InstanceProfileARN:   profileARN,
		ProfileRoleName:      "erun-edge-host-stop",
		ActiveAssociationID:  "iip-assoc-0aa11bb22cc33dd44",
		ActiveAssociationARN: profileARN,
	})...)
	fixture.StubBinary(t, stubs, "kubectl", "")
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"context", "start", "edge", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/start_real_run_inside_working_hours_gate_clears", normalize.Apply(result.Combined))
}

// workingHoursWindowOutsideNow returns an HH:MM-HH:MM window, in the clock the
// caller passes, that cannot contain that clock's current minute: it opens two
// minutes ahead and runs for one, so a run that takes a minute to reach the
// gate is still outside it. A window that wraps past midnight is still a valid
// span -- the evaluation treats start > end as the overnight case -- and the
// two ends can never be equal, which is the one shape the policy rejects.
func workingHoursWindowOutsideNow(now time.Time) string {
	start := (now.Hour()*60 + now.Minute() + 2) % (24 * 60)
	end := (start + 1) % (24 * 60)
	return fmt.Sprintf("%02d:%02d-%02d:%02d", start/60, start%60, end/60, end%60)
}
