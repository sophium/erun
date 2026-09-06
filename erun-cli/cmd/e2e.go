package cmd

import (
	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newE2ECmd(resolveOpen OpenResolver, findProjectRoot common.ProjectFinderFunc) *cobra.Command {
	var tenant, environment, component string
	cmd := &cobra.Command{
		Use:   "e2e",
		Short: "Run the project's Playwright suite against a deployed environment",
		Long: "Discover a project's playwright/ folder the way `erun build` discovers\n" +
			"docker/ and `erun deploy` discovers k8s/, then run it once against a real,\n" +
			"already-deployed environment. The suite receives the environment's resolved\n" +
			"HTTPS base URL and its currently deployed version as the ERUN_E2E_BASE_URL\n" +
			"and ERUN_E2E_VERSION environment variables -- both derived from the live\n" +
			"environment, never declared by the suite.\n\n" +
			"Refuses before a browser starts, naming the cause, when the environment is\n" +
			"not deployed, the target service is not exposed, or its certificate is not\n" +
			"yet issued. Also refuses a suite that sets ignoreHTTPSErrors or hardcodes its\n" +
			"own baseURL, since both would silently defeat the guarantee this command\n" +
			"exists to make. A project with no playwright/ folder is a clean no-op.\n\n" +
			"erun e2e is a separate step with its own exit code: `erun deploy` never runs\n" +
			"it, and `erun build --e2e` composes build, push, deploy, and this command for\n" +
			"the everyday branch flow.",
		Example: "  erun e2e --tenant team --environment dev\n" +
			"  erun e2e --tenant team --environment dev --component erun-console",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runE2ECommand(commandContext(cmd), resolveOpen, scopedOpenParams(tenant, environment), findProjectRoot, component)
		},
	}
	addDryRunFlag(cmd)
	cmd.Flags().StringVar(&tenant, "tenant", "", "Target a specific tenant (default: the current scope)")
	cmd.Flags().StringVar(&environment, "environment", "", "Target a specific environment; requires --tenant")
	cmd.Flags().StringVar(&component, "component", "", "Run the named component's suite when playwright/ has more than one")
	return cmd
}

func runE2ECommand(ctx common.Context, resolveOpen OpenResolver, params common.OpenParams, findProjectRoot common.ProjectFinderFunc, component string) error {
	suite, err := common.ResolveCurrentPlaywrightSuite(findProjectRoot, component)
	if err != nil {
		return err
	}
	if suite == nil {
		ctx.Info("e2e: no playwright/ suite found; nothing to run")
		return nil
	}

	result, err := resolveOpen(params)
	if err != nil {
		return err
	}
	req := common.ShellLaunchParamsFromResult(result)

	target := common.E2ETarget{
		Component:         component,
		Tenant:            req.Tenant,
		Environment:       req.Environment,
		Namespace:         req.Namespace,
		KubernetesContext: req.KubernetesContext,
	}

	e2eResult, err := common.RunE2E(ctx, *suite, target, nil)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return nil
	}
	if ctx.Output == common.OutputJSON {
		return ctx.WriteResult(e2eResult)
	}
	return nil
}
