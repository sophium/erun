package cloud

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/spf13/cobra"
)

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
