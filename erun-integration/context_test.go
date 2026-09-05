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
		setup := env.New(t)
		seedCloudContextConfig(t, setup, "edge")
		result := erun.Run(t, []string{"context", "stop", "edge", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "context/stop_dry_run_traces_aws_stop_instances", normalize.Apply(result.Combined))
	})

	t.Run("start_force_dry_run_traces_aws_start_and_profile_setup", func(t *testing.T) {
		setup := env.New(t)
		seedCloudContextConfig(t, setup, "edge")
		result := erun.Run(t, []string{"context", "start", "edge", "--force", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "context/start_force_dry_run_traces_aws_start_and_profile_setup", normalize.Apply(result.Combined))
	})

	t.Run("init_dry_run_traces_aws_security_group_and_run_instances", func(t *testing.T) {
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
	})

	t.Run("start_real_run_reuses_existing_profile_association", func(t *testing.T) {
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
	})

	t.Run("start_real_run_kubectl_context_configure_library_execution_mode_writes_kubeconfig_directly", func(t *testing.T) {
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
	})

	t.Run("start_real_run_recovers_add_role_limit_exceeded", func(t *testing.T) {
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
	})

	t.Run("start_real_run_recovers_existing_association_incorrect_state", func(t *testing.T) {
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
	})

	t.Run("start_real_run_recovers_already_associated", func(t *testing.T) {
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
	})

	t.Run("init_real_run_reuses_duplicate_security_group_and_ingress", func(t *testing.T) {
		// Locks two duplicate recoveries in one run: create-security-group
		// answered InvalidGroup.Duplicate (fall back to a lookup) and
		// authorize-ingress answered InvalidPermission.Duplicate (reuse the
		// existing rule). Real-run, not dry-run: both branches key off the
		// aws CLI's error output.
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
	})

	t.Run("stop_real_run_blocked_by_stop_protection", func(t *testing.T) {
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
	})

	t.Run("stop_real_run_expired_credentials", func(t *testing.T) {
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
	})

	t.Run("enable_api_stop_dry_run_traces_aws_modify_attribute", func(t *testing.T) {
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
	})

	t.Run("init_dry_run_generates_sequential_context_name", func(t *testing.T) {
		// Without --context the name derives from account id + region and the
		// counter advances past the highest existing erun-NNN context: the
		// seeded erun-001 forces erun-002, and the unrelated "edge" context
		// covers the non-matching-name skip.
		setup := env.New(t)
		seedCloudConfigWithContexts(t, setup,
			"  - name: erun-001-123456789012-eu-west-2\n"+
				"    provider: aws\n"+
				"    cloudprovideralias: dev\n"+
				"    region: eu-west-2\n"+
				"    instanceid: i-0123456789abcdef0\n"+
				"    kubernetescontext: erun-001-123456789012-eu-west-2\n"+
				"  - name: edge\n"+
				"    provider: aws\n"+
				"    cloudprovideralias: dev\n"+
				"    region: us-east-1\n"+
				"    instanceid: i-0aaaa56789abcdef0\n"+
				"    kubernetescontext: edge\n")
		result := erun.Run(t, []string{"context", "init", "--alias", "dev", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "erun-002-123456789012-eu-west-2") {
			t.Errorf("expected generated context name erun-002-123456789012-eu-west-2, got:\n%s", result.Combined)
		}
		golden.Equal(t, "context/init_dry_run_generates_sequential_context_name", normalize.Apply(result.Combined))
	})

	t.Run("init_dry_run_rejects_unsupported_region", func(t *testing.T) {
		setup := env.New(t)
		seedCloudContextConfig(t, setup, "edge")
		result := erun.Run(t, []string{"context", "init", "--alias", "dev", "--context", "fresh", "--region", "mars-north-1", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for unsupported region, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "context/init_dry_run_rejects_unsupported_region", normalize.Apply(result.Combined))
	})

	t.Run("init_dry_run_rejects_unsupported_instance_type", func(t *testing.T) {
		setup := env.New(t)
		seedCloudContextConfig(t, setup, "edge")
		result := erun.Run(t, []string{"context", "init", "--alias", "dev", "--context", "fresh", "--instance-type", "m1.tiny", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for unsupported instance type, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "context/init_dry_run_rejects_unsupported_instance_type", normalize.Apply(result.Combined))
	})

	t.Run("init_dry_run_rejects_unsupported_disk_size", func(t *testing.T) {
		setup := env.New(t)
		seedCloudContextConfig(t, setup, "edge")
		result := erun.Run(t, []string{"context", "init", "--alias", "dev", "--context", "fresh", "--disk-size", "13", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for unsupported disk size, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "context/init_dry_run_rejects_unsupported_disk_size", normalize.Apply(result.Combined))
	})

	t.Run("init_dry_run_rejects_unsupported_disk_type", func(t *testing.T) {
		setup := env.New(t)
		seedCloudContextConfig(t, setup, "edge")
		result := erun.Run(t, []string{"context", "init", "--alias", "dev", "--context", "fresh", "--disk-type", "io2", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for unsupported disk type, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "context/init_dry_run_rejects_unsupported_disk_type", normalize.Apply(result.Combined))
	})

	t.Run("list_real_run_reports_live_instance_states", func(t *testing.T) {
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
	})

	t.Run("list_real_run_refresh_failures_mark_unknown", func(t *testing.T) {
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
	})

	t.Run("start_real_run_retries_after_transitional_state", func(t *testing.T) {
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
	})

	t.Run("start_real_run_replaces_mismatched_profile_association", func(t *testing.T) {
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
	})

	t.Run("start_dry_run_unconfigured_alias_fails", func(t *testing.T) {
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
	})

	t.Run("stop_real_run_wait_failure_reports_transitioning", func(t *testing.T) {
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
	})

	t.Run("init_real_run_retries_iam_consistency_visibility", func(t *testing.T) {
		// Locks the IAM-consistency retry: a freshly created instance profile
		// is not yet visible to EC2, so the first run-instances fails with
		// "Invalid IAM Instance Profile name" and production backs off and
		// retries. Real-run, not dry-run: the classifier branches on the aws
		// CLI's error output. Costs one real 2s backoff sleep.
		setup := env.New(t)
		seedCloudContextConfig(t, setup, "preexisting")
		stubs := setup.Cwd + "/stubs"
		envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
			RoleName:           "erun-fresh-host-stop",
			InstanceProfileARN: "arn:aws:iam::123456789012:instance-profile/erun-fresh-host-stop",
			ProfileRoleName:    "erun-fresh-host-stop",
			InstanceID:         "i-0aaaabbbbcccc1111",
			RunInstancesError: &fixture.AWSStubError{
				Stderr: "An error occurred (InvalidParameterValue) when calling the RunInstances operation: Invalid IAM Instance Profile name",
				Once:   true,
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
		golden.Equal(t, "context/init_real_run_retries_iam_consistency_visibility", normalize.Apply(result.Combined))
	})
	t.Run("stop_unknown_context_fails", func(t *testing.T) {
		setup := env.New(t)
		seedCloudContextConfig(t, setup, "edge")
		result := erun.Run(t, []string{"context", "stop", "ghost", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for unknown context, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "context/stop_unknown_context_fails", normalize.Apply(result.Combined))
	})

	t.Run("disable_api_stop_without_instance_fails", func(t *testing.T) {
		setup := env.New(t)
		seedCloudConfigWithContexts(t, setup, contextYAMLItem("edge", "dev", "us-east-1", ""))
		result := erun.Run(t, []string{"context", "disable-api-stop", "edge", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a context without an instance, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "context/disable_api_stop_without_instance_fails", normalize.Apply(result.Combined))
	})

	t.Run("init_dry_run_existing_security_group_skips_creation", func(t *testing.T) {
		setup := env.New(t)
		seedCloudContextConfig(t, setup, "preexisting")
		args := []string{
			"context", "init",
			"--alias", "dev",
			"--context", "fresh",
			"--security-group-id", "sg-0aaaa11111aaaa1111",
			"--dry-run", "-v",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if strings.Contains(result.Combined, "create-security-group") {
			t.Errorf("expected no security-group creation with an explicit --security-group-id, got:\n%s", result.Combined)
		}
		golden.Equal(t, "context/init_dry_run_existing_security_group_skips_creation", normalize.Apply(result.Combined))
	})

	t.Run("init_real_run_security_group_failure_fails", func(t *testing.T) {
		// Locks the non-duplicate security-group failure: a
		// create-security-group rejection that is not InvalidGroup.Duplicate
		// must abort the init. Real-run, not dry-run: the classifier branches
		// on the aws CLI's error output.
		setup := env.New(t)
		seedCloudContextConfig(t, setup, "preexisting")
		stubs := setup.Cwd + "/stubs"
		envVars := append(setup.Env(), fixture.StubAWSCloudContext(t, stubs, fixture.AWSCloudContextStubSpec{
			CreateSecurityGroupError: &fixture.AWSStubError{
				Stderr: "An error occurred (UnauthorizedOperation) when calling the CreateSecurityGroup operation: You are not authorized to perform this operation.",
			},
		})...)
		args := []string{
			"context", "init",
			"--alias", "dev",
			"--context", "fresh",
			"-vv",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when security group creation fails, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "context/init_real_run_security_group_failure_fails", normalize.Apply(result.Combined))
	})

	t.Run("start_real_run_wait_running_failure_fails", func(t *testing.T) {
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
	})

	t.Run("start_real_run_inside_working_hours_gate_clears", func(t *testing.T) {
		// The permitting arm: an attached env inside its working window
		// (00:00-23:59, all but one minute per day) lets a start without
		// --force pass the gate and run the normal start flow.
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
	})

	t.Run("init_dry_run_generates_name_from_username", func(t *testing.T) {
		setup := env.New(t)
		root := filepath.Join(setup.ConfigHome, "erun")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", root, err)
		}
		body := "cloudproviders:\n" +
			"  - alias: dev\n" +
			"    provider: aws\n" +
			"    username: ops\n" +
			"    profile: dev\n"
		if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(body), 0o644); err != nil {
			t.Fatalf("write cloud config: %v", err)
		}
		result := erun.Run(t, []string{"context", "init", "--alias", "dev", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Combined, "erun-001-ops-eu-west-2") {
			t.Errorf("expected generated context name erun-001-ops-eu-west-2, got:\n%s", result.Combined)
		}
		golden.Equal(t, "context/init_dry_run_generates_name_from_username", normalize.Apply(result.Combined))
	})

	t.Run("init_dry_run_unaffected_by_kubectl_context_configure_library_execution_mode", func(t *testing.T) {
		// Locks the dry-run/audit contract: configureCloudKubeContext only
		// ever traces the three kubectl config lines and returns before
		// checking the execution mode, so flipping
		// execution.modes.kubectl-context-configure to library must not
		// change dry-run output at all -- reuses
		// init_dry_run_generates_name_from_username's golden byte-for-byte.
		setup := env.New(t)
		root := filepath.Join(setup.ConfigHome, "erun")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", root, err)
		}
		body := "cloudproviders:\n" +
			"  - alias: dev\n" +
			"    provider: aws\n" +
			"    username: ops\n" +
			"    profile: dev\n"
		if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(body), 0o644); err != nil {
			t.Fatalf("write cloud config: %v", err)
		}
		seedExecutionMode(t, setup, "kubectl-context-configure", "library")
		result := erun.Run(t, []string{"context", "init", "--alias", "dev", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "context/init_dry_run_generates_name_from_username", normalize.Apply(result.Combined))
	})
}

// contextYAMLItem renders one cloudcontexts YAML item; an empty instanceID
// omits the instanceid key so refresh scenarios can stage a context the AWS
// refresh must skip.
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
