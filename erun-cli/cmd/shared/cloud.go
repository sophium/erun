package shared

import (
	"fmt"
	"strings"

	"github.com/manifoldco/promptui"
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// CloudDependencies tolerates a missing secret store: when it can't be created
// the store stays nil, and Cloudflare operations that need the scoped token then
// fail clearly rather than crashing.
func CloudDependencies() cloud.CloudDependencies {
	return cloud.DefaultCloudDependencies()
}

// RefreshEnvironmentHostCredentials re-injects the operator's short-lived AWS
// credentials into an env's runtime pod. The root config store and the cloud
// dependency set are stateless values, so they are constructed here instead of
// widening open's already-long dependency list.
func RefreshEnvironmentHostCredentials(ctx core.Context, result configpkg.OpenResult) (cloud.HostCredentialsRefresh, error) {
	return cloud.RefreshHostAWSCredentials(ctx, configpkg.ConfigStore{}, result, CloudDependencies())
}

func RunCloudInitAWSCommand(ctx core.Context, store cloud.CloudStore, promptRunner PromptRunner, params cloud.InitAWSCloudProviderParams, deps cloud.CloudDependencies) error {
	var err error
	if strings.TrimSpace(params.Profile) == "" && awsInitParamsNeedPrompt(params) {
		printAWSInitGuidance(ctx)
	}
	params, err = promptAWSInitParams(promptRunner, params)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return traceAWSCloudInitDryRun(ctx, params)
	}
	provider, err := cloud.InitAWSCloudProvider(ctx, store, params, deps)
	if err != nil {
		return err
	}
	return WriteCloudProviderSaved(ctx, provider)
}

func traceAWSCloudInitDryRun(ctx core.Context, params cloud.InitAWSCloudProviderParams) error {
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
	traceAWSBearerTokenPlan(ctx, params, configpkg.CloudProviderBearerAudience)
	ctx.Trace("write erun root cloud provider alias")
	ctx.Trace("write cloud provider OIDC issuer resolved from AWS web identity token")
	_, err := fmt.Fprintln(ctx.Stdout, "Dry run: AWS cloud provider initialization planned.")
	return err
}

func promptAWSInitParams(promptRunner PromptRunner, params cloud.InitAWSCloudProviderParams) (cloud.InitAWSCloudProviderParams, error) {
	if strings.TrimSpace(params.Profile) != "" {
		return params, nil
	}
	return promptMissingAWSInitParams(promptRunner, params)
}

func promptMissingAWSInitParams(promptRunner PromptRunner, params cloud.InitAWSCloudProviderParams) (cloud.InitAWSCloudProviderParams, error) {
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
func awsInitParamsNeedPrompt(params cloud.InitAWSCloudProviderParams) bool {
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

func printAWSInitGuidance(ctx core.Context) {
	_, _ = fmt.Fprint(ctx.Stdout, awsInitGuidance)
}

func promptCloudValueIfEmpty(promptRunner PromptRunner, value, label, defaultValue string) (string, error) {
	if strings.TrimSpace(value) != "" {
		return value, nil
	}
	return RequiredCloudPrompt(promptRunner, label, defaultValue)
}

func RequiredCloudPrompt(promptRunner PromptRunner, label, defaultValue string) (string, error) {
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

func traceAWSConfigureSetPlan(ctx core.Context, params cloud.InitAWSCloudProviderParams, profile string) {
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

func traceAWSBearerTokenPlan(ctx core.Context, params cloud.InitAWSCloudProviderParams, audience string) {
	profile := strings.TrimSpace(params.Profile)
	if profile == "" {
		profile = "erun-sso-<timestamp>"
	}
	traceAWSBearerTokenCommand(ctx, profile, audience)
}

func traceAWSBearerTokenCommand(ctx core.Context, profile, audience string) {
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

func traceAWSEnableOIDCCommand(ctx core.Context, profile string) {
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

func SelectCloudAliasPrompt(store cloud.CloudStore, selectRunner SelectRunner) (string, error) {
	providers, err := cloud.ListCloudProviders(store)
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

func WriteCloudProviderSaved(ctx core.Context, provider configpkg.CloudProviderConfig) error {
	_, err := fmt.Fprintf(ctx.Stdout, "Saved cloud provider alias %s\n", provider.Alias)
	return err
}
