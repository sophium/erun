package envdelete

import (
	"errors"
	"fmt"
	"strings"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/cmd/shared/cloudcli"
	"github.com/sophium/erun/cmd/shared/prompt"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/envstore"
	"github.com/sophium/erun/erun-common/base/seam/kubeapi"
	"github.com/sophium/erun/erun-common/command/lifecycle/envdelete"
	"github.com/spf13/cobra"
)

func NewDeleteCmd(store envstore.DeleteStore, promptRunner prompt.PromptRunner, deleteNamespace kubeapi.NamespaceDeleterFunc) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete TENANT ENVIRONMENT",
		Short: "Delete a tenant environment and tear down its remote runtime",
		Long: "Delete a tenant environment and tear down its remote runtime.\n\n" +
			"Removes the environment's remote namespace and its data, then removes the local " +
			"config entry. The namespace data is not recoverable. Asks you to type " +
			"<tenant>-<environment> to confirm; -y skips the prompt for non-interactive callers.",
		Example:      "  erun delete team dev\n  erun delete team dev -y --output json",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeleteCommand(cloudcli.WithCloudContextPreflight(clicmd.CommandContext(cmd), store), store, promptRunner, deleteNamespace, args[0], args[1], yes)
		},
	}
	clicmd.AddDryRunFlag(cmd)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt (for non-interactive callers)")
	return cmd
}

func runDeleteCommand(ctx core.Context, store envstore.DeleteStore, promptRunner prompt.PromptRunner, deleteNamespace kubeapi.NamespaceDeleterFunc, tenant, environment string, yes bool) error {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	expected := envdelete.DeleteEnvironmentConfirmation(tenant, environment)
	if expected == "" {
		return fmt.Errorf("tenant and environment must not be empty: run `erun delete TENANT ENVIRONMENT` with both positional arguments set")
	}

	if !ctx.DryRun && !yes {
		if err := confirmDeleteCommand(promptRunner, expected); err != nil {
			return err
		}
	}

	result, err := envdelete.RunDeleteEnvironment(ctx, model.DeleteEnvironmentParams{
		Tenant:      tenant,
		Environment: environment,
	}, store, deleteNamespace)
	if err != nil {
		return err
	}

	if ctx.Output != model.OutputJSON {
		if result.NamespaceDeleteError != "" {
			_, _ = fmt.Fprintf(ctx.Stderr, "warning: failed to delete namespace %q in context %q: %s\n", result.Namespace, result.KubernetesContext, result.NamespaceDeleteError)
		}
		if !ctx.DryRun {
			_, _ = fmt.Fprintf(ctx.Stdout, "deleted environment: %s/%s\n", result.Tenant, result.Environment)
			if result.RemovedSSHHostAlias != "" {
				_, _ = fmt.Fprintf(ctx.Stdout, "removed ssh config block: Host %s (that alias no longer resolves)\n", result.RemovedSSHHostAlias)
			}
		}
	}
	return ctx.WriteResult(result)
}

func confirmDeleteCommand(promptRunner prompt.PromptRunner, expected string) error {
	if promptRunner == nil {
		promptRunner = prompt.RunPrompt
	}

	value, err := promptRunner(promptui.Prompt{
		Label: "Type " + expected + " to confirm deletion",
	})
	if err != nil {
		if errors.Is(err, promptui.ErrInterrupt) {
			return fmt.Errorf("delete interrupted")
		}
		return err
	}
	if strings.TrimSpace(value) != expected {
		return fmt.Errorf("delete confirmation did not match %q", expected)
	}
	return nil
}
