package platform

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/cmd/shared/prompt"
	"github.com/sophium/erun/cmd/shared/textfmt"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/platform"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/spf13/cobra"
)

func newPlatformEnvCmd(store config.CloudReadStore, alias *string, promptRunner prompt.PromptRunner, deps cloud.CloudDependencies) *cobra.Command {
	return clicmd.NewCommandGroup(
		"env",
		"Manage hosted environments on the erun platform",
		newPlatformEnvListCmd(store, alias, deps),
		newPlatformEnvGetCmd(store, alias, deps),
		newPlatformEnvRegisterCmd(store, alias, deps),
		newPlatformEnvPushCmd(store, alias, deps),
		newPlatformEnvPullCmd(store, alias, promptRunner, deps),
		newPlatformEnvDeployCmd(store, alias, deps),
		newPlatformEnvStopCmd(store, alias, deps),
		newPlatformEnvDeleteCmd(store, alias, promptRunner, deps),
	)
}

func newPlatformEnvListCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List the caller's tenant's hosted environments",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := clicmd.CommandContext(cmd)
			environments, err := platform.RunPlatformListEnvironments(ctx, store, *alias, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform environment list planned.")
				return err
			}
			if ctx.Output != model.OutputJSON {
				if err := writePlatformEnvironmentList(ctx, environments); err != nil {
					return err
				}
			}
			return ctx.WriteResult(environments)
		},
	}
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func writePlatformEnvironmentList(ctx core.Context, environments []model.PlatformEnvironment) error {
	if len(environments) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "no environments")
		return err
	}
	for _, environment := range environments {
		if err := writePlatformEnvironmentLine(ctx, environment); err != nil {
			return err
		}
	}
	return nil
}

func writePlatformEnvironmentLine(ctx core.Context, environment model.PlatformEnvironment) error {
	line := fmt.Sprintf("  - %s (%s) type=%s status=%s", environment.Name, environment.EnvironmentID, environment.Type, environment.Status)
	if strings.TrimSpace(environment.RuntimeVersion) != "" {
		line += " runtime-version=" + environment.RuntimeVersion
	}
	if strings.TrimSpace(environment.ProvisionError) != "" {
		line += " provision-error=" + textfmt.QuotedValueOrNone(environment.ProvisionError)
	}
	if strings.TrimSpace(environment.DeleteError) != "" {
		line += " delete-error=" + textfmt.QuotedValueOrNone(environment.DeleteError)
	}
	_, err := fmt.Fprintln(ctx.Stdout, line)
	return err
}

func newPlatformEnvGetCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "get ENVIRONMENT_ID",
		Short:        "Fetch one hosted environment by id",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := clicmd.CommandContext(cmd)
			environment, err := platform.RunPlatformGetEnvironment(ctx, store, *alias, args[0], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform environment lookup planned.")
				return err
			}
			if ctx.Output != model.OutputJSON {
				if err := writePlatformEnvironmentLine(ctx, environment); err != nil {
					return err
				}
			}
			return ctx.WriteResult(environment)
		},
	}
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func newPlatformEnvRegisterCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	var params model.PlatformCreateEnvironmentParams
	var localDefinition string
	cmd := &cobra.Command{
		Use:   "register",
		Short: "Register a hosted environment on the erun platform",
		Long: "Register a hosted environment on the erun platform.\n\n" +
			"Writes a new environment row, immediately, and — for a runtime environment with " +
			"--runtime-version set and a deploy executor configured on the platform — also starts " +
			"a server-side deploy: the response's status moves registered -> provisioning -> " +
			"running/failed, so poll `erun platform env get` to watch it land.\n\n" +
			"--adopt records a row for an environment that already exists on this machine, instead of " +
			"asking the platform to provision one. Paired with --definition TENANT/ENVIRONMENT it also " +
			"uploads that local environment's portable settings as definition revision 1 and records " +
			"the hosted marker in the local config, so later `erun platform env push` / `pull` know " +
			"which row this machine's copy corresponds to. Only the portable subset travels; the repo " +
			"path, ports, sshd keys and cluster-local Secret names never leave the machine.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Example:      "  erun platform env register --name prod --type runtime --context-id 018f... --runtime-version 1.4.2\n  erun platform env register --name dev --type local-agent --adopt --kubernetes-context kind-dev --definition acme/dev",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := clicmd.CommandContext(cmd)
			var local *model.EnvDefinitionAdoptParams
			if strings.TrimSpace(localDefinition) != "" {
				tenant, environment, err := parseLocalEnvironmentRef(localDefinition)
				if err != nil {
					return err
				}
				local = &model.EnvDefinitionAdoptParams{Tenant: tenant, Environment: environment}
			}
			environment, definition, err := platform.RunPlatformRegisterEnvironmentWithDefinition(ctx, store, *alias, params, local, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform environment registration planned.")
				return err
			}
			if ctx.Output != model.OutputJSON {
				if err := writePlatformEnvironmentLine(ctx, environment); err != nil {
					return err
				}
				if local != nil {
					if _, err := fmt.Fprintf(ctx.Stdout, "  uploaded %s/%s as definition revision %d\n", local.Tenant, local.Environment, definition.Revision); err != nil {
						return err
					}
				}
			}
			return ctx.WriteResult(environment)
		},
	}
	cmd.Flags().StringVar(&params.Name, "name", "", "Environment name (DNS-1123 label; forms the <tenant>-<env> namespace)")
	cmd.Flags().StringVar(&params.Type, "type", "", "Environment type: runtime, remote-agent, or local-agent")
	cmd.Flags().StringVar(&params.ContextID, "context-id", "", "Cloud context to deploy into (see `erun platform context list`)")
	cmd.Flags().StringVar(&params.KubernetesContext, "kubernetes-context", "", "Kubernetes context name to deploy into, if not using --context-id")
	cmd.Flags().StringVar(&params.RuntimeVersion, "runtime-version", "", "Published erun runtime version to deploy (runtime environments only)")
	cmd.Flags().BoolVar(&params.Adopt, "adopt", false, "Register a row for an environment that already exists on this machine, instead of asking the platform to provision one")
	cmd.Flags().StringVar(&params.TenantID, "tenant-id", "", "Target tenant id (operations-tenant callers only; defaults to the caller's own tenant)")
	cmd.Flags().StringVar(&localDefinition, "definition", "", "Upload this local environment's portable settings with the registration, as TENANT/ENVIRONMENT")
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

// parseLocalEnvironmentRef splits TENANT/ENVIRONMENT, refusing anything else.
func parseLocalEnvironmentRef(ref string) (string, string, error) {
	trimmed := strings.TrimSpace(ref)
	tenant, environment, found := strings.Cut(trimmed, "/")
	if !found || strings.TrimSpace(tenant) == "" || strings.TrimSpace(environment) == "" || strings.Contains(environment, "/") {
		return "", "", fmt.Errorf("--definition takes TENANT/ENVIRONMENT, got %q", ref)
	}
	return strings.TrimSpace(tenant), strings.TrimSpace(environment), nil
}

// newPlatformEnvPushCmd uploads local settings as the next revision of the env's platform row.
func newPlatformEnvPushCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push TENANT ENVIRONMENT",
		Short: "Upload a registered local environment's settings to the platform",
		Long: "Upload a registered local environment's settings to the platform.\n\n" +
			"Only the portable subset travels: the settings that describe the environment itself. " +
			"Anything that describes this machine or names a cluster-local object — the repo path, " +
			"the local port range, sshd keys, image pull secrets, cloud aliases — is never uploaded, " +
			"and neither is the root config.yaml or the secret store. Each upload advances the row's " +
			"definition revision.\n\n" +
			"The environment must already be marked as hosted: `erun platform env register --adopt " +
			"--definition TENANT/ENVIRONMENT` records that marker. A marker that names a different " +
			"tenant, platform or environment than this call resolves is refused rather than " +
			"re-pointed.",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		Example:      "  erun platform env push acme dev\n  erun platform env push acme dev --dry-run",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := clicmd.CommandContext(cmd)
			result, err := platform.RunPlatformPushEnvDefinition(ctx, store, *alias, model.EnvDefinitionPushParams{
				Tenant:      args[0],
				Environment: args[1],
			}, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform environment definition push planned.")
				return err
			}
			if ctx.Output != model.OutputJSON {
				if _, err := fmt.Fprintf(ctx.Stdout, "  uploaded %s/%s to %s (definition revision %d)\n",
					args[0], args[1], result.Environment.EnvironmentID, result.Revision); err != nil {
					return err
				}
			}
			return ctx.WriteResult(result)
		},
	}
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

// newPlatformEnvPullCmd pulls the platform's definition into a local env, creating it if absent.
func newPlatformEnvPullCmd(store config.CloudReadStore, alias *string, promptRunner prompt.PromptRunner, deps cloud.CloudDependencies) *cobra.Command {
	var params platform.EnvDefinitionPullParams
	var yes bool
	cmd := &cobra.Command{
		Use:   "pull TENANT ENVIRONMENT",
		Short: "Pull a hosted environment's settings down onto this machine",
		Long: "Pull a hosted environment's settings down onto this machine.\n\n" +
			"Only the portable subset is written. Every field the platform has no opinion about — " +
			"the repo path, the local port range, sshd, cluster-local Secret names, cloud aliases — " +
			"is left exactly as this machine has it, including keys this erun cannot model.\n\n" +
			"A platform-owned field that disagrees (the name, type or Kubernetes context) refuses " +
			"rather than merging: the local config path is derived from the name, so a rename would " +
			"write a second environment instead of updating this one. When the local copy and the " +
			"platform have both changed since the last sync, the diff is shown and you are asked; " +
			"-y accepts it without asking.\n\n" +
			"Pulling into an environment that does not exist here yet needs --environment-id, since " +
			"nothing local can say which platform row you mean.",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		Example:      "  erun platform env pull acme dev\n  erun platform env pull acme dev --environment-id 018f... --repo-path ~/src/acme",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := clicmd.CommandContext(cmd)
			params.Tenant = args[0]
			params.Environment = args[1]
			confirm := pullConfirmation(ctx, promptRunner, args[0], args[1], yes)
			result, err := platform.RunPlatformPullEnvDefinition(ctx, store, *alias, params, confirm, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform environment definition pull planned.")
				return err
			}
			if ctx.Output != model.OutputJSON {
				verb := "updated"
				if result.Created {
					verb = "created"
				}
				if _, err := fmt.Fprintf(ctx.Stdout, "  %s %s/%s from %s (definition revision %d)\n",
					verb, args[0], args[1], result.Environment.EnvironmentID, result.Revision); err != nil {
					return err
				}
			}
			return ctx.WriteResult(result)
		},
	}
	cmd.Flags().StringVar(&params.EnvironmentID, "environment-id", "", "Platform environment id to pull from (required when the local environment does not exist yet)")
	cmd.Flags().StringVar(&params.LocalRepoPath, "repo-path", "", "Host repo path to record for a new environment of a type that needs one")
	cmd.Flags().IntVar(&params.LocalPortRangeStart, "port-range-start", 0, "Local port range start for a new environment (default: the lowest free range on this machine)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Accept a two-sided edit without prompting (for non-interactive callers)")
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func newPlatformEnvDeployCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	var params model.PlatformDeployEnvironmentParams
	cmd := &cobra.Command{
		Use:   "deploy ENVIRONMENT_ID",
		Short: "Start a server-side deploy of an already-registered environment",
		Long: "Start a server-side deploy of an already-registered environment.\n\n" +
			"Starts the platform's own deploy executor, which helm-installs a published erun " +
			"runtime version into the environment's namespace. Fails with a conflict if a deploy " +
			"is already in progress for this environment. --version re-deploys at an explicit " +
			"version; omitted, the environment's own pinned runtime version is deployed.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Example:      "  erun platform env deploy 018f...\n  erun platform env deploy 018f... --version 1.4.2",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := clicmd.CommandContext(cmd)
			environment, err := platform.RunPlatformDeployEnvironment(ctx, store, *alias, args[0], params, deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform environment deploy planned.")
				return err
			}
			if ctx.Output != model.OutputJSON {
				if err := writePlatformEnvironmentLine(ctx, environment); err != nil {
					return err
				}
			}
			return ctx.WriteResult(environment)
		},
	}
	cmd.Flags().StringVar(&params.Version, "version", "", "Published version to deploy (defaults to the environment's pinned runtime version)")
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func newPlatformEnvStopCmd(store config.CloudReadStore, alias *string, deps cloud.CloudDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop ENVIRONMENT_ID",
		Short: "Scale a hosted environment's runtime to zero",
		Long: "Scale a hosted environment's runtime to zero.\n\n" +
			"The server-side equivalent of `erun stop`: the platform scales the environment's " +
			"runtime Deployment to zero. Persistent state is untouched; a later deploy or open " +
			"wakes it again.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := clicmd.CommandContext(cmd)
			environment, err := platform.RunPlatformStopEnvironment(ctx, store, *alias, args[0], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform environment stop planned.")
				return err
			}
			if ctx.Output != model.OutputJSON {
				if err := writePlatformEnvironmentLine(ctx, environment); err != nil {
					return err
				}
			}
			return ctx.WriteResult(environment)
		},
	}
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

// pullConfirmation asks about a two-sided edit. It returns nil under --yes or with no
// prompt, which makes erun-common refuse the edit rather than resolve it silently.
func pullConfirmation(ctx core.Context, promptRunner prompt.PromptRunner, tenant, environment string, yes bool) func(config.EnvDefinitionPlan) (bool, error) {
	if yes || promptRunner == nil {
		return nil
	}
	return func(plan config.EnvDefinitionPlan) (bool, error) {
		if _, err := fmt.Fprintln(ctx.Stdout, "  This environment and the platform have both changed since the last sync:"); err != nil {
			return false, err
		}
		for _, conflict := range plan.Conflicts {
			if _, err := fmt.Fprintf(ctx.Stdout, "    %s: %s -> %s\n", conflict.Label, conflict.OrUnsetLocal(), conflict.OrUnsetPlatform()); err != nil {
				return false, err
			}
		}
		return prompt.ConfirmPrompt(promptRunner, fmt.Sprintf("Overwrite the local %s/%s settings above with the platform's", tenant, environment))
	}
}

func newPlatformEnvDeleteCmd(store config.CloudReadStore, alias *string, promptRunner prompt.PromptRunner, deps cloud.CloudDependencies) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete ENVIRONMENT_ID",
		Short: "Start deleting a hosted environment and tearing down its remote namespace",
		Long: "Start deleting a hosted environment and tearing down its remote namespace.\n\n" +
			"The server-side equivalent of `erun delete`: the platform starts tearing down the " +
			"environment's namespace and its data, then removes the row. Not recoverable. The " +
			"teardown itself runs in the background — a namespace stuck on an unsatisfiable " +
			"finalizer can take a while, so this returns as soon as the delete is accepted, with " +
			"status \"deleting\". Run `erun platform env get` to watch it converge to gone (not " +
			"found) or \"deletion-blocked\" (naming why); a delete command against a blocked or " +
			"still-deleting environment retries it. Asks for confirmation; -y skips the prompt for " +
			"non-interactive callers.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		Example:      "  erun platform env delete 018f...\n  erun platform env delete 018f... -y",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := clicmd.CommandContext(cmd)
			if !ctx.DryRun && !yes {
				confirmed, err := prompt.ConfirmPrompt(promptRunner, fmt.Sprintf("Delete environment %s (irreversible)", args[0]))
				if err != nil {
					return err
				}
				if !confirmed {
					return fmt.Errorf("delete cancelled")
				}
			}
			environment, err := platform.RunPlatformDeleteEnvironment(ctx, store, *alias, args[0], deps)
			if err != nil {
				return err
			}
			if ctx.DryRun {
				_, err := fmt.Fprintln(ctx.Stdout, "Dry run: erun platform environment deletion planned.")
				return err
			}
			if ctx.Output != model.OutputJSON {
				if err := writePlatformEnvironmentLine(ctx, environment); err != nil {
					return err
				}
			}
			return ctx.WriteResult(environment)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt (for non-interactive callers)")
	clicmd.AddDryRunFlag(cmd)
	return cmd
}
