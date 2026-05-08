package integration

import (
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

	t.Run("init_aws_real_run_via_stubs", func(t *testing.T) {
		// Drive the AWS auth flow (cloud.go) via an aws stub so we exercise
		// the bearer-token retrieval, identity resolution, and persisted
		// config branches without needing real AWS credentials.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		// The aws stub returns canned JSON-ish output. Real `aws sts
		// get-caller-identity` returns a JSON object; we mimic just enough
		// for the parse to succeed.
		fixture.StubBinary(t, stubs, "aws", `{"Account":"123456789012","Arn":"arn:aws:sts::123456789012:assumed-role/Admin/test","UserId":"AROA"}`)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		args := []string{
			"cloud", "init", "aws",
			"--profile", "test-profile",
			"--account-id", "123456789012",
			"--role", "Admin",
			"--region", "eu-west-2",
			"--start-url", "https://example.awsapps.com/start",
			"--sso-region", "eu-west-1",
			"--skip-login",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		golden.Equal(t, "cloud/init_aws_real_run_via_stubs", normalize.Apply(result.Combined))
	})
}
