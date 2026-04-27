package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newContextCmd(store common.CloudContextStore, promptRunner PromptRunner, selectRunner SelectRunner, deps common.CloudContextDependencies) *cobra.Command {
	return newCommandGroup(
		"context",
		"Manage ERun cloud Kubernetes contexts",
		newContextListCmd(store),
		newContextInitCmd(store, promptRunner, selectRunner, deps),
		newContextStopCmd(store, deps),
		newContextStartCmd(store, deps),
	)
}

func newContextListCmd(store common.CloudContextStore) *cobra.Command {
	return &cobra.Command{
		Use:          "list",
		Short:        "List managed ERun cloud contexts",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runContextListCommand(commandContext(cmd), store)
		},
	}
}

func runContextListCommand(ctx common.Context, store common.CloudContextStore) error {
	contexts, err := common.ListCloudContextStatuses(store)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(ctx.Stdout, "Cloud Contexts:"); err != nil {
		return err
	}
	if len(contexts) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "  none")
		return err
	}
	for _, context := range contexts {
		if err := writeCloudContext(ctx, context); err != nil {
			return err
		}
	}
	return nil
}

func newContextInitCmd(store common.CloudContextStore, promptRunner PromptRunner, selectRunner SelectRunner, deps common.CloudContextDependencies) *cobra.Command {
	var params common.InitCloudContextParams
	cmd := &cobra.Command{
		Use:          "init",
		Short:        "Initialize a managed cloud k3s context",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runContextInitCommand(commandContext(cmd), store, promptRunner, selectRunner, params, deps)
		},
	}
	cmd.Flags().StringVar(&params.CloudProviderAlias, "alias", "", "Cloud provider alias to use")
	cmd.Flags().StringVar(&params.Name, "context", "", "Kubernetes context name to create")
	cmd.Flags().StringVar(&params.Region, "region", common.DefaultCloudContextRegion, "Cloud region for the context")
	cmd.Flags().StringVar(&params.InstanceType, "instance-type", common.DefaultCloudContextInstanceType, "EC2 instance type")
	cmd.Flags().IntVar(&params.DiskSizeGB, "disk-size", common.DefaultCloudContextDiskSizeGB, "Root disk size in GB")
	cmd.Flags().StringVar(&params.DiskType, "disk-type", common.DefaultCloudContextDiskType, "Root disk type")
	cmd.Flags().StringVar(&params.SubnetID, "subnet-id", "", "Optional EC2 subnet ID")
	cmd.Flags().StringVar(&params.SecurityGroupID, "security-group-id", "", "Optional EC2 security group ID")
	cmd.Flags().StringVar(&params.KeyName, "key-name", "", "Optional EC2 key pair name")
	addDryRunFlag(cmd)
	return cmd
}

func runContextInitCommand(ctx common.Context, store common.CloudContextStore, promptRunner PromptRunner, selectRunner SelectRunner, params common.InitCloudContextParams, deps common.CloudContextDependencies) error {
	var err error
	params.CloudProviderAlias, err = contextProviderAlias(store, selectRunner, params.CloudProviderAlias)
	if err != nil {
		return err
	}
	params, err = promptContextInitParams(promptRunner, selectRunner, params)
	if err != nil {
		return err
	}
	status, err := common.InitCloudContext(ctx, store, params, deps)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		_, err = fmt.Fprintln(ctx.Stdout, "Dry run: cloud context initialization planned.")
		if err != nil {
			return err
		}
	}
	return writeCloudContext(ctx, status)
}

func newContextStopCmd(store common.CloudContextStore, deps common.CloudContextDependencies) *cobra.Command {
	return newContextPowerCmd("stop", "Stop a managed ERun cloud context", store, deps, common.StopCloudContext)
}

func newContextStartCmd(store common.CloudContextStore, deps common.CloudContextDependencies) *cobra.Command {
	return newContextPowerCmd("start", "Start a managed ERun cloud context", store, deps, common.StartCloudContext)
}

func newContextPowerCmd(use, short string, store common.CloudContextStore, deps common.CloudContextDependencies, run func(common.Context, common.CloudContextStore, common.CloudContextParams, common.CloudContextDependencies) (common.CloudContextStatus, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:          use + " CONTEXT",
		Short:        short,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			status, err := run(ctx, store, common.CloudContextParams{Name: args[0]}, deps)
			if err != nil {
				return err
			}
			return writeCloudContext(ctx, status)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func contextProviderAlias(store common.CloudContextStore, selectRunner SelectRunner, alias string) (string, error) {
	alias = strings.TrimSpace(alias)
	if alias != "" {
		return alias, nil
	}
	return selectCloudAliasPrompt(store, selectRunner)
}

func promptContextInitParams(promptRunner PromptRunner, selectRunner SelectRunner, params common.InitCloudContextParams) (common.InitCloudContextParams, error) {
	var err error
	if strings.TrimSpace(params.Region) == "" {
		params.Region, err = selectOrKeepString(selectRunner, "Cloud region", common.CloudContextRegions(), params.Region)
		if err != nil {
			return params, err
		}
	}
	params.InstanceType, err = selectOrKeepString(selectRunner, "Instance type", common.CloudContextInstanceTypes(), params.InstanceType)
	if err != nil {
		return params, err
	}
	params.DiskSizeGB, err = selectOrKeepInt(selectRunner, "Disk size", common.CloudContextDiskSizesGB(), params.DiskSizeGB)
	if err != nil {
		return params, err
	}
	return params, nil
}

func selectOrKeepString(selectRunner SelectRunner, label string, options []string, current string) (string, error) {
	current = strings.TrimSpace(current)
	if current != "" {
		return current, nil
	}
	_, value, err := selectRunner(promptui.Select{Label: label, Items: options})
	return value, err
}

func selectOrKeepInt(selectRunner SelectRunner, label string, options []int, current int) (int, error) {
	if current > 0 {
		return current, nil
	}
	items := make([]string, 0, len(options))
	for _, option := range options {
		items = append(items, strconv.Itoa(option))
	}
	_, value, err := selectRunner(promptui.Select{Label: label, Items: items})
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(value)
}

func writeCloudContext(ctx common.Context, status common.CloudContextStatus) error {
	context := status.CloudContextConfig
	line := "  - " + context.Name
	line += " provider=" + quotedValueOrNone(context.Provider)
	line += " alias=" + quotedValueOrNone(context.CloudProviderAlias)
	line += " region=" + quotedValueOrNone(context.Region)
	line += " instance=" + quotedValueOrNone(context.InstanceID)
	line += " type=" + quotedValueOrNone(context.InstanceType)
	line += " disk=" + fmt.Sprintf("%dGB/%s", context.DiskSizeGB, context.DiskType)
	line += " kube-context=" + quotedValueOrNone(context.KubernetesContext)
	line += " status=" + quotedValueOrNone(context.Status)
	if strings.TrimSpace(context.PublicIP) != "" {
		line += " public-ip=" + quotedValueOrNone(context.PublicIP)
	}
	if strings.TrimSpace(status.Message) != "" {
		line += " message=" + quotedValueOrNone(status.Message)
	}
	_, err := fmt.Fprintln(ctx.Stdout, line)
	return err
}
