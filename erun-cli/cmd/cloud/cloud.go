package cloud

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/cmd/shared"
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/e2e"
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
	ctx, closeEnvTrace := e2e.ActivateEnvTrace(ctx, result.Tenant, result.Environment)
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
