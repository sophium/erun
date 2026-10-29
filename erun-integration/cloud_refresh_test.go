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

func testCloudRefreshDryRunTracesPodWriteAndResolvedRegion(t *testing.T) {
	// Credentials reach the pod on stdin, so the traced script carries no secret.
	setup := env.New(t)
	fixture.SeedLocalTenantEnvWithAWSAlias(t, setup, "team", "dev", "ops+123456789012@aws", "eu-west-2", "")
	result := erun.Run(t, []string{"cloud", "refresh", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/refresh_dry_run_traces_pod_write_and_resolved_region", normalize.Apply(result.Combined))
}

func testCloudRefreshDryRunReportsUnresolvedRegion(t *testing.T) {
	setup := env.New(t)
	fixture.SeedLocalTenantEnvWithAWSAlias(t, setup, "team", "dev", "ops+123456789012@aws", "", "")
	result := erun.Run(t, []string{"cloud", "refresh", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/refresh_dry_run_reports_unresolved_region", normalize.Apply(result.Combined))
}

func testCloudRefreshCloudflareAliasFails(t *testing.T) {
	// Cloudflare tokens ship as a chart Secret at deploy time, not via refresh.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	root := filepath.Join(setup.ConfigHome, "erun")
	rootBody := "defaulttenant: team\n" +
		"cloudproviders:\n" +
		"  - alias: dns-edit+0a1b2c3d@cloudflare\n" +
		"    provider: cloudflare\n" +
		"    cloudflare:\n" +
		"      accountid: 0a1b2c3d\n" +
		"      tokenname: dns-edit\n" +
		"      tokenref: erun-secret://dns-edit\n"
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(rootBody), 0o644); err != nil {
		t.Fatalf("write erun config: %v", err)
	}
	envCfgPath := filepath.Join(root, "team", "dev", "config.yaml")
	envBody := "name: dev\nrepopath: " + setup.Cwd + "\nkubernetescontext: test-context\n" +
		"containerregistry: registry.example/test\nruntimeversion: 1.0.0\ntype: local-agent\n" +
		"cloudprovideralias: dns-edit+0a1b2c3d@cloudflare\n"
	if err := os.WriteFile(envCfgPath, []byte(envBody), 0o644); err != nil {
		t.Fatalf("write env config: %v", err)
	}
	result := erun.Run(t, []string{"cloud", "refresh", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for a Cloudflare alias, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "cloud/refresh_cloudflare_alias_fails", normalize.Apply(result.Combined))
}

func testCloudRefreshWithoutAwsAliasFails(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"cloud", "refresh", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for an env with no AWS alias, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "cloud/refresh_without_aws_alias_fails", normalize.Apply(result.Combined))
}

func testCloudRefreshRealRunKeepsCredentialsOutOfOutput(t *testing.T) {
	// Neither streams nor the recorded kubectl argv may contain the credentials.
	setup := env.New(t)
	fixture.SeedLocalTenantEnvWithAWSAlias(t, setup, "team", "dev", "ops+123456789012@aws", "eu-west-2", "")
	stubs := filepath.Join(setup.Cwd, "stubs")
	const secret = "SECRETVALUEMUSTNOTLEAK"
	exported := `{"Version":1,"AccessKeyId":"ASIAEXAMPLE","SecretAccessKey":"` + secret +
		`","SessionToken":"TOKENVALUEMUSTNOTLEAK","Expiration":"2126-01-02T03:04:05Z"}`
	fixture.StubBinaryWithScript(t, stubs, "aws", strings.Join([]string{
		`case "$*" in`,
		`  *"configure export-credentials"*) printf '%s' '` + exported + `' ;;`,
		`  *) : ;;`,
		`esac`,
		`exit 0`,
	}, "\n"))
	argvLog := filepath.Join(stubs, "kubectl-argv.log")
	fixture.StubBinaryWithScript(t, stubs, "kubectl", strings.Join([]string{
		`printf '%s\n' "$*" >> '` + argvLog + `'`,
		`cat >/dev/null 2>&1 || true`,
		`exit 0`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws", "kubectl")...)
	result := erun.Run(t, []string{"cloud", "refresh", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// Normalization does not mask these values, so a raw substring guard is needed.
	for _, leak := range []string{secret, "TOKENVALUEMUSTNOTLEAK", "ASIAEXAMPLE"} {
		if strings.Contains(result.Combined, leak) {
			t.Fatalf("credential value %q leaked into command output:\n%s", leak, result.Combined)
		}
	}
	recorded, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("read recorded kubectl argv: %v", err)
	}
	for _, leak := range []string{secret, "TOKENVALUEMUSTNOTLEAK", "ASIAEXAMPLE"} {
		if strings.Contains(string(recorded), leak) {
			t.Fatalf("credential value %q leaked into a kubectl argument:\n%s", leak, recorded)
		}
	}
	golden.Equal(t, "cloud/refresh_real_run_keeps_credentials_out_of_output", normalize.Apply(result.Combined))
}

func testCloudRefreshDryRunUnaffectedByLibraryExecutionMode(t *testing.T) {
	// Library mode must not change the dry-run trace: export is never called on a dry run.
	setup := env.New(t)
	fixture.SeedLocalTenantEnvWithAWSAlias(t, setup, "team", "dev", "ops+123456789012@aws", "eu-west-2", "")
	seedExecutionMode(t, setup, "aws-export-credentials", "library")
	result := erun.Run(t, []string{"cloud", "refresh", "team", "dev", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "cloud/refresh_dry_run_traces_pod_write_and_resolved_region", normalize.Apply(result.Combined))
}

func testCloudRefreshRealRunLibraryExecutionModeNeedsNoAwsBinary(t *testing.T) {
	// Only kubectl is stubbed: an aws subprocess fallback would fail on the PATH scrub.
	setup := env.New(t)
	fixture.SeedLocalTenantEnvWithAWSAlias(t, setup, "team", "dev", "ops+123456789012@aws", "eu-west-2", "")
	seedExecutionMode(t, setup, "aws-export-credentials", "library")
	seedAWSStaticCredentialProfile(t, setup, "erun-sso-test")
	stubs := filepath.Join(setup.Cwd, "stubs")
	argvLog := filepath.Join(stubs, "kubectl-argv.log")
	fixture.StubBinaryWithScript(t, stubs, "kubectl", strings.Join([]string{
		`printf '%s\n' "$*" >> '` + argvLog + `'`,
		`cat >/dev/null 2>&1 || true`,
		`exit 0`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"cloud", "refresh", "team", "dev"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	for _, leak := range []string{"AKIAFAKE", "fakesecret"} {
		if strings.Contains(result.Combined, leak) {
			t.Fatalf("credential value %q leaked into command output:\n%s", leak, result.Combined)
		}
	}
	recorded, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("read recorded kubectl argv: %v", err)
	}
	for _, leak := range []string{"AKIAFAKE", "fakesecret"} {
		if strings.Contains(string(recorded), leak) {
			t.Fatalf("credential value %q leaked into a kubectl argument:\n%s", leak, recorded)
		}
	}
	golden.Equal(t, "cloud/refresh_real_run_library_execution_mode_needs_no_aws_binary", normalize.Apply(result.Combined))
}
