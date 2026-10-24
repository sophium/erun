package platform

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/platform"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/spf13/cobra"
)

func newPlatformProvisionCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	var params model.PlatformProvisionParams
	cmd := &cobra.Command{
		Use:   "provision",
		Short: "Preview the ordered plan for provisioning a hosted environment",
		Long: "Preview the ordered plan for provisioning a hosted environment.\n\n" +
			"Asks the platform to resolve tenant, quota, context bootstrap (or reuse), namespace, " +
			"registration, and deploy into one ordered plan, without executing any of it or writing " +
			"to the database. Pass either --context-name (with --context-alias and --context-region) " +
			"to bootstrap a new cluster, or --kubernetes-context to reuse an existing one.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example: "  erun platform provision --env-name prod --env-type runtime --kubernetes-context prod-cluster\n" +
			"  erun platform provision --env-name prod --env-type runtime --context-name prod --context-alias aws-main --context-region eu-west-2",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := clicmd.CommandContext(cmd)
			result, err := platform.RunPlatformProvision(ctx, store, *alias, params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform provision preview planned.")
				return err
			}
			if ctx.Output != model.OutputJSON {
				if err := writePlatformProvisionResult(ctx, result); err != nil {
					return err
				}
			}
			return ctx.WriteResult(result)
		},
	}
	cmd.Flags().StringVar(&params.Environment.Name, "env-name", "", "Environment name to provision")
	cmd.Flags().StringVar(&params.Environment.Type, "env-type", "", "Environment type: runtime, remote-agent, or local-agent")
	cmd.Flags().StringVar(&params.KubernetesContext, "kubernetes-context", "", "Reuse an existing kubernetes context instead of bootstrapping one")
	contextName, contextAlias, contextRegion := "", "", ""
	var contextInstanceType, contextDiskType string
	var contextDiskSizeGB int
	cmd.Flags().StringVar(&contextName, "context-name", "", "Name for a new cloud context to bootstrap")
	cmd.Flags().StringVar(&contextAlias, "context-alias", "", "Cloud provider alias to bootstrap the new context with")
	cmd.Flags().StringVar(&contextRegion, "context-region", "", "Cloud region for the new context")
	cmd.Flags().StringVar(&contextInstanceType, "context-instance-type", "", "Instance type for the new context's VM")
	cmd.Flags().StringVar(&contextDiskType, "context-disk-type", "", "Root disk type for the new context")
	cmd.Flags().IntVar(&contextDiskSizeGB, "context-disk-size-gb", 0, "Root disk size in GB for the new context")
	originalRunE := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(contextName) != "" {
			params.Context = &model.PlatformProvisionContext{
				Name:               contextName,
				CloudProviderAlias: contextAlias,
				Region:             contextRegion,
				InstanceType:       contextInstanceType,
				DiskType:           contextDiskType,
				DiskSizeGB:         contextDiskSizeGB,
			}
		}
		return originalRunE(cmd, args)
	}
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func writePlatformProvisionResult(ctx core.Context, result model.PlatformProvisionResult) error {
	for _, line := range result.Plan {
		if _, err := fmt.Fprintln(ctx.Stdout, "  "+line); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(ctx.Stdout, "quota ok: %t\n", result.QuotaOk)
	return err
}
