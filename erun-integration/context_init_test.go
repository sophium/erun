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

func testContextInitHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"context", "init", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "context/init_help", normalize.Apply(result.Combined))
}

func testContextInitDryRunTracesAWSSecurityGroupAndRunInstances(t *testing.T) {
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
}

func testContextInitRealRunReusesDuplicateSecurityGroupAndIngress(t *testing.T) {
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
}

func testContextInitDryRunGeneratesSequentialContextName(t *testing.T) {
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
}

func testContextInitDryRunRejectsUnsupportedRegion(t *testing.T) {
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	result := erun.Run(t, []string{"context", "init", "--alias", "dev", "--context", "fresh", "--region", "mars-north-1", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for unsupported region, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "context/init_dry_run_rejects_unsupported_region", normalize.Apply(result.Combined))
}

func testContextInitDryRunRejectsUnsupportedInstanceType(t *testing.T) {
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	result := erun.Run(t, []string{"context", "init", "--alias", "dev", "--context", "fresh", "--instance-type", "m1.tiny", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for unsupported instance type, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "context/init_dry_run_rejects_unsupported_instance_type", normalize.Apply(result.Combined))
}

func testContextInitDryRunRejectsUnsupportedDiskSize(t *testing.T) {
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	result := erun.Run(t, []string{"context", "init", "--alias", "dev", "--context", "fresh", "--disk-size", "13", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for unsupported disk size, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "context/init_dry_run_rejects_unsupported_disk_size", normalize.Apply(result.Combined))
}

func testContextInitDryRunRejectsUnsupportedDiskType(t *testing.T) {
	setup := env.New(t)
	seedCloudContextConfig(t, setup, "edge")
	result := erun.Run(t, []string{"context", "init", "--alias", "dev", "--context", "fresh", "--disk-type", "io2", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for unsupported disk type, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "context/init_dry_run_rejects_unsupported_disk_type", normalize.Apply(result.Combined))
}

func testContextInitRealRunRetriesIamConsistencyVisibility(t *testing.T) {
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
}

func testContextInitDryRunExistingSecurityGroupSkipsCreation(t *testing.T) {
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
}

func testContextInitRealRunSecurityGroupFailureFails(t *testing.T) {
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
}

func testContextInitDryRunGeneratesNameFromUsername(t *testing.T) {
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
}

func testContextInitDryRunUnaffectedByKubectlContextConfigureLibraryExecutionMode(t *testing.T) {
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
}
