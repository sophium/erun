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

func TestCloud(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"cloud", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/help", normalize.Apply(result.Combined))
	})

	t.Run("list_empty", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"cloud", "list"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "cloud/list_empty", normalize.Apply(result.Combined))
	})

	t.Run("init_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"cloud", "init", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/init_help", normalize.Apply(result.Combined))
	})

	t.Run("login_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"cloud", "login", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/login_help", normalize.Apply(result.Combined))
	})

	t.Run("oidc_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"cloud", "oidc", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/oidc_help", normalize.Apply(result.Combined))
	})

	t.Run("set_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"cloud", "set", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/set_help", normalize.Apply(result.Combined))
	})

	t.Run("init_aws_help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"cloud", "init", "aws", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/init_aws_help", normalize.Apply(result.Combined))
	})

	t.Run("init_aws_dry_run_traces_sso_setup_and_oidc_persistence", func(t *testing.T) {
		// Exercises cloud.go runCloudInitAWSCommand: --dry-run must trace
		// the aws configure sso plan, the sso login command, the sts
		// get-caller-identity command, the bearer-token resolution, and
		// the alias / OIDC issuer write — without invoking aws.
		setup := env.New(t)
		args := []string{
			"cloud", "init", "aws",
			"--account-id", "123456789012",
			"--role-name", "Admin",
			"--region", "eu-west-2",
			"--sso-start-url", "https://example.awsapps.com/start",
			"--sso-region", "eu-west-1",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "cloud/init_aws_dry_run", normalize.Apply(result.Combined))
	})

	t.Run("login_dry_run_traces_aws_sso_login", func(t *testing.T) {
		// Exercises cloud.go runCloudLoginCommand: --dry-run must trace
		// the aws sso login command for the resolved provider alias
		// without invoking aws or prompting.
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "rihards+123456789012@aws", "test-profile")
		result := erun.Run(t, []string{"cloud", "login", "--alias", "rihards+123456789012@aws", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Stderr, "aws sso login --profile test-profile") {
			t.Errorf("expected dry-run trace to contain aws sso login command, got stderr:\n%s", result.Stderr)
		}
	})

	t.Run("oidc_dry_run_traces_bearer_token_command", func(t *testing.T) {
		// Exercises cloud.go runCloudOIDCCommand: --dry-run must trace
		// the bearer-token resolution command with the resolved profile
		// and audience without invoking aws.
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "rihards+123456789012@aws", "test-profile")
		args := []string{"cloud", "oidc", "--alias", "rihards+123456789012@aws", "--audience", "https://api.example", "--dry-run"}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Stderr, "test-profile") || !strings.Contains(result.Stderr, "https://api.example") {
			t.Errorf("expected dry-run trace to mention profile and audience, got stderr:\n%s", result.Stderr)
		}
	})

	t.Run("set_dry_run_traces_env_alias_write", func(t *testing.T) {
		// Exercises cloud.go runCloudSetCommand: --dry-run must trace the
		// env-config write that updates the cloudProviderAlias without
		// actually persisting it.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"cloud", "set", "team", "dev", "--alias", "team-cloud", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if !strings.Contains(result.Stderr, "team-cloud") {
			t.Errorf("expected dry-run trace to mention team-cloud alias, got stderr:\n%s", result.Stderr)
		}
	})
}

func seedCloudProviderAlias(t testing.TB, setup env.Setup, alias, profile string) {
	t.Helper()
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	body := "cloudproviders:\n" +
		"  - alias: " + alias + "\n" +
		"    provider: aws\n" +
		"    profile: " + profile + "\n"
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write erun config: %v", err)
	}
}
