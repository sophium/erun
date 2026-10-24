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

func testCloudLoginRealRunInvokesAwsSsoLoginViaStub(t *testing.T) {
	setup := env.New(t)
	seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
	envVars, _ := stubAWSCallerIdentityAndJWT(t, setup)
	result := erun.Run(t, []string{"cloud", "login", "--alias", "test-user@aws"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/login_real_run_invokes_aws_sso_login_via_stub", normalize.Apply(result.Combined))
}

func testCloudLoginRealRunLibraryExecutionModeNeedsNoAwsBinary(t *testing.T) {
	// No "aws" stub is declared: a subprocess fallback would fail on the PATH scrub.
	setup := env.New(t)
	seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
	seedExecutionMode(t, setup, "aws-sts", "library")
	seedAWSStaticCredentialProfile(t, setup, "test-profile")
	envVars := append(setup.Env(), "AWS_ENDPOINT_URL="+stsCallerIdentityStubServer(t).URL)
	result := erun.Run(t, []string{"cloud", "login", "--alias", "test-user@aws"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/login_real_run_library_execution_mode_needs_no_aws_binary", normalize.Apply(result.Combined))
}

func testCloudLoginDryRunTracesAwsSsoLogin(t *testing.T) {
	setup := env.New(t)
	seedCloudProviderAlias(t, setup, "rihards+123456789012@aws", "test-profile")
	result := erun.Run(t, []string{"cloud", "login", "--alias", "rihards+123456789012@aws", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/login_dry_run_traces_aws_sso_login", normalize.Apply(result.Combined))
}

func testCloudLoginSelectPromptResolvesActiveAlias(t *testing.T) {
	// "\r" confirms the single alias; readline read-ahead allows one prompt per run.
	setup := env.New(t)
	seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
	envVars, _ := stubAWSCallerIdentityAndJWT(t, setup)
	result := erun.Run(t, []string{"cloud", "login"}, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   envVars,
		Stdin: "\r",
	})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/login_select_prompt_resolves_active_alias", normalize.Apply(result.Combined))
}

func testCloudLoginExpiredConfirmsReloginViaStub(t *testing.T) {
	setup := env.New(t)
	seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
	stubs := setup.Cwd + "/stubs"
	script := strings.Join([]string{
		`case "$*" in`,
		`  *"sts get-caller-identity"*)`,
		`    printf '%s\n' 'The SSO session associated with this profile has expired or is otherwise invalid.' >&2`,
		`    exit 255 ;;`,
		`  *) : ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubs, "aws", script)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
	result := erun.Run(t, []string{"cloud", "login", "--alias", "test-user@aws"}, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   envVars,
		Stdin: "y\n",
	})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/login_expired_confirms_relogin_via_stub", normalize.Apply(result.Combined))
}

func testCloudLoginNotConfiguredDeclinesRelogin(t *testing.T) {
	setup := env.New(t)
	seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
	stubs := setup.Cwd + "/stubs"
	script := strings.Join([]string{
		`case "$*" in`,
		`  *"sts get-caller-identity"*)`,
		`    printf '%s\n' 'The config profile (test-profile) could not be found' >&2`,
		`    exit 255 ;;`,
		`  *"sso login"*)`,
		`    printf '%s\n' 'sso login must not run when the user declines' >&2`,
		`    exit 254 ;;`,
		`  *) : ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubs, "aws", script)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
	result := erun.Run(t, []string{"cloud", "login", "--alias", "test-user@aws"}, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   envVars,
		Stdin: "n\n",
	})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/login_not_configured_declines_relogin", normalize.Apply(result.Combined))
}

func testCloudLoginRealRunUnsupportedProviderFails(t *testing.T) {
	setup := env.New(t)
	root := filepath.Join(setup.ConfigHome, "erun")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	body := "cloudproviders:\n" +
		"  - alias: test-user@gcp\n" +
		"    provider: gcp\n" +
		"    profile: test-profile\n"
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write erun config: %v", err)
	}
	result := erun.Run(t, []string{"cloud", "login", "--alias", "test-user@gcp"}, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   setup.Env(),
		Stdin: "y\n",
	})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for unsupported provider, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "cloud/login_real_run_unsupported_provider_fails", normalize.Apply(result.Combined))
}

func testCloudLoginRealRunSsoLoginFailureFails(t *testing.T) {
	setup := env.New(t)
	seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
	stubs := setup.Cwd + "/stubs"
	script := strings.Join([]string{
		`case "$*" in`,
		`  *"sts get-caller-identity"*)`,
		`    printf '%s\n' 'The SSO session associated with this profile has expired or is otherwise invalid.' >&2`,
		`    exit 255 ;;`,
		`  *"sso login"*)`,
		`    printf '%s\n' 'SSO authorization page failed to open' >&2`,
		`    exit 1 ;;`,
		`  *) : ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubs, "aws", script)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
	result := erun.Run(t, []string{"cloud", "login", "--alias", "test-user@aws"}, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   envVars,
		Stdin: "y\n",
	})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when sso login fails, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "cloud/login_real_run_sso_login_failure_fails", normalize.Apply(result.Combined))
}
