package cmd

import (
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
}

// cloudDependencies wires the CLI's cloud-command dependencies. The AWS runners
// and the Cloudflare verifier/account-lister default inside erun-common, but the
// secret store is nil unless a transport wires one — so Cloudflare alias init,
// login, and doctor repair can persist and read the scoped token. A missing
// config dir leaves the store nil and those Cloudflare operations fail clearly.
// This mirrors the MCP transport's wiring in erun-mcp/cloud.go.
func cloudDependencies() common.CloudDependencies {
	deps := common.CloudDependencies{}
	if store, err := common.DefaultCloudSecretStore(); err == nil {
		deps.CloudSecretStore = store
	}
	return deps
}

func newCloudCmd(store cloudCommandStoreInterface, promptRunner PromptRunner, selectRunner SelectRunner, deps common.CloudDependencies) *cobra.Command {
	return newCommandGroup(
		"cloud",
		"Cloud provider utilities",
		newCloudInitCmd(store, promptRunner, selectRunner, deps),
		newCloudLoginCmd(store, promptRunner, selectRunner, deps),
		newCloudOIDCCmd(store, promptRunner, selectRunner, deps),
		newCloudSetCmd(store),
	)
}

func newCloudInitCmd(store common.CloudStore, promptRunner PromptRunner, selectRunner SelectRunner, deps common.CloudDependencies) *cobra.Command {
	return newCommandGroup(
		"init",
		"Initialize cloud provider configuration",
		newCloudInitAWSCmd(store, promptRunner, deps),
		newCloudInitCloudflareCmd(store, promptRunner, selectRunner, deps),
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
	cmd.Flags().StringVar(&params.RoleName, "role-name", "", "AWS role name to use for SSO login")
	cmd.Flags().StringVar(&params.Region, "region", "", "Default AWS region for the generated configuration")
	cmd.Flags().StringVar(&params.OIDCIssuerURL, "oidc-issuer-url", "", "OIDC issuer URL trusted by deployed ERun APIs")
	addDryRunFlag(cmd)
	return cmd
}

func runCloudInitAWSCommand(ctx common.Context, store common.CloudStore, promptRunner PromptRunner, params common.InitAWSCloudProviderParams, deps common.CloudDependencies) error {
	var err error
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
		// Interactive guided wizard: prompts for the token, verifies it, and
		// resolves the account/label step by step.
		var err error
		params, err = runCloudflareInitWizard(ctx, promptRunner, selectRunner, params, deps)
		if err != nil {
			return err
		}
	case strings.TrimSpace(params.APIToken) != "" && strings.TrimSpace(params.AccountID) == "":
		// Non-interactive (flags / dry-run / MCP) with the account omitted:
		// auto-resolve it from the token, the same as the guided flow's step 3.
		// Under --dry-run this traces the GET /accounts call without contacting
		// Cloudflare. --token-name is still required (the shared init enforces it).
		accountID, err := resolveCloudflareAccountNonInteractive(ctx, params.APIToken, deps)
		if err != nil {
			return err
		}
		params.AccountID = accountID
	}
	// The shared init validates, verifies, stores, and saves — and traces the
	// full plan under --dry-run.
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

// resolveCloudflareAccountNonInteractive auto-resolves the account ID from the
// token for the non-interactive path (flags / dry-run / MCP). It requires the
// token to map to exactly one account; otherwise the caller must pass
// --account-id. Under --dry-run the underlying lookup is traced, not executed.
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

// runCloudflareInitWizard is the guided, step-by-step interactive setup: it
// points the operator at the token-creation page, takes the token (masked) and
// verifies it (re-prompting on failure), auto-resolves the account ID from the
// token, and defaults an editable label. Each step validates before advancing.
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

// verifyCloudflareTokenInteractive prompts for the token (masked) and verifies
// it, re-prompting in place until a valid token is entered.
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

// resolveCloudflareAccountInteractive auto-resolves the account from the token,
// shows a picker when the token sees several, and falls back to a manual prompt
// when none can be listed.
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

// defaultCloudflareTokenLabel proposes a recognizable label tied to this host
// and day so the operator can accept it without typing.
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
	params.RoleName, err = promptCloudValueIfEmpty(promptRunner, params.RoleName, "AWS role name", "")
	if err != nil {
		return params, err
	}
	params.Region, err = promptCloudValueIfEmpty(promptRunner, params.Region, "Default AWS region", strings.TrimSpace(params.SSORegion))
	if err != nil {
		return params, err
	}
	return params, err
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
		return writeCloudStatus(ctx, status)
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
	return writeCloudStatus(ctx, status)
}

// traceCloudLoginPlan emits the provider-appropriate login plan for dry-run.
// AWS re-runs the SSO browser login; Cloudflare re-verifies its stored scoped
// token against the Cloudflare API.
func traceCloudLoginPlan(ctx common.Context, provider common.CloudProviderConfig) {
	switch provider.Provider {
	case common.CloudProviderCloudflare:
		ctx.Trace("verify cloudflare api token via the Cloudflare API")
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
	if provider.Provider == common.CloudProviderCloudflare {
		return fmt.Errorf("cloud provider alias %q is a Cloudflare alias, which does not use OIDC web-identity federation", provider.Alias)
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
