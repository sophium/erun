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

// seedCloudContextConfig writes a root erun config with a cloud provider
// alias and one cloud context so the context start/stop subcommands have
// state to operate on without prompting or hitting AWS.
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
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"context", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "context/help", normalize.Apply(result.Combined))
	})

	t.Run("list_empty", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"context", "list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "context/list_empty", normalize.Apply(result.Combined))
	})

	t.Run("start_help_lists_force_flag", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"context", "start", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "context/start_help_lists_force_flag", normalize.Apply(result.Combined))
	})

	t.Run("stop_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"context", "stop", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "context/stop_help", normalize.Apply(result.Combined))
	})

	t.Run("init_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"context", "init", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "context/init_help", normalize.Apply(result.Combined))
	})

	t.Run("stop_dry_run_traces_aws_stop_instances", func(t *testing.T) {
		// Exercises eruncommon.StopCloudContext + defaultRunCloudContextAWS
		// dry-run gate: every AWS call goes through ctx.TraceCommand and
		// short-circuits before hitting the real CLI. Asserts the trace
		// shows the would-execute aws ec2 stop-instances invocation.
		setup := env.New(t)
		seedCloudContextConfig(t, setup, "edge")
		result := erun.Run(t, []string{"context", "stop", "edge", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "context/stop_dry_run_traces_aws_stop_instances", normalize.Apply(result.Combined))
	})

	t.Run("start_force_dry_run_traces_aws_start_and_profile_setup", func(t *testing.T) {
		// Exercises eruncommon.StartCloudContext force-bypass branch and
		// the IAM instance-profile association path: dry-run must trace
		// the working-hours bypass note, the aws ec2 start-instances call,
		// the instance-profile association lookup, and the kubectl context
		// configuration that follows once the instance is running.
		setup := env.New(t)
		seedCloudContextConfig(t, setup, "edge")
		result := erun.Run(t, []string{"context", "start", "edge", "--force", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "context/start_force_dry_run_traces_aws_start_and_profile_setup", normalize.Apply(result.Combined))
	})

	t.Run("init_dry_run_traces_aws_security_group_and_run_instances", func(t *testing.T) {
		// Exercises eruncommon.InitCloudContext: with the cloud provider
		// pre-seeded and all init flags supplied, dry-run skips prompts
		// and traces the would-execute aws calls that prepare a security
		// group, fetch the AMI, build the run-instances arguments, and
		// associate the instance profile.
		setup := env.New(t)
		seedCloudContextConfig(t, setup, "preexisting")
		args := []string{
			"context", "init",
			"--alias", "dev",
			"--context", "fresh",
			"--region", "eu-west-2",
			"--instance-type", "c8gd.2xlarge",
			"--disk-size", "100",
			"--dry-run", "-v",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "context/init_dry_run_traces_aws_security_group_and_run_instances", normalize.Apply(result.Combined))
	})

	t.Run("start_without_force_blocks_outside_working_hours", func(t *testing.T) {
		// Exercises eruncommon.cloudContextStartBlockedByWorkingHours: when
		// any attached environment has a working-hours window that excludes
		// `now`, start without --force fails informatively and traces the
		// gated reason. Seeds a tenant env that points to the cloud context
		// with a 1-minute working window so the gate reliably refuses.
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
		// The working-hours window 23:00-23:01 UTC is reliably "outside"
		// for every wall clock minute except a single minute per day, so
		// the gate refuses without --force.
		envBody := "name: dev\nrepopath: " + setup.Cwd + "\nkubernetescontext: edge\ncontainerregistry: registry.example/test\nruntimeversion: 1.0.0\nmanagedcloud: true\ncloudprovideralias: dev\nidle:\n  workinghours: 23:00-23:01\n  timezone: UTC\n"
		if err := os.WriteFile(filepath.Join(envDir, "config.yaml"), []byte(envBody), 0o644); err != nil {
			t.Fatalf("env cfg: %v", err)
		}
		result := erun.Run(t, []string{"context", "start", "edge", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when working-hours gate refuses, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "context/start_without_force_blocks_outside_working_hours", normalize.Apply(result.Combined))
	})

	t.Run("list_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"context", "list", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "context/list_help", normalize.Apply(result.Combined))
	})

	t.Run("disable_api_stop_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"context", "disable-api-stop", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "context/disable_api_stop_help", normalize.Apply(result.Combined))
	})

	t.Run("enable_api_stop_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"context", "enable-api-stop", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "context/enable_api_stop_help", normalize.Apply(result.Combined))
	})

	t.Run("disable_api_stop_dry_run_traces_aws_modify_attribute", func(t *testing.T) {
		// Exercises eruncommon.SetCloudContextStopProtection's lock
		// path. The dry-run trace must show the
		// `aws ec2 modify-instance-attribute --disable-api-stop` call
		// without --no-disable-api-stop appearing — that pair makes
		// the integration golden the public contract for the "AWS
		// rejects every stop until I unlock it" recovery lever.
		setup := env.New(t)
		seedCloudContextConfig(t, setup, "edge")
		result := erun.Run(t, []string{"context", "disable-api-stop", "edge", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "context/disable_api_stop_dry_run_traces_aws_modify_attribute", normalize.Apply(result.Combined))
	})

	t.Run("start_real_run_reuses_existing_profile_association", func(t *testing.T) {
		// Real-run start happy path via the argv-branching aws stub:
		// covers the realCloudContextInstanceProfile chain (get-role hit,
		// instance profile already exists and already carries the role),
		// the active-association short-circuit in
		// ensureCloudContextInstanceProfileAssociation
		// (profileRefMatchesAssociation against the association ARN), and
		// the post-start persistence (saveCloudContextConfig +
		// upsertCloudContext writing the refreshed PublicIP). Dry-run
		// cannot reach these branches: defaultRunCloudContextAWS
		// short-circuits before invoking aws, so the real code never sees
		// the CLI outputs these decisions branch on. -vv locks the aws
		// argv sequence in the golden.
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
	})

	t.Run("start_real_run_recovers_add_role_limit_exceeded", func(t *testing.T) {
		// Unlocks ensureCloudContextInstanceProfileRole's LimitExceeded
		// recovery (isInstanceProfileRoleLimitError +
		// cloudContextInstanceProfileRoleName re-check) plus the
		// create-role/create-instance-profile arms of
		// realCloudContextInstanceProfile: get-role and
		// get-instance-profile fail with NoSuchEntity so the profile is
		// created fresh, then add-role-to-instance-profile fails with
		// LimitExceeded and the recovery confirms the profile already
		// carries the expected role. Dry-run cannot reach this: the
		// classifier branches on the aws CLI's error output, which
		// dry-run never produces. The golden locks the recovery trace
		// "instance profile ... already carries a role".
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
	})

	t.Run("start_real_run_recovers_existing_association_incorrect_state", func(t *testing.T) {
		// Unlocks the associate-iam-instance-profile recovery branch in
		// ensureCloudContextInstanceProfileAssociation when AWS answers
		// IncorrectState/"existing association"
		// (isExistingInstanceProfileAssociationError). The stub reports no
		// active or pending association, then fails the associate call;
		// production must absorb the failure and continue the start.
		// Dry-run cannot reach this: the branch keys off the aws CLI's
		// error output. The golden locks the recovery trace "instance
		// profile already associated with i-... — reusing the existing
		// association".
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
	})

	t.Run("start_real_run_recovers_already_associated", func(t *testing.T) {
		// Same recovery branch as the IncorrectState scenario but through
		// the isAlreadyAssociatedAWSError classifier: the associate call
		// fails with an "... is already associated ..." message that does
		// not contain "existing association", so only the second
		// classifier admits it. Dry-run cannot reach this for the same
		// reason — the branch needs the aws CLI's error output.
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
	})

	t.Run("init_real_run_reuses_duplicate_security_group_and_ingress", func(t *testing.T) {
		// Unlocks createCloudContextSecurityGroup's two duplicate
		// recoveries in one real run: create-security-group fails with
		// InvalidGroup.Duplicate (isDuplicateSecurityGroupError →
		// describeCloudContextSecurityGroupID fallback lookup) and
		// authorize-security-group-ingress fails with
		// InvalidPermission.Duplicate
		// (isDuplicateSecurityGroupPermissionError). Also drives the full
		// real InitCloudContext body: AMI lookup, run-instances, the
		// instance-running wait, kube-context configuration, and the
		// saveCloudContextConfig append alongside the preexisting context.
		// Dry-run cannot reach the duplicate branches: they key off the
		// aws CLI's error output. The golden locks both recovery traces
		// ("security group ... already exists — reusing it" and "k3s API
		// ingress rule already present on ... — reusing it").
		setup := env.New(t)
		seedCloudContextConfig(t, setup, "preexisting")
		stubs := setup.Cwd + "/stubs"
		envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
			RoleName:           "erun-fresh-host-stop",
			InstanceProfileARN: "arn:aws:iam::123456789012:instance-profile/erun-fresh-host-stop",
			ProfileRoleName:    "erun-fresh-host-stop",
			SecurityGroupID:    "sg-0123456789abcdef0",
			InstanceID:         "i-0aaaabbbbcccc1111",
			CreateSecurityGroupError: &fixture.AWSStubError{
				Stderr: "An error occurred (InvalidGroup.Duplicate) when calling the CreateSecurityGroup operation: The security group 'fresh-k3s' already exists for VPC 'vpc-0123456789abcdef0'",
			},
			AuthorizeIngressError: &fixture.AWSStubError{
				Stderr: "An error occurred (InvalidPermission.Duplicate) when calling the AuthorizeSecurityGroupIngress operation: the specified rule (peer: 0.0.0.0/0, TCP, from port: 6443, to port: 6443, ALLOW) already exists",
			},
		})...)
		fixture.StubBinary(t, stubs, "kubectl", "")
		envVars = append(envVars, fixture.StubEnv(stubs, "kubectl")...)
		args := []string{
			"context", "init",
			"--alias", "dev",
			"--context", "fresh",
			"--region", "eu-west-2",
			"--instance-type", "c8gd.2xlarge",
			"--disk-size", "100",
			"-vv",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "context/init_real_run_reuses_duplicate_security_group_and_ingress", normalize.Apply(result.Combined))
		// Persistence is a side effect outside the captured streams: the
		// new context must be appended next to the preexisting one with
		// the recovered security group and the launched instance.
		raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
		if err != nil {
			t.Fatalf("read root config: %v", err)
		}
		for _, want := range []string{
			"name: fresh",
			"name: preexisting",
			"securitygroupid: sg-0123456789abcdef0",
			"instanceid: i-0aaaabbbbcccc1111",
		} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("expected persisted config to contain %q, got:\n%s", want, raw)
			}
		}
	})

	t.Run("stop_real_run_recovers_incorrect_instance_state", func(t *testing.T) {
		// Unlocks StopCloudContext's IncorrectInstanceState absorption
		// (isAWSIncorrectInstanceStateError +
		// resolveCloudContextStatusForName): stop-instances is rejected
		// because the instance is already stopping, and production must
		// fall through to the instance-stopped wait instead of failing.
		// Dry-run cannot reach this: the branch keys off the aws CLI's
		// error output. The golden locks the recovery trace "instance is
		// already in a non-running state — waiting for stopped".
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
	})

	t.Run("stop_real_run_blocked_by_stop_protection", func(t *testing.T) {
		// Unlocks classifyCloudContextPowerError's OperationNotPermitted
		// branch: stop-instances is rejected because DisableApiStop is
		// set, and the user-facing error must name the unlock command
		// (`erun context enable-api-stop edge`) instead of surfacing a
		// bare AWS exit 1 (issue #456). Dry-run cannot reach this: the
		// classifier branches on the aws CLI's error output.
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
	})

	t.Run("stop_real_run_expired_credentials", func(t *testing.T) {
		// Unlocks classifyCloudContextPowerError's expired-credentials
		// branch (isAWSExpiredCredentialsError): stop-instances fails with
		// ExpiredToken and the user-facing error must point at `erun
		// cloud login --alias dev` instead of the raw AWS message.
		// Dry-run cannot reach this: the classifier branches on the aws
		// CLI's error output.
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
	})

	t.Run("enable_api_stop_dry_run_traces_aws_modify_attribute", func(t *testing.T) {
		// Exercises eruncommon.SetCloudContextStopProtection's unlock
		// path. The dry-run trace must show
		// `--no-disable-api-stop`, not `--disable-api-stop`, so the
		// reverse operation is unambiguous in audit output.
		setup := env.New(t)
		seedCloudContextConfig(t, setup, "edge")
		result := erun.Run(t, []string{"context", "enable-api-stop", "edge", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "context/enable_api_stop_dry_run_traces_aws_modify_attribute", normalize.Apply(result.Combined))
	})
}
