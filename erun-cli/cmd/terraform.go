package cmd

import (
	"bufio"
	"fmt"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newTerraformCmd(store common.TerraformStore, findProjectRoot common.ProjectFinderFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "terraform",
		Short: "Run a platform's per-env Terraform from the right folder automatically",
		Long: "Run Terraform against a hosted platform's per-environment root (terraform-<tenant>/<environment>/, " +
			"<tenant>-devops/terraform-<tenant>/<environment>/, or the paths.terraform base from .erun/config.yaml) " +
			"without hand-running terraform or cd-ing into the folder.\n\n" +
			"erun resolves the env's folder from the current scope, picks up the symlinked common.tf, and runs that " +
			"env's main.tf with its <environment>.tfvars. State and the provider cache live on the durable home " +
			"directory (not in the playbook tree), so they survive a runtime pod restart. Use a subcommand: " +
			"`apply`, `plan`, or `destroy`.",
		SilenceUsage: true,
	}
	cmd.AddCommand(
		newTerraformOperationCmd(store, findProjectRoot, common.TerraformApply, "apply [TENANT] [ENVIRONMENT]",
			"Plan and apply the env's Terraform (prompts for the environment name)",
			"Plan and apply the env's Terraform root.\n\n"+
				"Resolves the env's Terraform root (terraform-<tenant>/<environment>/ or "+
				"<tenant>-devops/terraform-<tenant>/<environment>/), runs init -> fmt -> plan, then prompts you to type "+
				"the environment name before applying — a guard against applying to the wrong env. It mutates real cloud "+
				"and cluster state (DNS, TLS, ingress, workloads); state is kept on the durable home directory. Pass "+
				"--confirm-environment <env> for non-interactive use, or --dry-run to preview the resolved terraform commands.",
			"  erun terraform apply frs prod\n  erun terraform apply frs prod --dry-run"),
		newTerraformOperationCmd(store, findProjectRoot, common.TerraformPlan, "plan [TENANT] [ENVIRONMENT]",
			"Show the env's Terraform plan without applying", "", "  erun terraform plan frs prod"),
		newTerraformOperationCmd(store, findProjectRoot, common.TerraformDestroy, "destroy [TENANT] [ENVIRONMENT]",
			"Plan and apply a destroy of the env's Terraform (prompts for the environment name)",
			"Plan and apply a destroy of the env's Terraform root.\n\n"+
				"Resolves the env's Terraform root (terraform-<tenant>/ or <tenant>-devops/terraform-<tenant>/), plans a destroy, then prompts you to type the environment "+
				"name before applying. This tears down the env's Terraform-managed cloud and cluster resources — "+
				"irreversible. Pass --confirm-environment <env> for non-interactive use, or --dry-run to preview.",
			"  erun terraform destroy frs prod --dry-run"),
	)
	return cmd
}

func newTerraformOperationCmd(store common.TerraformStore, findProjectRoot common.ProjectFinderFunc, op common.TerraformOperation, use, short, long, example string) *cobra.Command {
	var tenantFlag, environmentFlag, confirmEnvironment string
	cmd := &cobra.Command{
		Use:          use,
		Short:        short,
		Long:         long,
		Example:      example,
		Args:         cobra.MaximumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTerraformCommand(withCloudContextPreflight(commandContext(cmd), store), store, findProjectRoot, op, args, tenantFlag, environmentFlag, confirmEnvironment)
		},
	}
	addDryRunFlag(cmd)
	cmd.Flags().StringVar(&tenantFlag, "tenant", "", "Target a specific tenant (defaults to the current scope)")
	cmd.Flags().StringVar(&environmentFlag, "environment", "", "Target a specific environment (defaults to the tenant's default)")
	if op != common.TerraformPlan {
		cmd.Flags().StringVar(&confirmEnvironment, "confirm-environment", "", "Confirm by restating the environment name, bypassing the interactive prompt (non-interactive use)")
	}
	return cmd
}

func runTerraformCommand(ctx common.Context, store common.TerraformStore, findProjectRoot common.ProjectFinderFunc, op common.TerraformOperation, args []string, tenantFlag, environmentFlag, confirmEnvironment string) error {
	if findProjectRoot == nil {
		findProjectRoot = common.FindProjectRoot
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}
	tenant := terraformArgOrFlag(tenantFlag, args, 0)
	environment := terraformArgOrFlag(environmentFlag, args, 1)

	result, err := common.RunTerraform(ctx, common.TerraformParams{
		Tenant:      tenant,
		Environment: environment,
		Operation:   op,
		ProjectRoot: projectRoot,
	}, store, terraformConfirmer(op, confirmEnvironment))
	if err != nil {
		return err
	}
	if !ctx.DryRun {
		_, _ = fmt.Fprintf(ctx.Stdout, "terraform %s complete in %s\n", result.Operation, result.Directory)
	}
	return nil
}

func terraformArgOrFlag(flag string, args []string, index int) string {
	if v := strings.TrimSpace(flag); v != "" {
		return v
	}
	if index < len(args) {
		return strings.TrimSpace(args[index])
	}
	return ""
}

// terraformConfirmer guards apply/destroy against hitting the wrong env; plan
// never mutates, so it needs no confirmation.
func terraformConfirmer(op common.TerraformOperation, confirmEnvironment string) common.TerraformConfirmFunc {
	if op == common.TerraformPlan {
		return nil
	}
	return func(ctx common.Context, environment string) error {
		provided := strings.TrimSpace(confirmEnvironment)
		if provided == "" {
			_, _ = fmt.Fprintf(ctx.Stderr, "Type the environment name to %s (%s): ", op, environment)
			line, _ := bufio.NewReader(ctx.Stdin).ReadString('\n')
			provided = strings.TrimSpace(line)
		}
		if provided != environment {
			return fmt.Errorf("confirmation %q does not match environment %q; aborting %s", provided, environment, op)
		}
		return nil
	}
}
