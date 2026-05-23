package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
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
