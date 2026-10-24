package platform

import (
	"fmt"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/platform"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/spf13/cobra"
)

func newPlatformContextCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	return shared.NewCommandGroup(
		"context",
		"Manage the erun platform's own cloud contexts (managed clusters)",
		newPlatformContextCreateCmd(store, alias, deps),
		newPlatformContextListCmd(store, alias, deps),
		newPlatformContextGetCmd(store, alias, deps),
	)
}

func newPlatformContextCreateCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	var params platformclient.PlatformCreateContextParams
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Bootstrap or preview a cloud context (managed cluster) on the erun platform",
		Long: "Bootstrap or preview a cloud context (managed cluster) on the erun platform.\n\n" +
			"Without --preview this launches a real cloud VM and provisions k3s on it, billing the " +
			"tenant's cloud account until stopped. --preview asks the platform to resolve and " +
			"return the bootstrap plan without creating anything (a server-side check, distinct " +
			"from --dry-run, which skips the network call entirely).",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun platform context create --name prod --alias aws-main --region eu-west-2 --preview\n  erun platform context create --name prod --alias aws-main --region eu-west-2",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := shared.CommandContext(cmd)
			result, err := platform.RunPlatformCreateContext(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform context creation planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
				if err := writePlatformCreateContextResult(ctx, result); err != nil {
					return err
				}
			}
			return ctx.WriteResult(result)
		},
	}
	cmd.Flags().StringVar(&params.Name, "name", "", "Kubernetes context name to create")
	cmd.Flags().StringVar(&params.CloudProviderAlias, "alias", "", "Cloud provider alias (on the tenant's own account) to bootstrap with")
	cmd.Flags().StringVar(&params.Region, "region", "", "Cloud region for the context")
	cmd.Flags().StringVar(&params.InstanceType, "instance-type", "", "Instance type for the context's VM")
	cmd.Flags().StringVar(&params.DiskType, "disk-type", "", "Root disk type")
	cmd.Flags().IntVar(&params.DiskSizeGB, "disk-size-gb", 0, "Root disk size in GB")
	cmd.Flags().BoolVar(&params.Preview, "preview", false, "Resolve and return the bootstrap plan without creating anything")
	shared.AddDryRunFlag(cmd)
	return cmd
}

func writePlatformCreateContextResult(ctx core.Context, result platformclient.PlatformCreateContextResult) error {
	for _, line := range result.Plan {
		if _, err := fmt.Fprintln(ctx.Stdout, "  "+line); err != nil {
			return err
		}
	}
	if result.Context == nil {
		return nil
	}
	_, err := fmt.Fprintf(ctx.Stdout, "created context %s (%s) status=%s\n", result.Context.Name, result.Context.ContextID, result.Context.Status)
	return err
}

func newPlatformContextListCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List the caller's tenant's cloud contexts on the erun platform",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := shared.CommandContext(cmd)
			contexts, err := platform.RunPlatformListContexts(ctx, store, *alias, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform context list planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
				if err := writePlatformContextList(ctx, contexts); err != nil {
					return err
				}
			}
			return ctx.WriteResult(contexts)
		},
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}

func writePlatformContextList(ctx core.Context, contexts []platformclient.PlatformContext) error {
	if len(contexts) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "no contexts")
		return err
	}
	for _, cloudContext := range contexts {
		if _, err := fmt.Fprintf(ctx.Stdout, "  - %s (%s) status=%s region=%s\n", cloudContext.Name, cloudContext.ContextID, cloudContext.Status, shared.QuotedValueOrNone(cloudContext.Region)); err != nil {
			return err
		}
	}
	return nil
}

func newPlatformContextGetCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "get CONTEXT_ID",
		Short:        "Fetch one cloud context by id",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.CommandContext(cmd)
			cloudContext, err := platform.RunPlatformGetContext(ctx, store, *alias, args[0], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform context lookup planned.")
				return err
			}
			if ctx.Output != core.OutputJSON {
				if _, err := fmt.Fprintf(ctx.Stdout, "  - %s (%s) status=%s region=%s\n", cloudContext.Name, cloudContext.ContextID, cloudContext.Status, shared.QuotedValueOrNone(cloudContext.Region)); err != nil {
					return err
				}
			}
			return ctx.WriteResult(cloudContext)
		},
	}
	shared.AddDryRunFlag(cmd)
	return cmd
}
