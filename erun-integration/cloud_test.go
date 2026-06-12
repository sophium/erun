package integration

import (
	"encoding/base64"
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

// stubAWSCallerIdentityAndJWT writes an `aws` stub that branches on argv:
// `aws sts get-caller-identity` returns canned identity JSON;
// `aws sts get-web-identity-token` returns a minimal 3-part JWT whose
// payload encodes an issuer claim;
// every other invocation (configure set, sso login, ...) exits 0 silently.
// The bearer token issuer is exposed so callers can assert against it.
func stubAWSCallerIdentityAndJWT(t testing.TB, setup env.Setup) (envVars []string, issuer string) {
	t.Helper()
	stubs := setup.Cwd + "/stubs"
	issuer = "https://oidc.eu-west-2.amazonaws.com/test-issuer"
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + issuer + `"}`))
	jwt := header + "." + payload + ".sig"
	identityJSON := `{"UserId":"AIDAEXAMPLE","Account":"123456789012","Arn":"arn:aws:iam::123456789012:user/test-user"}`
	script := strings.Join([]string{
		`case "$*" in`,
		`  *"sts get-caller-identity"*) printf '%s' '` + identityJSON + `' ;;`,
		`  *"sts get-web-identity-token"*) printf '%s' '` + jwt + `' ;;`,
		`  *) : ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubs, "aws", script)
	envVars = append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
	return envVars, issuer
}

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

	t.Run("init_aws_prompts_missing_region_via_stdin", func(t *testing.T) {
		// Exercises requiredCloudPrompt (via promptCloudValueIfEmpty):
		// every AWS init param except --region is provided, so the command
		// asks exactly one prompt — "Default AWS region", defaulting to the
		// SSO region — and the typed value flows into the traced
		// `aws configure set region` plan. One prompt per subprocess
		// (readline read-ahead), which is why only --region is omitted.
		setup := env.New(t)
		args := []string{
			"cloud", "init", "aws",
			"--account-id", "123456789012",
			"--role-name", "Admin",
			"--sso-start-url", "https://example.awsapps.com/start",
			"--sso-region", "eu-west-1",
			"--dry-run",
		}
		result := erun.Run(t, args, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   setup.Env(),
			Stdin: "eu-west-2\n",
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/init_aws_prompts_missing_region_via_stdin", normalize.Apply(result.Combined))
	})

	t.Run("init_aws_real_run_persists_alias_and_issuer", func(t *testing.T) {
		// Exercises eruncommon.InitAWSCloudProvider end-to-end without
		// --dry-run: drives initAWSProfile (configure-set + sso login),
		// ResolveAWSIdentity (sts get-caller-identity → JSON), the
		// SaveCloudProviderConfig write into XDG, and SetupCloudProviderOIDC
		// (sts get-web-identity-token → JWT → issuer extraction). All AWS
		// calls go through a single stub that branches on argv.
		setup := env.New(t)
		envVars, issuer := stubAWSCallerIdentityAndJWT(t, setup)
		args := []string{
			"cloud", "init", "aws",
			"--account-id", "123456789012",
			"--role-name", "Admin",
			"--region", "eu-west-2",
			"--sso-start-url", "https://example.awsapps.com/start",
			"--sso-region", "eu-west-1",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		// The persisted root config must contain the resolved alias plus
		// the issuer extracted from the stubbed bearer token.
		raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
		if err != nil {
			t.Fatalf("read root config: %v", err)
		}
		body := string(raw)
		for _, want := range []string{
			"alias: test-user+123456789012@aws",
			"username: test-user",
			"accountid: \"123456789012\"",
			"ssostarturl: https://example.awsapps.com/start",
			"oidcissuerurl: " + issuer,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("expected persisted config to contain %q, got:\n%s", want, body)
			}
		}
	})

	t.Run("login_real_run_invokes_aws_sso_login_via_stub", func(t *testing.T) {
		// Exercises eruncommon.LoginCloudProviderAlias real-run path:
		// resolves the seeded provider, calls deps.RunAWSLogin which
		// shells out to `aws sso login` via the stub, then returns the
		// status. Locks the trace and confirms the stub was reached.
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
		envVars, _ := stubAWSCallerIdentityAndJWT(t, setup)
		result := erun.Run(t, []string{"cloud", "login", "--alias", "test-user@aws"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/login_real_run_invokes_aws_sso_login_via_stub", normalize.Apply(result.Combined))
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
		golden.Equal(t, "cloud/login_dry_run_traces_aws_sso_login", normalize.Apply(result.Combined))
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
		golden.Equal(t, "cloud/oidc_dry_run_traces_bearer_token_command", normalize.Apply(result.Combined))
	})

	t.Run("login_select_prompt_resolves_active_alias", func(t *testing.T) {
		// Exercises cloud.go selectCloudAliasPrompt: `cloud login` without
		// --alias must list the configured aliases in a Select prompt;
		// "\r" confirms the single (highlighted) entry. The aws stub answers
		// `sts get-caller-identity` with exit 0, so defaultCheckAWSStatus
		// classifies the token as active and runCloudLoginCommand returns
		// the status without a login round-trip. The Select is the run's
		// single interactive prompt (readline read-ahead).
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
	})

	t.Run("login_expired_confirms_relogin_via_stub", func(t *testing.T) {
		// Exercises eruncommon.LoginCloudProviderAlias end to end on the
		// expired-token path: defaultCheckAWSStatus classifies the stubbed
		// `sts get-caller-identity` failure as expired, the confirm prompt
		// is accepted ("y"), the forced login shells out to `aws sso login`
		// via the stub (defaultRunAWSLogin real arm), and the post-login
		// status is printed. The stub's stderr message is the decision
		// input driving the expired classification.
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
	})

	t.Run("login_not_configured_declines_relogin", func(t *testing.T) {
		// Exercises defaultCheckAWSStatus's "could not be found" →
		// not_configured classification plus runCloudLoginCommand's decline
		// branch: answering "n" to the login confirm must print the current
		// status without invoking `aws sso login`.
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
	})

	t.Run("oidc_real_run_enables_federation_and_persists_issuer", func(t *testing.T) {
		// Exercises CloudProviderBearerToken's federation-disabled recovery
		// end to end: the first `sts get-web-identity-token` fails with
		// OutboundWebIdentityFederationDisabledException, the command runs
		// `iam enable-outbound-web-identity-federation` (defaultRunAWSEnableOIDC
		// real arm), and the retried token call succeeds — a stateful aws
		// stub flips behavior via a marker file once the enable call runs.
		// The issuer extracted from the JWT must be persisted to the root
		// config (side effect outside the captured streams).
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
		stubs := setup.Cwd + "/stubs"
		issuer := "https://oidc.eu-west-2.amazonaws.com/test-issuer"
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + issuer + `"}`))
		jwt := header + "." + payload + ".sig"
		marker := filepath.Join(stubs, "federation-enabled")
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*) printf '%s' '{"UserId":"AIDAEXAMPLE","Account":"123456789012","Arn":"arn:aws:iam::123456789012:user/test-user"}' ;;`,
			`  *"iam enable-outbound-web-identity-federation"*)`,
			`    touch '` + marker + `'`,
			`    printf '%s\n' '` + issuer + `' ;;`,
			`  *"sts get-web-identity-token"*)`,
			`    if [ -f '` + marker + `' ]; then`,
			`      printf '%s' '` + jwt + `'`,
			`    else`,
			`      printf '%s\n' 'An error occurred (OutboundWebIdentityFederationDisabledException) when calling the GetWebIdentityToken operation' >&2`,
			`      exit 254`,
			`    fi ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		result := erun.Run(t, []string{"cloud", "oidc", "--alias", "test-user@aws"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/oidc_real_run_enables_federation_and_persists_issuer", normalize.Apply(result.Combined))
		raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
		if err != nil {
			t.Fatalf("read root config: %v", err)
		}
		if !strings.Contains(string(raw), "oidcissuerurl: "+issuer) {
			t.Errorf("expected persisted OIDC issuer %q, got:\n%s", issuer, raw)
		}
	})

	t.Run("oidc_real_run_tolerates_already_enabled_federation", func(t *testing.T) {
		// Exercises defaultRunAWSEnableOIDC's already-enabled tolerance:
		// when the enable call itself fails with the "FeatureEnabled /
		// already enabled" message (another principal enabled federation
		// between the failed token call and the recovery), the command must
		// treat it as success and retry the bearer token. The stub fails
		// the first token call with the disabled exception, fails the
		// enable call with the already-enabled message (while flipping the
		// marker), and serves the JWT afterwards.
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
		stubs := setup.Cwd + "/stubs"
		issuer := "https://oidc.eu-west-2.amazonaws.com/test-issuer"
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + issuer + `"}`))
		jwt := header + "." + payload + ".sig"
		marker := filepath.Join(stubs, "federation-enabled")
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*) printf '%s' '{"UserId":"AIDAEXAMPLE","Account":"123456789012","Arn":"arn:aws:iam::123456789012:user/test-user"}' ;;`,
			`  *"iam enable-outbound-web-identity-federation"*)`,
			`    touch '` + marker + `'`,
			`    printf '%s\n' 'An error occurred (FeatureEnabled): Outbound web identity federation is already enabled' >&2`,
			`    exit 254 ;;`,
			`  *"sts get-web-identity-token"*)`,
			`    if [ -f '` + marker + `' ]; then`,
			`      printf '%s' '` + jwt + `'`,
			`    else`,
			`      printf '%s\n' 'An error occurred (OutboundWebIdentityFederationDisabledException) when calling the GetWebIdentityToken operation' >&2`,
			`      exit 254`,
			`    fi ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		result := erun.Run(t, []string{"cloud", "oidc", "--alias", "test-user@aws"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/oidc_real_run_tolerates_already_enabled_federation", normalize.Apply(result.Combined))
	})

	t.Run("set_real_run_same_alias_heals_managed_cloud", func(t *testing.T) {
		// Exercises saveManagedCloudAliasIfNeeded: `cloud set` with the
		// alias the remote env already carries takes the no-change path,
		// which must still backfill managedcloud=true for a remote worktree
		// that predates the flag (side effect outside the captured
		// streams).
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		envCfgPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		body := "name: dev\n" +
			"repopath: /home/erun/git/team\n" +
			"kubernetescontext: test-context\n" +
			"containerregistry: registry.example/test\n" +
			"runtimeversion: 1.0.0\n" +
			"remote: true\n" +
			"cloudprovideralias: team-cloud\n"
		if err := os.WriteFile(envCfgPath, []byte(body), 0o644); err != nil {
			t.Fatalf("rewrite env config with alias: %v", err)
		}
		result := erun.Run(t, []string{"cloud", "set", "team", "dev", "--alias", "team-cloud"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/set_real_run_same_alias_heals_managed_cloud", normalize.Apply(result.Combined))
		raw, err := os.ReadFile(envCfgPath)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		if !strings.Contains(string(raw), "managedcloud: true") {
			t.Errorf("expected managedcloud backfilled on the remote env, got:\n%s", raw)
		}
	})

	t.Run("init_aws_real_run_identity_failure_fails", func(t *testing.T) {
		// Exercises InitAWSCloudProvider's identity-resolution failure:
		// profile setup and sso login succeed, but `sts get-caller-identity`
		// fails, so the command must trace the failure and exit non-zero
		// (defaultResolveAWSIdentity error path). Dry-run cannot reach this:
		// the failure is the aws CLI's real exit status.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*)`,
			`    printf '%s\n' 'An error occurred (AccessDenied) when calling the GetCallerIdentity operation' >&2`,
			`    exit 254 ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		args := []string{
			"cloud", "init", "aws",
			"--account-id", "123456789012",
			"--role-name", "Admin",
			"--region", "eu-west-2",
			"--sso-start-url", "https://example.awsapps.com/start",
			"--sso-region", "eu-west-1",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when identity resolution fails, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/init_aws_real_run_identity_failure_fails", normalize.Apply(result.Combined))
	})

	t.Run("init_aws_real_run_unresolvable_alias_fails", func(t *testing.T) {
		// Exercises the alias fallbacks in InitAWSCloudProvider: an identity
		// response with no Account/Arn forces both the username and account
		// params fallbacks, and with no username available the resolved
		// alias is empty, which must fail with "cloud provider alias cannot
		// be resolved" instead of persisting a nameless provider.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*) printf '%s' '{}' ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		args := []string{
			"cloud", "init", "aws",
			"--account-id", "123456789012",
			"--role-name", "Admin",
			"--region", "eu-west-2",
			"--sso-start-url", "https://example.awsapps.com/start",
			"--sso-region", "eu-west-1",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when no alias can be derived, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/init_aws_real_run_unresolvable_alias_fails", normalize.Apply(result.Combined))
	})

	t.Run("init_aws_real_run_root_arn_username_fallback", func(t *testing.T) {
		// Exercises AWSUsernameFromARN's colon fallback: a root-account ARN
		// has no "/" segment, so the username derives from the last ":"
		// segment ("root") and the persisted alias must reflect it.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		identityJSON := `{"UserId":"123456789012","Account":"123456789012","Arn":"arn:aws:iam::123456789012:root"}`
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://oidc.example/issuer"}`))
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*) printf '%s' '` + identityJSON + `' ;;`,
			`  *"sts get-web-identity-token"*) printf '%s' '` + header + "." + payload + ".sig" + `' ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		args := []string{
			"cloud", "init", "aws",
			"--account-id", "123456789012",
			"--role-name", "Admin",
			"--region", "eu-west-2",
			"--sso-start-url", "https://example.awsapps.com/start",
			"--sso-region", "eu-west-1",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		raw, err := os.ReadFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"))
		if err != nil {
			t.Fatalf("read root config: %v", err)
		}
		if !strings.Contains(string(raw), "alias: root+123456789012@aws") {
			t.Errorf("expected alias derived from the root ARN colon segment, got:\n%s", raw)
		}
	})

	t.Run("init_aws_real_run_configure_set_failure_fails", func(t *testing.T) {
		// Exercises initAWSProfile's configure failure: the first
		// `aws configure set` fails, defaultRunAWSConfigureSSO wraps the
		// stderr into "aws configure set sso_start_url: ...", and the init
		// traces "profile setup failed" before exiting non-zero.
		setup := env.New(t)
		stubs := setup.Cwd + "/stubs"
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"configure set"*)`,
			`    printf '%s\n' 'Permission denied writing ~/.aws/config' >&2`,
			`    exit 1 ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		args := []string{
			"cloud", "init", "aws",
			"--account-id", "123456789012",
			"--role-name", "Admin",
			"--region", "eu-west-2",
			"--sso-start-url", "https://example.awsapps.com/start",
			"--sso-region", "eu-west-1",
		}
		result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when configure set fails, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/init_aws_real_run_configure_set_failure_fails", normalize.Apply(result.Combined))
	})

	t.Run("login_real_run_unsupported_provider_fails", func(t *testing.T) {
		// Exercises LoginCloudProviderAlias's unsupported-provider arm plus
		// CloudProviderTokenStatus's non-AWS classification: a provider
		// stored with provider=gcp reports status unknown, and confirming
		// the login must fail with "unsupported cloud provider" instead of
		// shelling out to a CLI that does not exist.
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
	})

	t.Run("login_real_run_sso_login_failure_fails", func(t *testing.T) {
		// Exercises LoginCloudProviderAlias's login failure arm plus
		// defaultRunAWSLogin's error wrap: the token reads expired, the
		// user confirms a re-login, and the stubbed `aws sso login` fails,
		// so the command must surface "aws sso login: <stderr>".
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
	})

	t.Run("oidc_real_run_token_access_denied_fails", func(t *testing.T) {
		// Exercises CloudProviderBearerToken's non-recoverable token
		// failure: the session is active, but `sts get-web-identity-token`
		// fails with AccessDenied — not the federation-disabled exception —
		// so the command must fail without attempting the enable-federation
		// recovery (SetupCloudProviderOIDC's bearer-failure trace).
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
		stubs := setup.Cwd + "/stubs"
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*) printf '%s' '{"UserId":"AIDAEXAMPLE","Account":"123456789012","Arn":"arn:aws:iam::123456789012:user/test-user"}' ;;`,
			`  *"iam enable-outbound-web-identity-federation"*)`,
			`    printf '%s\n' 'enable-federation must not run for a non-federation error' >&2`,
			`    exit 254 ;;`,
			`  *"sts get-web-identity-token"*)`,
			`    printf '%s\n' 'An error occurred (AccessDenied) when calling the GetWebIdentityToken operation' >&2`,
			`    exit 254 ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		result := erun.Run(t, []string{"cloud", "oidc", "--alias", "test-user@aws"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when the bearer token call fails, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/oidc_real_run_token_access_denied_fails", normalize.Apply(result.Combined))
	})

	t.Run("oidc_real_run_expired_session_logs_in_before_token", func(t *testing.T) {
		// Exercises CloudProviderBearerToken's inactive-session branch: the
		// status check classifies the session as expired, so the flow must
		// run `aws sso login` before requesting the web identity token. The
		// stub records the login call via a marker file (side effect outside
		// the captured streams) and serves the JWT either way.
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
		stubs := setup.Cwd + "/stubs"
		issuer := "https://oidc.eu-west-2.amazonaws.com/test-issuer"
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + issuer + `"}`))
		jwt := header + "." + payload + ".sig"
		marker := filepath.Join(stubs, "sso-login-ran")
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*)`,
			`    printf '%s\n' 'The SSO session associated with this profile has expired or is otherwise invalid.' >&2`,
			`    exit 255 ;;`,
			`  *"sso login"*)`,
			`    touch '` + marker + `' ;;`,
			`  *"sts get-web-identity-token"*) printf '%s' '` + jwt + `' ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		result := erun.Run(t, []string{"cloud", "oidc", "--alias", "test-user@aws"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Errorf("expected `aws sso login` to run before the token request: %v", err)
		}
		golden.Equal(t, "cloud/oidc_real_run_expired_session_logs_in_before_token", normalize.Apply(result.Combined))
	})

	t.Run("oidc_real_run_non_jwt_token_fails", func(t *testing.T) {
		// Exercises issuerFromJWT's shape validation: a token without the
		// three JWT segments must fail with "bearer token is not a JWT"
		// instead of persisting a bogus issuer.
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
		stubs := setup.Cwd + "/stubs"
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*) printf '%s' '{"UserId":"AIDAEXAMPLE","Account":"123456789012","Arn":"arn:aws:iam::123456789012:user/test-user"}' ;;`,
			`  *"sts get-web-identity-token"*) printf '%s' 'not-a-jwt' ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		result := erun.Run(t, []string{"cloud", "oidc", "--alias", "test-user@aws"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a non-JWT token, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/oidc_real_run_non_jwt_token_fails", normalize.Apply(result.Combined))
	})

	t.Run("oidc_real_run_enable_federation_failure_fails", func(t *testing.T) {
		// Exercises CloudProviderBearerToken's enable-recovery failure arm:
		// the token call reports federation disabled, the enable call then
		// fails with a real error (not "already enabled"), and that enable
		// failure must be the command's error.
		setup := env.New(t)
		seedCloudProviderAlias(t, setup, "test-user@aws", "test-profile")
		stubs := setup.Cwd + "/stubs"
		script := strings.Join([]string{
			`case "$*" in`,
			`  *"sts get-caller-identity"*) printf '%s' '{"UserId":"AIDAEXAMPLE","Account":"123456789012","Arn":"arn:aws:iam::123456789012:user/test-user"}' ;;`,
			`  *"iam enable-outbound-web-identity-federation"*)`,
			`    printf '%s\n' 'An error occurred (AccessDenied) when calling the EnableOutboundWebIdentityFederation operation' >&2`,
			`    exit 254 ;;`,
			`  *"sts get-web-identity-token"*)`,
			`    printf '%s\n' 'An error occurred (OutboundWebIdentityFederationDisabledException) when calling the GetWebIdentityToken operation' >&2`,
			`    exit 254 ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
		fixture.StubBinaryWithScript(t, stubs, "aws", script)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "aws")...)
		result := erun.Run(t, []string{"cloud", "oidc", "--alias", "test-user@aws"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when enabling federation fails, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/oidc_real_run_enable_federation_failure_fails", normalize.Apply(result.Combined))
	})

	t.Run("set_real_run_new_alias_marks_managed_cloud", func(t *testing.T) {
		// Exercises the alias-change path of SetEnvironmentCloudProviderAlias
		// on a remote env: assigning a new alias must set managedcloud=true
		// (remote worktree implies a managed cloud runtime) and persist both
		// fields. The env config deliberately omits `name:` so the
		// environment-name backfill branch runs too.
		setup := env.New(t)
		fixture.SeedRemoteTenantEnv(t, setup, "team", "dev")
		envCfgPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		body := "repopath: " + filepath.Join(setup.Home, "git", "team") + "\n" +
			"kubernetescontext: test-context\n" +
			"containerregistry: registry.example/test\n" +
			"runtimeversion: 1.0.0\n" +
			"remote: true\n"
		if err := os.WriteFile(envCfgPath, []byte(body), 0o644); err != nil {
			t.Fatalf("rewrite env config without alias: %v", err)
		}
		result := erun.Run(t, []string{"cloud", "set", "team", "dev", "--alias", "team-cloud"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "cloud/set_real_run_new_alias_marks_managed_cloud", normalize.Apply(result.Combined))
		raw, err := os.ReadFile(envCfgPath)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		for _, want := range []string{"cloudprovideralias: team-cloud", "managedcloud: true", "name: dev"} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("expected persisted env config to contain %q, got:\n%s", want, raw)
			}
		}
	})

	t.Run("set_missing_environment_fails", func(t *testing.T) {
		// Exercises SetEnvironmentCloudProviderAlias's not-found arm: the
		// tenant exists but the environment does not, so the command must
		// fail with the environment-not-found error.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"cloud", "set", "team", "ghost", "--alias", "team-cloud"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a missing environment, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/set_missing_environment_fails", normalize.Apply(result.Combined))
	})

	t.Run("set_empty_alias_fails", func(t *testing.T) {
		// Exercises normalizeEnvironmentCloudProviderAliasParams: --alias is
		// flag-required by cobra, but an explicitly empty value must still
		// be rejected by the shared params validation.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"cloud", "set", "team", "dev", "--alias", ""}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for an empty alias, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "cloud/set_empty_alias_fails", normalize.Apply(result.Combined))
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
		golden.Equal(t, "cloud/set_dry_run_traces_env_alias_write", normalize.Apply(result.Combined))
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
