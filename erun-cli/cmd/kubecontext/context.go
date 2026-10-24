package kubecontext

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/spf13/cobra"
)

func NewContextCmd(store cloud.CloudContextStore, promptRunner shared.PromptRunner, selectRunner shared.SelectRunner, deps cloud.CloudContextDependencies) *cobra.Command {
	return shared.NewCommandGroup(
		"context",
		"Manage ERun cloud Kubernetes contexts",
		newContextListCmd(store, deps),
		newContextInitCmd(store, promptRunner, selectRunner, deps),
		newContextStopCmd(store, deps),
		newContextStartCmd(store, deps),
		newContextDisableAPIStopCmd(store, deps),
		newContextEnableAPIStopCmd(store, deps),
	)
}

func newContextListCmd(store cloud.CloudContextStore, deps cloud.CloudContextDependencies) *cobra.Command {
	return &cobra.Command{
		Use:          "list",
		Short:        "List managed ERun cloud contexts",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runContextListCommand(shared.CommandContext(cmd), store, deps)
		},
	}
}

func runContextListCommand(ctx core.Context, store cloud.CloudContextStore, deps cloud.CloudContextDependencies) error {
	// Status isn't persisted, so refresh it from AWS.
	result, err := cloud.RefreshCloudContextList(ctx, store, deps)
	if err != nil {
		return err
	}
	return WriteCloudContextList(ctx, result)
}

func WriteCloudContextList(ctx core.Context, result model.CloudContextListResult) error {
	if err := ctx.WriteResult(result); err != nil {
		return err
	}
	if ctx.Output == model.OutputJSON {
		return nil
	}
	if _, err := fmt.Fprintln(ctx.Stdout, "Cloud Contexts:"); err != nil {
		return err
	}
	// A batched refresh failure is reported once, not on every row it covered.
	for _, failure := range result.RefreshFailures {
		line := "  status refresh failed for alias=" + shared.QuotedValueOrNone(failure.Alias) +
			" region=" + shared.QuotedValueOrNone(failure.Region) + ": " + failure.Message
		if _, err := fmt.Fprintln(ctx.Stdout, line); err != nil {
			return err
		}
	}
	if len(result.CloudContexts) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "  none")
		return err
	}
	for _, context := range result.CloudContexts {
		if err := writeCloudContext(ctx, context); err != nil {
			return err
		}
	}
	return nil
}

func newContextInitCmd(store cloud.CloudContextStore, promptRunner shared.PromptRunner, selectRunner shared.SelectRunner, deps cloud.CloudContextDependencies) *cobra.Command {
	var params model.InitCloudContextParams
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a managed cloud context",
		Long: "Create a managed cloud context.\n\n" +
			"Launches a cloud VM and provisions k3s on it, then writes the kubeconfig context " +
			"so environments can deploy to it. The instance bills until you stop it " +
			"(erun context stop).",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runContextInitCommand(shared.CommandContext(cmd), store, promptRunner, selectRunner, params, deps)
		},
	}
	cmd.Flags().StringVar(&params.CloudProviderAlias, "alias", "", "Cloud provider alias to use")
	cmd.Flags().StringVar(&params.Name, "context", "", "Kubernetes context name to create")
	cmd.Flags().StringVar(&params.Region, "region", cloud.DefaultCloudContextRegion, "Cloud region for the context")
	cmd.Flags().StringVar(&params.InstanceType, "instance-type", config.DefaultCloudContextInstanceType, "EC2 instance type")
	cmd.Flags().IntVar(&params.DiskSizeGB, "disk-size", config.DefaultCloudContextDiskSizeGB, "Root disk size in GB")
	cmd.Flags().StringVar(&params.DiskType, "disk-type", config.DefaultCloudContextDiskType, "Root disk type")
	cmd.Flags().StringVar(&params.SubnetID, "subnet-id", "", "Optional EC2 subnet ID")
	cmd.Flags().StringVar(&params.SecurityGroupID, "security-group-id", "", "Optional EC2 security group ID")
	cmd.Flags().StringVar(&params.KeyName, "key-name", "", "Optional EC2 key pair name")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func runContextInitCommand(ctx core.Context, store cloud.CloudContextStore, promptRunner shared.PromptRunner, selectRunner shared.SelectRunner, params model.InitCloudContextParams, deps cloud.CloudContextDependencies) error {
	var err error
	params.CloudProviderAlias, err = contextProviderAlias(store, selectRunner, params.CloudProviderAlias)
	if err != nil {
		return err
	}
	params, err = promptContextInitParams(promptRunner, selectRunner, params)
	if err != nil {
		return err
	}
	status, err := cloud.InitCloudContext(ctx, store, params, deps)
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

func newContextStopCmd(store cloud.CloudContextStore, deps cloud.CloudContextDependencies) *cobra.Command {
	return newContextPowerCmd("stop", "Stop a managed ERun cloud context", false, store, deps, cloud.StopCloudContext)
}

func newContextStartCmd(store cloud.CloudContextStore, deps cloud.CloudContextDependencies) *cobra.Command {
	return newContextPowerCmd("start", "Start a managed ERun cloud context", true, store, deps, cloud.StartCloudContext)
}

func newContextDisableAPIStopCmd(store cloud.CloudContextStore, deps cloud.CloudContextDependencies) *cobra.Command {
	return newContextStopProtectionCmd(
		"disable-api-stop",
		"Lock a cloud context against stop calls (sets AWS DisableApiStop=true)",
		true,
		store, deps,
	)
}

func newContextEnableAPIStopCmd(store cloud.CloudContextStore, deps cloud.CloudContextDependencies) *cobra.Command {
	return newContextStopProtectionCmd(
		"enable-api-stop",
		"Restore normal stop behaviour for a cloud context (sets AWS DisableApiStop=false)",
		false,
		store, deps,
	)
}

// Stop protection also holds off the idle monitor.
func newContextStopProtectionCmd(use, short string, enabled bool, store cloud.CloudContextStore, deps cloud.CloudContextDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          use + " CONTEXT",
		Short:        short,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.CommandContext(cmd)
			status, err := cloud.SetCloudContextStopProtection(ctx, store, model.CloudContextStopProtectionParams{
				Name:    args[0],
				Enabled: enabled,
			}, deps)
			if err != nil {
				return err
			}
			return writeCloudContext(ctx, status)
		},
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}

func newContextPowerCmd(use, short string, supportsForce bool, store cloud.CloudContextStore, deps cloud.CloudContextDependencies, run func(core.Context, cloud.CloudContextStore, model.CloudContextParams, cloud.CloudContextDependencies) (model.CloudContextStatus, error)) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:          use + " CONTEXT",
		Short:        short,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.CommandContext(cmd)
			status, err := run(ctx, store, model.CloudContextParams{Name: args[0], Force: force}, deps)
			if err != nil {
				return err
			}
			return writeCloudContext(ctx, status)
		},
	}
	if supportsForce {
		cmd.Flags().BoolVar(&force, "force", false, "Override the working-hours start gate")
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}

func contextProviderAlias(store cloud.CloudContextStore, selectRunner shared.SelectRunner, alias string) (string, error) {
	alias = strings.TrimSpace(alias)
	if alias != "" {
		return alias, nil
	}
	return shared.SelectCloudAliasPrompt(store, selectRunner)
}

func promptContextInitParams(promptRunner shared.PromptRunner, selectRunner shared.SelectRunner, params model.InitCloudContextParams) (model.InitCloudContextParams, error) {
	var err error
	if strings.TrimSpace(params.Region) == "" {
		params.Region, err = selectOrKeepString(selectRunner, "Cloud region", cloud.CloudContextRegions(), params.Region)
		if err != nil {
			return params, err
		}
	}
	params.InstanceType, err = selectOrKeepString(selectRunner, "Instance type", cloud.CloudContextInstanceTypes(), params.InstanceType)
	if err != nil {
		return params, err
	}
	params.DiskSizeGB, err = selectOrKeepInt(selectRunner, "Disk size", cloud.CloudContextDiskSizesGB(), params.DiskSizeGB)
	if err != nil {
		return params, err
	}
	return params, nil
}

func selectOrKeepString(selectRunner shared.SelectRunner, label string, options []string, current string) (string, error) {
	current = strings.TrimSpace(current)
	if current != "" {
		return current, nil
	}
	_, value, err := selectRunner(promptui.Select{Label: label, Items: options})
	return value, err
}

func selectOrKeepInt(selectRunner shared.SelectRunner, label string, options []int, current int) (int, error) {
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

func writeCloudContext(ctx core.Context, status model.CloudContextStatus) error {
	context := status.CloudContextConfig
	line := "  - " + context.Name
	line += " provider=" + shared.QuotedValueOrNone(context.Provider)
	line += " alias=" + shared.QuotedValueOrNone(context.CloudProviderAlias)
	line += " region=" + shared.QuotedValueOrNone(context.Region)
	line += " instance=" + shared.QuotedValueOrNone(context.InstanceID)
	line += " type=" + shared.QuotedValueOrNone(context.InstanceType)
	line += " disk=" + fmt.Sprintf("%dGB/%s", context.DiskSizeGB, context.DiskType)
	line += " kube-context=" + shared.QuotedValueOrNone(context.KubernetesContext)
	line += " status=" + shared.QuotedValueOrNone(status.Status)
	if strings.TrimSpace(context.PublicIP) != "" {
		line += " public-ip=" + shared.QuotedValueOrNone(context.PublicIP)
	}
	if status.StopProtectionKnown {
		line += " stop-protection=" + strconv.FormatBool(status.StopProtection)
	}
	if strings.TrimSpace(status.Message) != "" {
		line += " message=" + shared.QuotedValueOrNone(status.Message)
	}
	_, err := fmt.Fprintln(ctx.Stdout, line)
	return err
}
