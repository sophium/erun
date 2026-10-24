package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func seedCloudContextConfig(t testing.TB, setup env.Setup, contextName string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	body := strings.Join([]string{
		"cloudproviders:",
		"  - alias: dev",
		"    provider: aws",
		"    accountid: \"123456789012\"",
		"    profile: dev",
		"cloudcontexts:",
		"  - name: " + contextName,
		"    provider: aws",
		"    cloudprovideralias: dev",
		"    region: us-east-1",
		"    instanceid: i-0123456789abcdef0",
		"    instancetype: t3.medium",
		"    disktype: gp3",
		"    disksizegb: 50",
		"    kubernetescontext: " + contextName,
		"    admintoken: dummy-admin-token",
		"    status: running",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write cloud config: %v", err)
	}
}

func TestContext(t *testing.T) {
	t.Parallel()
	t.Run("help", testContextHelp)
	t.Run("list_empty", testContextListEmpty)
	t.Run("start_help_lists_force_flag", testContextStartHelpListsForceFlag)
	t.Run("stop_help", testContextStopHelp)
	t.Run("init_help", testContextInitHelp)
	t.Run("stop_dry_run_traces_aws_stop_instances", testContextStopDryRunTracesAWSStopInstances)
	t.Run("start_force_dry_run_traces_aws_start_and_profile_setup", testContextStartForceDryRunTracesAWSStartAndProfileSetup)
	t.Run("init_dry_run_traces_aws_security_group_and_run_instances", testContextInitDryRunTracesAWSSecurityGroupAndRunInstances)
	t.Run("start_without_force_blocks_outside_working_hours", testContextStartWithoutForceBlocksOutsideWorkingHours)
	t.Run("list_help", testContextListHelp)
	t.Run("disable_api_stop_help", testContextDisableAPIStopHelp)
	t.Run("enable_api_stop_help", testContextEnableAPIStopHelp)
	t.Run("disable_api_stop_dry_run_traces_aws_modify_attribute", testContextDisableAPIStopDryRunTracesAWSModifyAttribute)
	t.Run("start_real_run_reuses_existing_profile_association", testContextStartRealRunReusesExistingProfileAssociation)
	t.Run("start_real_run_kubectl_context_configure_library_execution_mode_writes_kubeconfig_directly", testContextStartRealRunKubectlContextConfigureLibraryExecutionModeWritesKubeconfigDirectly)
	t.Run("start_real_run_recovers_add_role_limit_exceeded", testContextStartRealRunRecoversAddRoleLimitExceeded)
	t.Run("start_real_run_recovers_existing_association_incorrect_state", testContextStartRealRunRecoversExistingAssociationIncorrectState)
	t.Run("start_real_run_recovers_already_associated", testContextStartRealRunRecoversAlreadyAssociated)
	t.Run("init_real_run_reuses_duplicate_security_group_and_ingress", testContextInitRealRunReusesDuplicateSecurityGroupAndIngress)
	t.Run("stop_real_run_recovers_incorrect_instance_state", testContextStopRealRunRecoversIncorrectInstanceState)
	t.Run("stop_real_run_blocked_by_stop_protection", testContextStopRealRunBlockedByStopProtection)
	t.Run("stop_real_run_expired_credentials", testContextStopRealRunExpiredCredentials)
	t.Run("enable_api_stop_dry_run_traces_aws_modify_attribute", testContextEnableAPIStopDryRunTracesAWSModifyAttribute)
	t.Run("init_dry_run_generates_sequential_context_name", testContextInitDryRunGeneratesSequentialContextName)
	t.Run("init_dry_run_rejects_unsupported_region", testContextInitDryRunRejectsUnsupportedRegion)
	t.Run("init_dry_run_rejects_unsupported_instance_type", testContextInitDryRunRejectsUnsupportedInstanceType)
	t.Run("init_dry_run_rejects_unsupported_disk_size", testContextInitDryRunRejectsUnsupportedDiskSize)
	t.Run("init_dry_run_rejects_unsupported_disk_type", testContextInitDryRunRejectsUnsupportedDiskType)
	t.Run("list_real_run_reports_live_instance_states", testContextListRealRunReportsLiveInstanceStates)
	t.Run("list_real_run_refresh_failures_mark_unknown", testContextListRealRunRefreshFailuresMarkUnknown)
	t.Run("list_real_run_reports_a_batched_refresh_failure_once", testContextListRealRunReportsABatchedRefreshFailureOnce)
	t.Run("start_real_run_retries_after_transitional_state", testContextStartRealRunRetriesAfterTransitionalState)
	t.Run("start_real_run_replaces_mismatched_profile_association", testContextStartRealRunReplacesMismatchedProfileAssociation)
	t.Run("start_dry_run_unconfigured_alias_fails", testContextStartDryRunUnconfiguredAliasFails)
	t.Run("stop_real_run_wait_failure_reports_transitioning", testContextStopRealRunWaitFailureReportsTransitioning)
	t.Run("init_real_run_retries_iam_consistency_visibility", testContextInitRealRunRetriesIamConsistencyVisibility)
	t.Run("stop_unknown_context_fails", testContextStopUnknownContextFails)
	t.Run("disable_api_stop_without_instance_fails", testContextDisableAPIStopWithoutInstanceFails)
	t.Run("init_dry_run_existing_security_group_skips_creation", testContextInitDryRunExistingSecurityGroupSkipsCreation)
	t.Run("init_real_run_security_group_failure_fails", testContextInitRealRunSecurityGroupFailureFails)
	t.Run("start_real_run_wait_running_failure_fails", testContextStartRealRunWaitRunningFailureFails)
	t.Run("start_real_run_inside_working_hours_gate_clears", testContextStartRealRunInsideWorkingHoursGateClears)
	t.Run("init_dry_run_generates_name_from_username", testContextInitDryRunGeneratesNameFromUsername)
	t.Run("init_dry_run_unaffected_by_kubectl_context_configure_library_execution_mode", testContextInitDryRunUnaffectedByKubectlContextConfigureLibraryExecutionMode)
}

func testContextHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"context", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/help", normalize.Apply(result.Combined))
}

func testContextListEmpty(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"context", "list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "context/list_empty", normalize.Apply(result.Combined))
}

func testContextStopHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"context", "stop", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/stop_help", normalize.Apply(result.Combined))
}

func testContextStopDryRunTracesAWSStopInstances(t *testing.T) {
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	result := erun.Run(t, []string{"context", "stop", "edge", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/stop_dry_run_traces_aws_stop_instances", normalize.Apply(result.Combined))
}

func testContextListHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"context", "list", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/list_help", normalize.Apply(result.Combined))
}

func testContextDisableAPIStopHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"context", "disable-api-stop", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/disable_api_stop_help", normalize.Apply(result.Combined))
}

func testContextEnableAPIStopHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"context", "enable-api-stop", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/enable_api_stop_help", normalize.Apply(result.Combined))
}

func testContextDisableAPIStopDryRunTracesAWSModifyAttribute(t *testing.T) {
	// The lock direction: --disable-api-stop must appear and
	// --no-disable-api-stop must not, so the golden pins the audit
	// contract for the "AWS rejects every stop until I unlock it" lever.
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	result := erun.Run(t, []string{"context", "disable-api-stop", "edge", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/disable_api_stop_dry_run_traces_aws_modify_attribute", normalize.Apply(result.Combined))
}

func testContextStopRealRunRecoversIncorrectInstanceState(t *testing.T) {
	// Locks the IncorrectInstanceState absorption: stop-instances is
	// rejected because the instance is already stopping, so production
	// falls through to the stopped-wait instead of failing. Real-run,
	// not dry-run: the branch keys off the aws CLI's error output.
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	stubs := setup.Cwd + "/stubs"
	envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
		StopInstancesError: &fixture.AWSStubError{
			Stderr: "An error occurred (IncorrectInstanceState) when calling the StopInstances operation: This instance 'i-0123456789abcdef0' is not in a state from which it can be stopped.",
		},
	})...)
	result := erun.Run(t, []string{"context", "stop", "edge", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/stop_real_run_recovers_incorrect_instance_state", normalize.Apply(result.Combined))
}

func testContextStopRealRunBlockedByStopProtection(t *testing.T) {
	// Locks the stop-protection block: when DisableApiStop is set the
	// user-facing error must name the unlock command
	// (`erun context enable-api-stop edge`), not a bare AWS exit 1.
	// Real-run, not dry-run: the classifier branches on aws error output.
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	stubs := setup.Cwd + "/stubs"
	envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
		StopInstancesError: &fixture.AWSStubError{
			Stderr: "An error occurred (OperationNotPermitted) when calling the StopInstances operation: The instance 'i-0123456789abcdef0' may not be stopped because it has stop protection enabled.",
		},
	})...)
	result := erun.Run(t, []string{"context", "stop", "edge", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when stop protection blocks the stop, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "context/stop_real_run_blocked_by_stop_protection", normalize.Apply(result.Combined))
}

func testContextStopRealRunExpiredCredentials(t *testing.T) {
	// Locks the expired-credentials block: ExpiredToken must surface as
	// `erun cloud login --alias dev`, not the raw AWS message. Real-run,
	// not dry-run: the classifier branches on aws error output.
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	stubs := setup.Cwd + "/stubs"
	envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
		StopInstancesError: &fixture.AWSStubError{
			Stderr: "An error occurred (ExpiredToken) when calling the StopInstances operation: The security token included in the request is expired",
		},
	})...)
	result := erun.Run(t, []string{"context", "stop", "edge", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when credentials are expired, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "context/stop_real_run_expired_credentials", normalize.Apply(result.Combined))
}

func testContextEnableAPIStopDryRunTracesAWSModifyAttribute(t *testing.T) {
	// The unlock direction: --no-disable-api-stop, not
	// --disable-api-stop, so the reverse operation is unambiguous in
	// audit output.
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	result := erun.Run(t, []string{"context", "enable-api-stop", "edge", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/enable_api_stop_dry_run_traces_aws_modify_attribute", normalize.Apply(result.Combined))
}

func testContextListRealRunReportsLiveInstanceStates(t *testing.T) {
	// Locks the state mapping from one grouped describe-instances call:
	// running, pending, stopped, an unknown state ("terminated") carrying
	// its raw message, a missing instance ("not found in AWS"), and a
	// no-instance-id context that is skipped. Real-run, not dry-run: the
	// parse branches on real aws stdout.
	setup := env.New(t)
	seedCloudConfigWithContexts(t, setup,
		contextYAMLItem("ctx-a", "dev", "us-east-1", "i-0aaaa11111aaaa1111")+
			contextYAMLItem("ctx-b", "dev", "us-east-1", "i-0bbbb22222bbbb2222")+
			contextYAMLItem("ctx-c", "dev", "us-east-1", "i-0cccc33333cccc3333")+
			contextYAMLItem("ctx-d", "dev", "us-east-1", "i-0dddd44444dddd4444")+
			contextYAMLItem("ctx-e", "dev", "us-east-1", "")+
			contextYAMLItem("ctx-f", "dev", "us-east-1", "i-0ffff55555ffff5555"))
	stubs := setup.Cwd + "/stubs"
	envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
		InstanceStates: "i-0aaaa11111aaaa1111\trunning\n" +
			"i-0bbbb22222bbbb2222\tpending\n" +
			"i-0cccc33333cccc3333\tterminated\n" +
			"i-0ffff55555ffff5555\tstopped",
	})...)
	result := erun.Run(t, []string{"context", "list", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/list_real_run_reports_live_instance_states", normalize.Apply(result.Combined))
}

func testContextListRealRunRefreshFailuresMarkUnknown(t *testing.T) {
	// Both refresh-failure families — an alias with no configured
	// provider, and a describe-instances call that itself fails — must
	// downgrade to status=Unknown carrying the failure, never a stale
	// cached state.
	setup := env.New(t)
	seedCloudConfigWithContexts(t, setup,
		contextYAMLItem("edge", "dev", "us-east-1", "i-0aaaa11111aaaa1111")+
			contextYAMLItem("ghost-ctx", "ghost", "us-east-1", "i-0bbbb22222bbbb2222"))
	stubs := setup.Cwd + "/stubs"
	envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
		DescribeInstanceStatesError: &fixture.AWSStubError{
			Stderr: "An error occurred (ExpiredToken) when calling the DescribeInstances operation: The security token included in the request is expired",
		},
	})...)
	result := erun.Run(t, []string{"context", "list", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/list_real_run_refresh_failures_mark_unknown", normalize.Apply(result.Combined))
}

func testContextListRealRunReportsABatchedRefreshFailureOnce(t *testing.T) {
	// One describe-instances call covers every context sharing its
	// (alias, region), and its failure is one fact about that batch. The
	// operator must read it once, at the batch level -- not once per
	// context, where each row would also re-quote the several-hundred-
	// character command naming every sibling context's instance ID.
	setup := env.New(t)
	seedCloudConfigWithContexts(t, setup,
		contextYAMLItem("ctx-a", "dev", "us-east-1", "i-0aaaa11111aaaa1111")+
			contextYAMLItem("ctx-b", "dev", "us-east-1", "i-0bbbb22222bbbb2222")+
			contextYAMLItem("ctx-c", "dev", "us-east-1", "i-0cccc33333cccc3333")+
			contextYAMLItem("ctx-d", "dev", "us-east-1", "i-0dddd44444dddd4444"))
	stubs := setup.Cwd + "/stubs"
	envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
		DescribeInstanceStatesError: &fixture.AWSStubError{
			Stderr: "An error occurred (ExpiredToken) when calling the DescribeInstances operation: The security token included in the request is expired",
		},
	})...)
	result := erun.Run(t, []string{"context", "list", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// The state classification stays honest -- the refresh could not
	// determine the state, so it reports unknown rather than guessing.
	if got := strings.Count(result.Combined, "status refresh failed"); got != 1 {
		t.Fatalf("status refresh failure appears %d times, want exactly once:\n%s", got, result.Combined)
	}
	for _, row := range contextListRows(result.Combined) {
		if strings.Contains(row, "message=") {
			t.Fatalf("row repeats the shared cause instead of leaving it to the batch line:\n%s", row)
		}
	}
	golden.Equal(t, "context/list_real_run_reports_a_batched_refresh_failure_once", normalize.Apply(result.Combined))
}

func testContextStopRealRunWaitFailureReportsTransitioning(t *testing.T) {
	// Locks the post-stop wait failure: AWS accepts stop-instances but the
	// stopped-wait exhausts its attempts, and the error must say the stop
	// was accepted yet not observed stopped — "stopped" is only reported
	// once AWS observes it. Real-run, not dry-run: the wait result only
	// exists in real execution.
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	stubs := setup.Cwd + "/stubs"
	envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
		WaitError: &fixture.AWSStubError{
			Stderr:   "Waiter InstanceStopped failed: Max attempts exceeded",
			ExitCode: 255,
		},
	})...)
	result := erun.Run(t, []string{"context", "stop", "edge", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when the stopped wait fails, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "context/stop_real_run_wait_failure_reports_transitioning", normalize.Apply(result.Combined))
}

func testContextStopUnknownContextFails(t *testing.T) {
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	result := erun.Run(t, []string{"context", "stop", "ghost", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for unknown context, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "context/stop_unknown_context_fails", normalize.Apply(result.Combined))
}

func testContextDisableAPIStopWithoutInstanceFails(t *testing.T) {
	setup := env.New(t)
	seedCloudConfigWithContexts(t, setup, contextYAMLItem("edge", "dev", "us-east-1", ""))
	result := erun.Run(t, []string{"context", "disable-api-stop", "edge", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a context without an instance, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "context/disable_api_stop_without_instance_fails", normalize.Apply(result.Combined))
}

// contextYAMLItem renders one cloudcontexts YAML item; an empty instanceID
// omits the instanceid key so refresh scenarios can stage a context the AWS
// refresh must skip.
// contextListRows returns only the per-context rows of a context listing, so a
// batch-level line that legitimately names the whole batch's instance IDs is
// not mistaken for a row disclosing its siblings.
func contextListRows(out string) []string {
	var rows []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  - ") {
			rows = append(rows, line)
		}
	}
	return rows
}

func contextYAMLItem(name, alias, region, instanceID string) string {
	item := "  - name: " + name + "\n" +
		"    provider: aws\n" +
		"    cloudprovideralias: " + alias + "\n" +
		"    region: " + region + "\n"
	if instanceID != "" {
		item += "    instanceid: " + instanceID + "\n"
	}
	item += "    kubernetescontext: " + name + "\n"
	item += "    admintoken: dummy-admin-token\n"
	return item
}

// seedCloudConfigWithContexts stages the "dev" AWS provider plus arbitrary raw
// cloudcontexts YAML, so refresh and naming scenarios can express context
// shapes that seedCloudContextConfig's fixed single-context layout cannot.
func seedCloudConfigWithContexts(t testing.TB, setup env.Setup, contextsYAML string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	body := "cloudproviders:\n" +
		"  - alias: dev\n" +
		"    provider: aws\n" +
		"    accountid: \"123456789012\"\n" +
		"    profile: dev\n" +
		"cloudcontexts:\n" +
		contextsYAML
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write cloud config: %v", err)
	}
}
