package cmd

import (
	"fmt"
	"strings"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

type cloudCommandStoreInterface interface {
	common.CloudStore
	common.EnvironmentCloudAliasStore
}

func newCloudCmd(store cloudCommandStoreInterface, promptRunner PromptRunner, selectRunner SelectRunner, deps common.CloudDependencies) *cobra.Command {
	return newCommandGroup(
		"cloud",
		"Cloud provider utilities",
		newCloudInitCmd(store, promptRunner, deps),
		newCloudLoginCmd(store, promptRunner, selectRunner, deps),
		newCloudSetCmd(store),
	)
}

func newCloudInitCmd(store common.CloudStore, promptRunner PromptRunner, deps common.CloudDependencies) *cobra.Command {
	return newCommandGroup(
		"init",
		"Initialize cloud provider configuration",
		newCloudInitAWSCmd(store, promptRunner, deps),
	)
}

func newCloudInitAWSCmd(store common.CloudStore, promptRunner PromptRunner, deps common.CloudDependencies) *cobra.Command {
	var params common.InitAWSCloudProviderParams
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Initialize an AWS SSO cloud provider alias",
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
		ctx.Trace("write erun root cloud provider alias")
		_, err := fmt.Fprintln(ctx.Stdout, "Dry run: AWS cloud provider initialization planned.")
		return err
	}
	provider, err := common.InitAWSCloudProvider(ctx, store, params, deps)
	if err != nil {
		return err
	}
	return writeCloudProviderSaved(ctx, provider)
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
	config, err := common.SetEnvironmentCloudProviderAlias(ctx, store, params)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		_, err = fmt.Fprintln(ctx.Stdout, "Dry run: cloud provider alias update planned.")
		return err
	}
	_, err = fmt.Fprintf(ctx.Stdout, "Set cloud provider alias %s for %s/%s\n", config.CloudProviderAlias, strings.TrimSpace(params.Tenant), strings.TrimSpace(params.Environment))
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
