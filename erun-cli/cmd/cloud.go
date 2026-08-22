package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

type cloudCommandStoreInterface interface {
	common.CloudStore
	common.EnvironmentCloudAliasStore
	common.OpenStore
}

// cloudDependencies tolerates a missing secret store: when it can't be created
// the store stays nil, and Cloudflare operations that need the scoped token then
// fail clearly rather than crashing.
func cloudDependencies() common.CloudDependencies {
	return common.DefaultCloudDependencies()
}

// refreshEnvironmentHostCredentials re-injects the operator's short-lived AWS
// credentials into an env's runtime pod. The root config store and the cloud
// dependency set are stateless values, so they are constructed here instead of
// widening open's already-long dependency list.
func refreshEnvironmentHostCredentials(ctx common.Context, result common.OpenResult) (common.HostCredentialsRefresh, error) {
	return common.RefreshHostAWSCredentials(ctx, common.ConfigStore{}, result, cloudDependencies())
}

func newCloudCmd(store cloudCommandStoreInterface, promptRunner PromptRunner, selectRunner SelectRunner, deps common.CloudDependencies) *cobra.Command {
	return newCommandGroup(
		"cloud",
		"Cloud provider utilities",
		newCloudInitCmd(store, promptRunner, selectRunner, deps),
		newCloudLoginCmd(store, promptRunner, selectRunner, deps),
		newCloudOIDCCmd(store, promptRunner, selectRunner, deps),
		newCloudSetCmd(store),
		newCloudRefreshCmd(store, deps),
	)
}

func newCloudRefreshCmd(store cloudCommandStoreInterface, deps common.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "refresh TENANT ENVIRONMENT",
		Short: "Refresh an environment's in-pod host AWS credentials",
		Long: "Refresh an environment's in-pod host AWS credentials.\n\n" +
			"Reads the AWS profile behind the environment's cloud provider alias, mints short-lived " +
			"credentials from it, and writes them into the runtime pod's ~/.aws/credentials under the " +
			"erun-host profile — the profile the chart wires into the pod's AWS_PROFILE. The pod then " +
			"acts as your AWS identity until those credentials expire, typically about an hour.\n\n" +
			"Nothing secret passes through the caller: the credentials are exported here and streamed " +
			"to the pod on stdin, so no key, secret, or session token ever appears in an argument, a " +
			"trace line, or a transcript. That makes this the verb scripts and agents should use. " +
			"`erun open` runs the same refresh, so reach for this when a long-running session has gone " +
			"stale without reopening it. The write replaces the erun-host profile in place and leaves " +
			"every other profile in the file alone.\n\n" +
			"Requires an unexpired local SSO session; run `erun cloud login --alias <alias>` first if " +
			"it has lapsed.",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		Example: "  erun cloud refresh my-tenant prod\n" +
			"  erun cloud refresh my-tenant prod --dry-run",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCloudRefreshCommand(commandContext(cmd), store, args[0], args[1], deps)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func runCloudRefreshCommand(ctx common.Context, store cloudCommandStoreInterface, tenant, environment string, deps common.CloudDependencies) error {
	result, err := common.ResolveOpen(store, common.OpenParams{Tenant: tenant, Environment: environment})
	if err != nil {
		return err
	}
	ctx, closeEnvTrace := common.ActivateEnvTrace(ctx, result.Tenant, result.Environment)
	defer closeEnvTrace()
	refresh, err := common.RefreshHostAWSCredentials(ctx, store, result, deps)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		_, err := fmt.Fprintf(ctx.Stdout, "Dry run: host credential refresh planned for %s/%s.\n", result.Tenant, result.Environment)
		return err
	}
	return writeHostCredentialsRefreshed(ctx, result, refresh)
}

func writeHostCredentialsRefreshed(ctx common.Context, result common.OpenResult, refresh common.HostCredentialsRefresh) error {
	line := fmt.Sprintf("Refreshed %s credentials for %s/%s in profile %s (%s)",
		refresh.Alias, result.Tenant, result.Environment, refresh.Profile, refresh.Path)
	if !refresh.Expiration.IsZero() {
		line += ", valid until " + refresh.Expiration.UTC().Format(time.RFC3339)
	}
	_, err := fmt.Fprintln(ctx.Stdout, line)
	return err
}

func newCloudInitCmd(store common.CloudStore, promptRunner PromptRunner, selectRunner SelectRunner, deps common.CloudDependencies) *cobra.Command {
	return newCommandGroup(
		"init",
		"Initialize cloud provider configuration",
		newCloudInitAWSCmd(store, promptRunner, deps),
		newCloudInitCloudflareCmd(store, promptRunner, selectRunner, deps),
		newCloudInitERunCmd(store, deps),
	)
}

func newCloudInitAWSCmd(store common.CloudStore, promptRunner PromptRunner, deps common.CloudDependencies) *cobra.Command {
	var params common.InitAWSCloudProviderParams
	cmd := &cobra.Command{
		Use:   "aws",
		Short: "Set up an AWS cloud provider alias",
		Long: "Set up an AWS cloud provider alias.\n\n" +
			"Registers an AWS IAM Identity Center (SSO) profile and saves it as a cloud provider " +
			"alias for use by managed contexts and environments. Opens an AWS SSO login in your " +
			"browser.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCloudInitAWSCommand(commandContext(cmd), store, promptRunner, params, deps)
		},
	}
	cmd.Flags().StringVar(&params.SSOStartURL, "sso-start-url", "", "AWS IAM Identity Center start URL")
	cmd.Flags().StringVar(&params.SSORegion, "sso-region", "", "AWS IAM Identity Center region")
	cmd.Flags().StringVar(&params.AccountID, "account-id", "", "AWS account ID to use for SSO login")
	cmd.Flags().StringVar(&params.RoleName, "role-name", "", "AWS permission set (IAM Identity Center) to assume for SSO login")
	cmd.Flags().StringVar(&params.Region, "region", "", "Default AWS region for the generated configuration")
	cmd.Flags().StringVar(&params.OIDCIssuerURL, "oidc-issuer-url", "", "OIDC issuer URL trusted by deployed ERun APIs")
	addDryRunFlag(cmd)
	return cmd
}

func runCloudInitAWSCommand(ctx common.Context, store common.CloudStore, promptRunner PromptRunner, params common.InitAWSCloudProviderParams, deps common.CloudDependencies) error {
	var err error
	if strings.TrimSpace(params.Profile) == "" && awsInitParamsNeedPrompt(params) {
		printAWSInitGuidance(ctx)
	}
	params, err = promptAWSInitParams(promptRunner, params)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		if strings.TrimSpace(params.Profile) == "" {
			traceAWSConfigureSetPlan(ctx, params, "erun-sso-<timestamp>")
		}
		args := []string{"sso", "login"}
		if strings.TrimSpace(params.Profile) != "" {
			args = append(args, "--profile", strings.TrimSpace(params.Profile))
		} else {
			args = append(args, "--profile", "erun-sso-<timestamp>")
		}
		if !params.SkipLogin {
			ctx.TraceCommand("", "aws", args...)
		}
		identityArgs := []string{"sts", "get-caller-identity", "--output", "json"}
		if strings.TrimSpace(params.Profile) != "" {
			identityArgs = append(identityArgs, "--profile", strings.TrimSpace(params.Profile))
		} else {
			identityArgs = append(identityArgs, "--profile", "erun-sso-<timestamp>")
		}
		ctx.TraceCommand("", "aws", identityArgs...)
		traceAWSEnableOIDCCommand(ctx, strings.TrimSpace(params.Profile))
		traceAWSBearerTokenPlan(ctx, params, common.CloudProviderBearerAudience)
		ctx.Trace("write erun root cloud provider alias")
		ctx.Trace("write cloud provider OIDC issuer resolved from AWS web identity token")
		_, err := fmt.Fprintln(ctx.Stdout, "Dry run: AWS cloud provider initialization planned.")
		return err
	}
	provider, err := common.InitAWSCloudProvider(ctx, store, params, deps)
	if err != nil {
		return err
	}
	return writeCloudProviderSaved(ctx, provider)
}

func newCloudInitCloudflareCmd(store common.CloudStore, promptRunner PromptRunner, selectRunner SelectRunner, deps common.CloudDependencies) *cobra.Command {
	var params common.InitCloudflareCloudProviderParams
	cmd := &cobra.Command{
		Use:   "cloudflare",
		Short: "Set up a Cloudflare cloud provider alias",
		Long: "Set up a Cloudflare cloud provider alias.\n\n" +
			"Run with no flags for a guided setup: ERun shows you where to mint a delegated " +
			"API token (Zone + DNS edit, plus any other scopes you'll use such as Cloudflare " +
			"Pages for static sites), then prompts for it, " +
			"verifies it against the Cloudflare API, and auto-resolves the account ID from the " +
			"token. The token is held in a local secret store referenced from erun config — it is " +
			"never written into erun-config.yaml. Environments that attach this alias receive the " +
			"token as CLOUDFLARE_API_TOKEN so in-pod tooling (e.g. terraform) authenticates as it.\n\n" +
			"Pass --api-token (with --account-id and --token-name) for non-interactive setup.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun cloud init cloudflare\n  erun cloud init cloudflare --account-id <account> --token-name <label> --api-token <token>",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCloudInitCloudflareCommand(commandContext(cmd), store, promptRunner, selectRunner, params, deps)
		},
	}
	cmd.Flags().StringVar(&params.AccountID, "account-id", "", "Cloudflare account ID the token belongs to (auto-resolved in guided setup)")
	cmd.Flags().StringVar(&params.TokenName, "token-name", "", "Label for the scoped API token (defaults in guided setup)")
	cmd.Flags().StringVar(&params.APIToken, "api-token", "", "Cloudflare API token value (Zone + DNS edit, plus any other scopes you'll use such as Cloudflare Pages)")
	addDryRunFlag(cmd)
	return cmd
}

func runCloudInitCloudflareCommand(ctx common.Context, store common.CloudStore, promptRunner PromptRunner, selectRunner SelectRunner, params common.InitCloudflareCloudProviderParams, deps common.CloudDependencies) error {
	switch {
	case !ctx.DryRun && strings.TrimSpace(params.APIToken) == "":
		var err error
		params, err = runCloudflareInitWizard(ctx, promptRunner, selectRunner, params, deps)
		if err != nil {
			return err
		}
	case strings.TrimSpace(params.APIToken) != "" && strings.TrimSpace(params.AccountID) == "":
		accountID, err := resolveCloudflareAccountNonInteractive(ctx, params.APIToken, deps)
		if err != nil {
			return err
		}
		params.AccountID = accountID
	}
	provider, err := common.InitCloudflareCloudProvider(ctx, store, params, deps)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		_, err := fmt.Fprintln(ctx.Stdout, "Dry run: Cloudflare cloud provider initialization planned.")
		return err
	}
	return writeCloudProviderSaved(ctx, provider)
}

func newCloudInitERunCmd(store common.CloudStore, deps common.CloudDependencies) *cobra.Command {
	var params common.InitERunCloudProviderParams
	cmd := &cobra.Command{
		Use:   "erun",
		Short: "Set up a hosted erun platform cloud provider alias",
		Long: "Set up a hosted erun platform cloud provider alias.\n\n" +
			"Discovers the platform's own config (OIDC issuer, CLI client id) from its unauthenticated " +
			"GET /v1/platform endpoint and saves the alias — no instance's name is hardcoded. Run " +
			"`erun cloud login --alias <alias>` afterward to sign in: the Device Authorization Grant " +
			"(the only flow that works with no browser), falling back to Authorization Code + PKCE on " +
			"a loopback listener when the issuer advertises no device endpoint.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun cloud init erun --api-url https://api.frs-prod.services.erunpaas.com",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCloudInitERunCommand(commandContext(cmd), store, params, deps)
		},
	}
	cmd.Flags().StringVar(&params.APIURL, "api-url", "", "Base URL of the hosted erun platform's API")
	if err := cmd.MarkFlagRequired("api-url"); err != nil {
		panic(err)
	}
	addDryRunFlag(cmd)
	return cmd
}

func runCloudInitERunCommand(ctx common.Context, store common.CloudStore, params common.InitERunCloudProviderParams, deps common.CloudDependencies) error {
	provider, err := common.InitERunCloudProvider(ctx, store, params, deps)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun cloud provider initialization planned.")
		return err
	}
	return writeCloudProviderSaved(ctx, provider)
}

func resolveCloudflareAccountNonInteractive(ctx common.Context, token string, deps common.CloudDependencies) (string, error) {
	accounts, err := common.ResolveCloudflareAccounts(ctx, token, deps)
	if err != nil {
		return "", err
	}
	switch len(accounts) {
	case 0:
		return "", fmt.Errorf("no Cloudflare account is accessible by this token; pass --account-id")
	case 1:
		ctx.Trace("cloud init cloudflare: resolved account " + accounts[0].ID)
		return accounts[0].ID, nil
	default:
		return "", fmt.Errorf("this token can access multiple Cloudflare accounts; pass --account-id to choose")
	}
}

func runCloudflareInitWizard(ctx common.Context, promptRunner PromptRunner, selectRunner SelectRunner, params common.InitCloudflareCloudProviderParams, deps common.CloudDependencies) (common.InitCloudflareCloudProviderParams, error) {
	out := ctx.Stdout
	_, _ = fmt.Fprintln(out, "\nAdd a Cloudflare cloud alias")
	_, _ = fmt.Fprintln(out, "ERun needs a delegated Cloudflare API token. It never sees your Cloudflare password.")
	_, _ = fmt.Fprintln(out, "\nStep 1 of 2 · Create the token, then paste it below")
	_, _ = fmt.Fprintln(out, "  Open this page (you're already logged in to Cloudflare there):")
	_, _ = fmt.Fprintln(out, "    "+common.CloudflareCreateTokenURL)
	_, _ = fmt.Fprintln(out, "  Click Create Token → Create Custom Token, give it a name, then add")
	_, _ = fmt.Fprintln(out, "  these permission rows (each row is Scope → Permission → Access; use")
	_, _ = fmt.Fprintln(out, "  \"+ Add more\" for each):")
	_, _ = fmt.Fprintln(out, "    Zone    → Zone             → Edit   (create/manage zones)")
	_, _ = fmt.Fprintln(out, "    Zone    → DNS              → Edit   (manage DNS records)")
	_, _ = fmt.Fprintln(out, "    Account → Cloudflare Pages → Edit   (deploy static sites)")
	_, _ = fmt.Fprintln(out, "  Set Zone Resources to:    Include → All zones")
	_, _ = fmt.Fprintln(out, "  Set Account Resources to: Include → your account")
	_, _ = fmt.Fprintln(out, "  Add more rows (Workers, R2, etc.) if this token will manage those too.")
	_, _ = fmt.Fprintln(out, "  Continue to summary → Create Token, then copy it and paste it here.")
	_, _ = fmt.Fprintln(out)
	token, err := verifyCloudflareTokenInteractive(ctx, promptRunner, deps)
	if err != nil {
		return params, err
	}
	params.APIToken = token

	_, _ = fmt.Fprintln(out, "\nStep 2 of 2 · Confirm the account")
	accountID, err := resolveCloudflareAccountInteractive(ctx, promptRunner, selectRunner, token, deps)
	if err != nil {
		return params, err
	}
	params.AccountID = accountID

	if strings.TrimSpace(params.TokenName) == "" {
		label, err := requiredCloudPrompt(promptRunner, "Token label", defaultCloudflareTokenLabel())
		if err != nil {
			return params, err
		}
		params.TokenName = label
	}
	return params, nil
}

func verifyCloudflareTokenInteractive(ctx common.Context, promptRunner PromptRunner, deps common.CloudDependencies) (string, error) {
	for {
		token, err := requiredCloudSecretPrompt(promptRunner, "Cloudflare API token")
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintln(ctx.Stdout, "  Verifying with Cloudflare…")
		if _, err := common.VerifyCloudflareAPIToken(ctx, token, deps); err != nil {
			_, _ = fmt.Fprintln(ctx.Stdout, "  ✗ "+err.Error())
			continue
		}
		_, _ = fmt.Fprintln(ctx.Stdout, "  ✓ token is active")
		return token, nil
	}
}

func resolveCloudflareAccountInteractive(ctx common.Context, promptRunner PromptRunner, selectRunner SelectRunner, token string, deps common.CloudDependencies) (string, error) {
	accounts, err := common.ResolveCloudflareAccounts(ctx, token, deps)
	if err != nil || len(accounts) == 0 {
		_, _ = fmt.Fprintln(ctx.Stdout, "  Could not auto-resolve the account from the token; enter it manually.")
		return requiredCloudPrompt(promptRunner, "Cloudflare account ID", "")
	}
	if len(accounts) == 1 {
		_, _ = fmt.Fprintf(ctx.Stdout, "  Using account %q (%s)\n", accounts[0].Name, accounts[0].ID)
		return accounts[0].ID, nil
	}
	items := make([]string, 0, len(accounts))
	for _, account := range accounts {
		items = append(items, fmt.Sprintf("%s (%s)", account.Name, account.ID))
	}
	index, _, err := selectRunner(promptui.Select{Label: "Select Cloudflare account", Items: items})
	if err != nil {
		return "", err
	}
	return accounts[index].ID, nil
}

func defaultCloudflareTokenLabel() string {
	host, _ := os.Hostname()
	host = strings.TrimSpace(host)
	if host == "" {
		host = "erun"
	}
	return "erun-" + host + "-" + time.Now().UTC().Format("20060102")
}

func requiredCloudSecretPrompt(promptRunner PromptRunner, label string) (string, error) {
	prompt := promptui.Prompt{
		Label: label,
		Mask:  '*',
		Validate: func(input string) error {
			if strings.TrimSpace(input) == "" {
				return fmt.Errorf("%s is required", label)
			}
			return nil
		},
	}
	value, err := promptRunner(prompt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func promptAWSInitParams(promptRunner PromptRunner, params common.InitAWSCloudProviderParams) (common.InitAWSCloudProviderParams, error) {
	if strings.TrimSpace(params.Profile) != "" {
		return params, nil
	}
	return promptMissingAWSInitParams(promptRunner, params)
}

func promptMissingAWSInitParams(promptRunner PromptRunner, params common.InitAWSCloudProviderParams) (common.InitAWSCloudProviderParams, error) {
	var err error
	params.SSOStartURL, err = promptCloudValueIfEmpty(promptRunner, params.SSOStartURL, "AWS SSO start URL", "")
	if err != nil {
		return params, err
	}
	params.SSORegion, err = promptCloudValueIfEmpty(promptRunner, params.SSORegion, "AWS SSO region", "")
	if err != nil {
		return params, err
	}
	params.AccountID, err = promptCloudValueIfEmpty(promptRunner, params.AccountID, "AWS account ID", "")
	if err != nil {
		return params, err
	}
	params.RoleName, err = promptCloudValueIfEmpty(promptRunner, params.RoleName, "AWS permission set", "")
	if err != nil {
		return params, err
	}
	params.Region, err = promptCloudValueIfEmpty(promptRunner, params.Region, "Default AWS region", strings.TrimSpace(params.SSORegion))
	if err != nil {
		return params, err
	}
	return params, err
}

// awsInitParamsNeedPrompt reports whether the AWS SSO wizard will ask the
// operator for at least one value — used to gate the one-time guidance block.
func awsInitParamsNeedPrompt(params common.InitAWSCloudProviderParams) bool {
	return strings.TrimSpace(params.SSOStartURL) == "" ||
		strings.TrimSpace(params.SSORegion) == "" ||
		strings.TrimSpace(params.AccountID) == "" ||
		strings.TrimSpace(params.RoleName) == "" ||
		strings.TrimSpace(params.Region) == ""
}

// awsInitGuidance orients the operator before the AWS SSO prompts: where to find
// each value in the AWS access portal, and that IAM Identity Center grants the
// account through a permission set.
const awsInitGuidance = `Set up an AWS IAM Identity Center (SSO) profile for ERun.
You'll sign in through your browser when prompted; ERun never sees your AWS password.
Find each value in your AWS access portal (open the SSO start URL, sign in, then expand the account):
  - SSO start URL   your Identity Center portal, e.g. https://my-sso.awsapps.com/start
  - SSO region      the region Identity Center runs in, e.g. eu-west-2
  - Account ID      the 12-digit AWS account to use
  - Permission set  the permission set granted to you on that account (the name shown
                    under the account tile, e.g. AdministratorAccess)

`

func printAWSInitGuidance(ctx common.Context) {
	_, _ = fmt.Fprint(ctx.Stdout, awsInitGuidance)
}

func promptCloudValueIfEmpty(promptRunner PromptRunner, value, label, defaultValue string) (string, error) {
	if strings.TrimSpace(value) != "" {
		return value, nil
	}
	return requiredCloudPrompt(promptRunner, label, defaultValue)
}

func requiredCloudPrompt(promptRunner PromptRunner, label, defaultValue string) (string, error) {
	prompt := promptui.Prompt{
		Label:   label,
		Default: defaultValue,
		Validate: func(input string) error {
			if strings.TrimSpace(input) == "" {
				return fmt.Errorf("%s is required", label)
			}
			return nil
		},
	}
	value, err := promptRunner(prompt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func traceAWSConfigureSetPlan(ctx common.Context, params common.InitAWSCloudProviderParams, profile string) {
	region := strings.TrimSpace(params.Region)
	if region == "" {
		region = strings.TrimSpace(params.SSORegion)
	}
	settings := []struct {
		key   string
		value string
	}{
		{key: "sso_start_url", value: strings.TrimSpace(params.SSOStartURL)},
		{key: "sso_region", value: strings.TrimSpace(params.SSORegion)},
		{key: "sso_account_id", value: strings.TrimSpace(params.AccountID)},
		{key: "sso_role_name", value: strings.TrimSpace(params.RoleName)},
		{key: "region", value: region},
		{key: "output", value: "json"},
	}
	for _, setting := range settings {
		ctx.TraceCommand("", "aws", "configure", "set", setting.key, setting.value, "--profile", profile)
	}
}

func traceAWSBearerTokenPlan(ctx common.Context, params common.InitAWSCloudProviderParams, audience string) {
	profile := strings.TrimSpace(params.Profile)
	if profile == "" {
		profile = "erun-sso-<timestamp>"
	}
	traceAWSBearerTokenCommand(ctx, profile, audience)
}

func traceAWSBearerTokenCommand(ctx common.Context, profile, audience string) {
	args := []string{
		"sts", "get-web-identity-token",
		"--audience", strings.TrimSpace(audience),
		"--signing-algorithm", "RS256",
		"--duration-seconds", "900",
		"--query", "WebIdentityToken",
		"--output", "text",
	}
	if strings.TrimSpace(profile) != "" {
		args = append(args, "--profile", strings.TrimSpace(profile))
	}
	ctx.TraceCommand("", "aws", args...)
}

func traceAWSEnableOIDCCommand(ctx common.Context, profile string) {
	if strings.TrimSpace(profile) == "" {
		profile = "erun-sso-<timestamp>"
	}
	args := []string{
		"iam", "enable-outbound-web-identity-federation",
		"--query", "IssuerIdentifier",
		"--output", "text",
	}
	if strings.TrimSpace(profile) != "" {
		args = append(args, "--profile", strings.TrimSpace(profile))
	}
	ctx.TraceCommand("", "aws", args...)
}

func newCloudLoginCmd(store common.CloudStore, promptRunner PromptRunner, selectRunner SelectRunner, deps common.CloudDependencies) *cobra.Command {
	var alias string
	cmd := &cobra.Command{
		Use:          "login",
		Short:        "Login to a configured cloud provider alias",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCloudLoginCommand(commandContext(cmd), store, promptRunner, selectRunner, common.CloudLoginParams{Alias: alias}, deps)
		},
	}
	cmd.Flags().StringVar(&alias, "alias", "", "Cloud provider alias to login")
	addDryRunFlag(cmd)
	return cmd
}

func runCloudLoginCommand(ctx common.Context, store common.CloudStore, promptRunner PromptRunner, selectRunner SelectRunner, params common.CloudLoginParams, deps common.CloudDependencies) error {
	alias := strings.TrimSpace(params.Alias)
	if alias == "" {
		if ctx.DryRun {
			ctx.Trace("cloud login: dry-run requires --alias to be specified explicitly")
			_, err := fmt.Fprintln(ctx.Stdout, "Dry run: cloud login planned.")
			return err
		}
		selected, err := selectCloudAliasPrompt(store, selectRunner)
		if err != nil {
			return err
		}
		alias = selected
	}
	provider, err := common.ResolveCloudProvider(store, alias)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		ctx.Trace("check cloud provider token status")
		traceCloudLoginPlan(ctx, provider)
		_, err := fmt.Fprintf(ctx.Stdout, "Dry run: cloud login planned for %s.\n", provider.Alias)
		return err
	}
	status := common.CloudProviderTokenStatus(provider, deps)
	if status.Status == common.CloudTokenStatusActive {
		return finishCloudLogin(ctx, store, status, deps)
	}
	login, err := confirmPrompt(promptRunner, fmt.Sprintf("Login to %s", provider.Alias))
	if err != nil {
		return err
	}
	if !login {
		return writeCloudStatus(ctx, status)
	}
	status, err = common.LoginCloudProviderAlias(ctx, store, common.CloudLoginParams{Alias: alias, Force: true}, deps)
	if err != nil {
		return err
	}
	return finishCloudLogin(ctx, store, status, deps)
}

// finishCloudLogin writes the resolved status and, for an active erun-hosted
// alias, additionally proves the session actually authenticates against the
// real platform by calling GET /v1/whoami — the smallest end-to-end evidence
// that a token round-trip worked, not just that a token was obtained.
func finishCloudLogin(ctx common.Context, store common.CloudStore, status common.CloudProviderStatus, deps common.CloudDependencies) error {
	if err := writeCloudStatus(ctx, status); err != nil {
		return err
	}
	if status.Provider != common.CloudProviderERun || status.Status != common.CloudTokenStatusActive {
		return nil
	}
	return writeERunWhoami(ctx, store, status.Alias, deps)
}

func writeERunWhoami(ctx common.Context, store common.CloudStore, alias string, deps common.CloudDependencies) error {
	provider, err := common.ResolveCloudProvider(store, alias)
	if err != nil {
		return err
	}
	if provider.ERun == nil {
		return nil
	}
	client := common.NewPlatformClient(provider.ERun.APIURL, func() (string, error) {
		token, err := common.CloudProviderBearerToken(ctx, store, common.CloudBearerParams{Alias: alias}, deps)
		if err != nil {
			return "", err
		}
		return token.Token, nil
	})
	whoami, err := client.Whoami(context.Background())
	if err != nil {
		return fmt.Errorf("verify erun platform sign-in: %w", err)
	}
	if _, err := fmt.Fprintf(ctx.Stdout, "Signed in to %s as %s (tenant %s)\n", alias, whoami.Username, whoami.TenantID); err != nil {
		return err
	}
	config, err := client.Config(context.Background())
	if err != nil {
		return fmt.Errorf("read erun platform config: %w", err)
	}
	_, err = fmt.Fprintf(ctx.Stdout, "Tenant %s: %d environment(s), %d cloud context(s)\n", config.Tenant.Name, len(config.Environments), len(config.Contexts))
	return err
}

func traceCloudLoginPlan(ctx common.Context, provider common.CloudProviderConfig) {
	switch provider.Provider {
	case common.CloudProviderCloudflare:
		ctx.Trace("verify cloudflare api token via the Cloudflare API")
	case common.CloudProviderERun:
		ctx.Trace("GET " + provider.OIDCIssuerURL + "/.well-known/openid-configuration")
		ctx.Trace("start the device authorization grant (or the authorization code + pkce fallback if the issuer advertises no device endpoint)")
	default:
		ctx.TraceCommand("", "aws", "sso", "login", "--profile", provider.Profile)
	}
}

func newCloudOIDCCmd(store common.CloudStore, promptRunner PromptRunner, selectRunner SelectRunner, deps common.CloudDependencies) *cobra.Command {
	var alias string
	var audience string
	cmd := &cobra.Command{
		Use:          "oidc",
		Short:        "Refresh the OIDC issuer for a configured cloud provider alias",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCloudOIDCCommand(commandContext(cmd), store, promptRunner, selectRunner, common.CloudBearerParams{
				Alias:    alias,
				Audience: audience,
			}, deps)
		},
	}
	cmd.Flags().StringVar(&alias, "alias", "", "Cloud provider alias to refresh")
	cmd.Flags().StringVar(&audience, "audience", common.CloudProviderBearerAudience, "Audience for the AWS web identity bearer token")
	addDryRunFlag(cmd)
	return cmd
}

func runCloudOIDCCommand(ctx common.Context, store common.CloudStore, _ PromptRunner, selectRunner SelectRunner, params common.CloudBearerParams, deps common.CloudDependencies) error {
	alias := strings.TrimSpace(params.Alias)
	if alias == "" {
		selected, err := selectCloudAliasPrompt(store, selectRunner)
		if err != nil {
			return err
		}
		alias = selected
	}
	provider, err := common.ResolveCloudProvider(store, alias)
	if err != nil {
		return err
	}
	if provider.Provider != common.CloudProviderAWS {
		return fmt.Errorf("cloud provider alias %q is a %q-type alias, which does not use AWS web-identity federation; its OIDC issuer is set at `cloud init`", provider.Alias, provider.Provider)
	}
	if ctx.DryRun {
		audience := strings.TrimSpace(params.Audience)
		if audience == "" {
			audience = common.CloudProviderBearerAudience
		}
		ctx.Trace("check cloud provider token status")
		ctx.TraceCommand("", "aws", "sso", "login", "--profile", provider.Profile)
		traceAWSEnableOIDCCommand(ctx, provider.Profile)
		traceAWSBearerTokenCommand(ctx, provider.Profile, audience)
		ctx.Trace("write cloud provider OIDC issuer resolved from AWS web identity token")
		_, err := fmt.Fprintln(ctx.Stdout, "Dry run: cloud provider OIDC setup planned.")
		return err
	}
	status, _, err := common.SetupCloudProviderOIDC(ctx, store, common.CloudBearerParams{
		Alias:    alias,
		Audience: params.Audience,
	}, deps)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(ctx.Stdout, "Saved OIDC issuer %s for %s\n", status.OIDCIssuerURL, status.Alias)
	return err
}

func newCloudSetCmd(store common.EnvironmentCloudAliasStore) *cobra.Command {
	var alias string
	cmd := &cobra.Command{
		Use:          "set TENANT ENVIRONMENT",
		Short:        "Set the cloud provider alias for an environment",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCloudSetCommand(commandContext(cmd), store, common.SetEnvironmentCloudAliasParams{
				Tenant:      args[0],
				Environment: args[1],
				Alias:       alias,
			})
		},
	}
	cmd.Flags().StringVar(&alias, "alias", "", "Cloud provider alias to assign")
	if err := cmd.MarkFlagRequired("alias"); err != nil {
		panic(err)
	}
	addDryRunFlag(cmd)
	return cmd
}

func runCloudSetCommand(ctx common.Context, store common.EnvironmentCloudAliasStore, params common.SetEnvironmentCloudAliasParams) error {
	if _, err := common.SetEnvironmentCloudProviderAlias(ctx, store, params); err != nil {
		return err
	}
	var err error
	if ctx.DryRun {
		_, err = fmt.Fprintln(ctx.Stdout, "Dry run: cloud provider alias update planned.")
		return err
	}
	_, err = fmt.Fprintf(ctx.Stdout, "Set cloud provider alias %s for %s/%s\n", strings.TrimSpace(params.Alias), strings.TrimSpace(params.Tenant), strings.TrimSpace(params.Environment))
	return err
}

func selectCloudAliasPrompt(store common.CloudStore, selectRunner SelectRunner) (string, error) {
	providers, err := common.ListCloudProviders(store)
	if err != nil {
		return "", err
	}
	if len(providers) == 0 {
		return "", fmt.Errorf("no cloud provider aliases are configured")
	}
	items := make([]string, 0, len(providers))
	for _, provider := range providers {
		items = append(items, provider.Alias)
	}
	_, alias, err := selectRunner(promptui.Select{
		Label: "Cloud provider",
		Items: items,
	})
	return alias, err
}

func writeCloudProviderSaved(ctx common.Context, provider common.CloudProviderConfig) error {
	_, err := fmt.Fprintf(ctx.Stdout, "Saved cloud provider alias %s\n", provider.Alias)
	return err
}

func writeCloudStatus(ctx common.Context, status common.CloudProviderStatus) error {
	line := fmt.Sprintf("%s: %s", status.Alias, status.Status)
	if strings.TrimSpace(status.Message) != "" {
		line += " (" + strings.TrimSpace(status.Message) + ")"
	}
	_, err := fmt.Fprintln(ctx.Stdout, line)
	return err
}
