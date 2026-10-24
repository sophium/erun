package cloud

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/cmd/shared"
	common "github.com/sophium/erun/erun-common"
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-common/primitive/open"
	sharedpkg "github.com/sophium/erun/erun-common/primitive/shared"
	"github.com/spf13/cobra"
)

type cloudCommandStoreInterface interface {
	cloud.CloudStore
	cloud.EnvironmentCloudAliasStore
	configpkg.OpenStore
}

func NewCloudCmd(store cloudCommandStoreInterface, promptRunner shared.PromptRunner, selectRunner shared.SelectRunner, deps cloud.CloudDependencies) *cobra.Command {
	return shared.NewCommandGroup(
		"cloud",
		"Cloud provider utilities",
		newCloudInitCmd(store, promptRunner, selectRunner, deps),
		newCloudLoginCmd(store, promptRunner, selectRunner, deps),
		newCloudOIDCCmd(store, promptRunner, selectRunner, deps),
		newCloudSetCmd(store),
		newCloudRefreshCmd(store, deps),
	)
}

func newCloudRefreshCmd(store cloudCommandStoreInterface, deps cloud.CloudDependencies) *cobra.Command {
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
			return runCloudRefreshCommand(shared.CommandContext(cmd), store, args[0], args[1], deps)
		},
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runCloudRefreshCommand(ctx core.Context, store cloudCommandStoreInterface, tenant, environment string, deps cloud.CloudDependencies) error {
	result, err := open.ResolveOpen(store, sharedpkg.OpenParams{Tenant: tenant, Environment: environment})
	if err != nil {
		return err
	}
	ctx, closeEnvTrace := common.ActivateEnvTrace(ctx, result.Tenant, result.Environment)
	defer closeEnvTrace()
	refresh, err := cloud.RefreshHostAWSCredentials(ctx, store, result, deps)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		_, err := fmt.Fprintf(ctx.Stdout, "Dry run: host credential refresh planned for %s/%s.\n", result.Tenant, result.Environment)
		return err
	}
	return writeHostCredentialsRefreshed(ctx, result, refresh)
}

func writeHostCredentialsRefreshed(ctx core.Context, result configpkg.OpenResult, refresh cloud.HostCredentialsRefresh) error {
	line := fmt.Sprintf("Refreshed %s credentials for %s/%s in profile %s (%s)",
		refresh.Alias, result.Tenant, result.Environment, refresh.Profile, refresh.Path)
	if !refresh.Expiration.IsZero() {
		line += ", valid until " + refresh.Expiration.UTC().Format(time.RFC3339)
	}
	_, err := fmt.Fprintln(ctx.Stdout, line)
	return err
}

func newCloudInitCmd(store cloud.CloudStore, promptRunner shared.PromptRunner, selectRunner shared.SelectRunner, deps cloud.CloudDependencies) *cobra.Command {
	return shared.NewCommandGroup(
		"init",
		"Initialize cloud provider configuration",
		newCloudInitAWSCmd(store, promptRunner, deps),
		newCloudInitCloudflareCmd(store, promptRunner, selectRunner, deps),
		newCloudInitERunCmd(store, deps),
	)
}

func newCloudInitAWSCmd(store cloud.CloudStore, promptRunner shared.PromptRunner, deps cloud.CloudDependencies) *cobra.Command {
	var params cloud.InitAWSCloudProviderParams
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
			return shared.RunCloudInitAWSCommand(shared.CommandContext(cmd), store, promptRunner, params, deps)
		},
	}
	cmd.Flags().StringVar(&params.SSOStartURL, "sso-start-url", "", "AWS IAM Identity Center start URL")
	cmd.Flags().StringVar(&params.SSORegion, "sso-region", "", "AWS IAM Identity Center region")
	cmd.Flags().StringVar(&params.AccountID, "account-id", "", "AWS account ID to use for SSO login")
	cmd.Flags().StringVar(&params.RoleName, "role-name", "", "AWS permission set (IAM Identity Center) to assume for SSO login")
	cmd.Flags().StringVar(&params.Region, "region", "", "Default AWS region for the generated configuration")
	cmd.Flags().StringVar(&params.OIDCIssuerURL, "oidc-issuer-url", "", "OIDC issuer URL trusted by deployed ERun APIs")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func newCloudInitCloudflareCmd(store cloud.CloudStore, promptRunner shared.PromptRunner, selectRunner shared.SelectRunner, deps cloud.CloudDependencies) *cobra.Command {
	var params cloud.InitCloudflareCloudProviderParams
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
			return runCloudInitCloudflareCommand(shared.CommandContext(cmd), store, promptRunner, selectRunner, params, deps)
		},
	}
	cmd.Flags().StringVar(&params.AccountID, "account-id", "", "Cloudflare account ID the token belongs to (auto-resolved in guided setup)")
	cmd.Flags().StringVar(&params.TokenName, "token-name", "", "Label for the scoped API token (defaults in guided setup)")
	cmd.Flags().StringVar(&params.APIToken, "api-token", "", "Cloudflare API token value (Zone + DNS edit, plus any other scopes you'll use such as Cloudflare Pages)")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runCloudInitCloudflareCommand(ctx core.Context, store cloud.CloudStore, promptRunner shared.PromptRunner, selectRunner shared.SelectRunner, params cloud.InitCloudflareCloudProviderParams, deps cloud.CloudDependencies) error {
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
	provider, err := cloud.InitCloudflareCloudProvider(ctx, store, params, deps)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		_, err := fmt.Fprintln(ctx.Stdout, "Dry run: Cloudflare cloud provider initialization planned.")
		return err
	}
	return shared.WriteCloudProviderSaved(ctx, provider)
}

func newCloudInitERunCmd(store cloud.CloudStore, deps cloud.CloudDependencies) *cobra.Command {
	var params cloud.InitERunCloudProviderParams
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
		Example:      "  erun cloud init erun --api-url " + cloud.HostedPlatformAPIURL,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCloudInitERunCommand(shared.CommandContext(cmd), store, params, deps)
		},
	}
	cmd.Flags().StringVar(&params.APIURL, "api-url", "", "Base URL of the hosted erun platform's API")
	if err := cmd.MarkFlagRequired("api-url"); err != nil {
		panic(err)
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runCloudInitERunCommand(ctx core.Context, store cloud.CloudStore, params cloud.InitERunCloudProviderParams, deps cloud.CloudDependencies) error {
	provider, err := cloud.InitERunCloudProvider(ctx, store, params, deps)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun cloud provider initialization planned.")
		return err
	}
	return shared.WriteCloudProviderSaved(ctx, provider)
}

func resolveCloudflareAccountNonInteractive(ctx core.Context, token string, deps cloud.CloudDependencies) (string, error) {
	accounts, err := cloud.ResolveCloudflareAccounts(ctx, token, deps)
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

func runCloudflareInitWizard(ctx core.Context, promptRunner shared.PromptRunner, selectRunner shared.SelectRunner, params cloud.InitCloudflareCloudProviderParams, deps cloud.CloudDependencies) (cloud.InitCloudflareCloudProviderParams, error) {
	out := ctx.Stdout
	_, _ = fmt.Fprintln(out, "\nAdd a Cloudflare cloud alias")
	_, _ = fmt.Fprintln(out, "ERun needs a delegated Cloudflare API token. It never sees your Cloudflare password.")
	_, _ = fmt.Fprintln(out, "\nStep 1 of 2 · Create the token, then paste it below")
	_, _ = fmt.Fprintln(out, "  Open this page (you're already logged in to Cloudflare there):")
	_, _ = fmt.Fprintln(out, "    "+cloud.CloudflareCreateTokenURL)
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
		label, err := shared.RequiredCloudPrompt(promptRunner, "Token label", defaultCloudflareTokenLabel())
		if err != nil {
			return params, err
		}
		params.TokenName = label
	}
	return params, nil
}

func verifyCloudflareTokenInteractive(ctx core.Context, promptRunner shared.PromptRunner, deps cloud.CloudDependencies) (string, error) {
	for {
		token, err := requiredCloudSecretPrompt(promptRunner, "Cloudflare API token")
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintln(ctx.Stdout, "  Verifying with Cloudflare…")
		if _, err := cloud.VerifyCloudflareAPIToken(ctx, token, deps); err != nil {
			_, _ = fmt.Fprintln(ctx.Stdout, "  ✗ "+err.Error())
			continue
		}
		_, _ = fmt.Fprintln(ctx.Stdout, "  ✓ token is active")
		return token, nil
	}
}

func resolveCloudflareAccountInteractive(ctx core.Context, promptRunner shared.PromptRunner, selectRunner shared.SelectRunner, token string, deps cloud.CloudDependencies) (string, error) {
	accounts, err := cloud.ResolveCloudflareAccounts(ctx, token, deps)
	if err != nil || len(accounts) == 0 {
		_, _ = fmt.Fprintln(ctx.Stdout, "  Could not auto-resolve the account from the token; enter it manually.")
		return shared.RequiredCloudPrompt(promptRunner, "Cloudflare account ID", "")
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

func requiredCloudSecretPrompt(promptRunner shared.PromptRunner, label string) (string, error) {
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

func newCloudLoginCmd(store cloud.CloudStore, promptRunner shared.PromptRunner, selectRunner shared.SelectRunner, deps cloud.CloudDependencies) *cobra.Command {
	var alias string
	var flow string
	var scopes []string
	var force bool
	cmd := &cobra.Command{
		Use:          "login",
		Short:        "Login to a configured cloud provider alias",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCloudLoginCommand(shared.CommandContext(cmd), store, promptRunner, selectRunner, cloud.CloudLoginParams{Alias: alias, Flow: flow, Scopes: scopes, Force: force}, deps)
		},
	}
	cmd.Flags().StringVar(&alias, "alias", "", "Cloud provider alias to login")
	cmd.Flags().StringVar(&flow, "flow", "", "OIDC grant for an erun-hosted alias: \"device\", \"authcode\", or \"auto\" (default) which prefers the device grant and falls back to authorization code + PKCE when it cannot complete. Use \"authcode\" when the device page's authentication method fails — its loopback redirect reuses a browser session you already hold")
	cmd.Flags().StringArrayVar(&scopes, "scope", nil, "Extra OAuth scope to request on top of \"openid offline_access\" and the org-claim scope requested by default (repeatable). A provider's reserved scopes are often absent from its discovery document, so they can only be asked for by name")
	cmd.Flags().BoolVar(&force, "force", false, "Re-authenticate even when the stored session is still active. Needed to change the requested scopes or flow on an alias that is already signed in, since an active session otherwise short-circuits the login")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runCloudLoginCommand(ctx core.Context, store cloud.CloudStore, promptRunner shared.PromptRunner, selectRunner shared.SelectRunner, params cloud.CloudLoginParams, deps cloud.CloudDependencies) error {
	alias := strings.TrimSpace(params.Alias)
	if alias == "" {
		if ctx.DryRun {
			ctx.Trace("cloud login: dry-run requires --alias to be specified explicitly")
			_, err := fmt.Fprintln(ctx.Stdout, "Dry run: cloud login planned.")
			return err
		}
		selected, err := shared.SelectCloudAliasPrompt(store, selectRunner)
		if err != nil {
			return err
		}
		alias = selected
	}
	provider, err := cloud.ResolveCloudProvider(store, alias)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		ctx.Trace("check cloud provider token status")
		traceCloudLoginPlan(ctx, provider)
		_, err := fmt.Fprintf(ctx.Stdout, "Dry run: cloud login planned for %s.\n", provider.Alias)
		return err
	}
	status := cloud.CloudProviderTokenStatus(provider, deps)
	if status.Status == configpkg.CloudTokenStatusActive && !params.Force {
		return finishCloudLogin(ctx, store, status, deps)
	}
	return confirmAndLoginCloudProvider(ctx, store, promptRunner, alias, provider.Alias, status, params, deps)
}

// confirmAndLoginCloudProvider asks before starting an interactive login, and
// reports the status unchanged when the operator declines — a refusal is the
// current state, not a failure. Split out of runCloudLoginCommand so that
// function stays under the cyclop threshold; the sequence is unchanged.
func confirmAndLoginCloudProvider(ctx core.Context, store cloud.CloudStore, promptRunner shared.PromptRunner, alias, providerAlias string, status cloud.CloudProviderStatus, params cloud.CloudLoginParams, deps cloud.CloudDependencies) error {
	login, err := shared.ConfirmPrompt(promptRunner, fmt.Sprintf("Login to %s", providerAlias))
	if err != nil {
		return err
	}
	if !login {
		return writeCloudStatus(ctx, status)
	}
	status, err = cloud.LoginCloudProviderAlias(ctx, store, cloud.CloudLoginParams{Alias: alias, Force: true, Flow: params.Flow, Scopes: params.Scopes}, deps)
	if err != nil {
		return err
	}
	return finishCloudLogin(ctx, store, status, deps)
}

// finishCloudLogin writes the resolved status and, for an active erun-hosted
// alias, additionally proves the session actually authenticates against the
// real platform by calling GET /v1/whoami — the smallest end-to-end evidence
// that a token round-trip worked, not just that a token was obtained.
func finishCloudLogin(ctx core.Context, store cloud.CloudStore, status cloud.CloudProviderStatus, deps cloud.CloudDependencies) error {
	if err := writeCloudStatus(ctx, status); err != nil {
		return err
	}
	if status.Provider != configpkg.CloudProviderERun || status.Status != configpkg.CloudTokenStatusActive {
		return nil
	}
	return writeERunWhoami(ctx, store, status.Alias, deps)
}

func writeERunWhoami(ctx core.Context, store cloud.CloudStore, alias string, deps cloud.CloudDependencies) error {
	provider, err := cloud.ResolveCloudProvider(store, alias)
	if err != nil {
		return err
	}
	if provider.ERun == nil {
		return nil
	}
	client := platformclient.NewPlatformClient(provider.ERun.APIURL, func() (string, error) {
		token, err := cloud.CloudProviderBearerToken(ctx, store, cloud.CloudBearerParams{Alias: alias}, deps)
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

func traceCloudLoginPlan(ctx core.Context, provider configpkg.CloudProviderConfig) {
	switch provider.Provider {
	case configpkg.CloudProviderCloudflare:
		ctx.Trace("verify cloudflare api token via the Cloudflare API")
	case configpkg.CloudProviderERun:
		ctx.Trace("GET " + provider.OIDCIssuerURL + "/.well-known/openid-configuration")
		ctx.Trace("start the device authorization grant (or the authorization code + pkce fallback if the issuer advertises no device endpoint)")
	default:
		ctx.TraceCommand("", "aws", "sso", "login", "--profile", provider.Profile)
	}
}

func newCloudOIDCCmd(store cloud.CloudStore, promptRunner shared.PromptRunner, selectRunner shared.SelectRunner, deps cloud.CloudDependencies) *cobra.Command {
	var alias string
	var audience string
	cmd := &cobra.Command{
		Use:          "oidc",
		Short:        "Refresh the OIDC issuer for a configured cloud provider alias",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCloudOIDCCommand(shared.CommandContext(cmd), store, promptRunner, selectRunner, cloud.CloudBearerParams{
				Alias:    alias,
				Audience: audience,
			}, deps)
		},
	}
	cmd.Flags().StringVar(&alias, "alias", "", "Cloud provider alias to refresh")
	cmd.Flags().StringVar(&audience, "audience", configpkg.CloudProviderBearerAudience, "Audience for the AWS web identity bearer token")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runCloudOIDCCommand(ctx core.Context, store cloud.CloudStore, _ shared.PromptRunner, selectRunner shared.SelectRunner, params cloud.CloudBearerParams, deps cloud.CloudDependencies) error {
	alias := strings.TrimSpace(params.Alias)
	if alias == "" {
		selected, err := shared.SelectCloudAliasPrompt(store, selectRunner)
		if err != nil {
			return err
		}
		alias = selected
	}
	provider, err := cloud.ResolveCloudProvider(store, alias)
	if err != nil {
		return err
	}
	if provider.Provider != configpkg.CloudProviderAWS {
		return fmt.Errorf("cloud provider alias %q is a %q-type alias, which does not use AWS web-identity federation; its OIDC issuer is set at `cloud init`", provider.Alias, provider.Provider)
	}
	status, _, err := cloud.SetupCloudProviderOIDC(ctx, store, cloud.CloudBearerParams{
		Alias:    alias,
		Audience: params.Audience,
	}, deps)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		_, err := fmt.Fprintln(ctx.Stdout, "Dry run: cloud provider OIDC setup planned.")
		return err
	}
	_, err = fmt.Fprintf(ctx.Stdout, "Saved OIDC issuer %s for %s\n", status.OIDCIssuerURL, status.Alias)
	return err
}

func newCloudSetCmd(store cloud.EnvironmentCloudAliasStore) *cobra.Command {
	var alias string
	cmd := &cobra.Command{
		Use:          "set TENANT ENVIRONMENT",
		Short:        "Set the cloud provider alias for an environment",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCloudSetCommand(shared.CommandContext(cmd), store, cloud.SetEnvironmentCloudAliasParams{
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
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runCloudSetCommand(ctx core.Context, store cloud.EnvironmentCloudAliasStore, params cloud.SetEnvironmentCloudAliasParams) error {
	if _, err := cloud.SetEnvironmentCloudProviderAlias(ctx, store, params); err != nil {
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

func writeCloudStatus(ctx core.Context, status cloud.CloudProviderStatus) error {
	line := fmt.Sprintf("%s: %s", status.Alias, status.Status)
	if strings.TrimSpace(status.Message) != "" {
		line += " (" + strings.TrimSpace(status.Message) + ")"
	}
	_, err := fmt.Fprintln(ctx.Stdout, line)
	return err
}
