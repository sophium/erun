package e2e

import (
	"strings"

	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/cmd/shared/envscope"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/e2e"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/shared/projectseam"
	"github.com/spf13/cobra"
)

func NewE2ECmd(resolveOpen envscope.OpenResolver, findProjectRoot projectseam.ProjectFinderFunc) *cobra.Command {
	var tenant, environment, component, baseURL, version string
	cmd := &cobra.Command{
		Use:   "e2e",
		Short: "Run the project's e2e suites against a deployed environment",
		Long: "Discover the project's e2e suites -- <tenant>-devops/e2e/<component>/Dockerfile,\n" +
			"each declaring an `e2e` stage -- the way `erun build` discovers docker/ and\n" +
			"`erun deploy` discovers k8s/, and run each one as a docker build of that stage\n" +
			"against a real, already-deployed environment. The e2e stage re-runs every time\n" +
			"while the stages before it stay cached, and the build keeps no image.\n\n" +
			"Each suite receives build args derived from the live environment, never\n" +
			"declared by the suite: ERUN_E2E_VERSION (the deployed version),\n" +
			"ERUN_E2E_BASE_URL (the HTTPS URL of the exposed service named like the suite's\n" +
			"directory), and ERUN_E2E_URL_<SERVICE> for every exposed service. The\n" +
			".erun/config.yaml e2e.secrets list is passed as --secret.\n\n" +
			"--base-url points the suites at an address you name instead and skips the\n" +
			"exposure and certificate checks. --version sets the version the suites\n" +
			"expect instead of the deployed one. Because a --base-url may serve a\n" +
			"different deployment than the current scope, it needs either --version or\n" +
			"an explicit --tenant/--environment naming the environment it serves (whose\n" +
			"deployed version is then resolved); with both --base-url and --version the\n" +
			"environment is not read at all.\n\n" +
			"With no --component every suite runs, in order, and the exit code is non-zero\n" +
			"if any failed. Refuses before any suite runs, naming the cause, when the\n" +
			"environment is not deployed, a suite's service is not exposed or its\n" +
			"certificate is not yet issued, or its URL resolves to a loopback address a\n" +
			"docker build cannot reach. Also refuses a suite whose playwright.config sets\n" +
			"ignoreHTTPSErrors or hardcodes baseURL. A project with no e2e/ suite is a\n" +
			"clean no-op.\n\n" +
			"erun e2e is a separate step with its own exit code: `erun build`, `erun deploy`\n" +
			"and releases never run it, and it is not a release or merge gate. A failure\n" +
			"names the suite, target URL, deployed version, and log so it can be filed as\n" +
			"a bug; erun e2e files nothing itself.",
		Example: "  erun e2e --tenant team --environment dev\n" +
			"  erun e2e --tenant team --environment dev --component erun-console\n" +
			"  erun e2e --tenant team --environment dev --component erun-console --base-url https://console.example.com\n" +
			"  erun e2e --component erun-console --base-url https://console.example.com --version 1.4.2",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runE2ECommand(clicmd.CommandContext(cmd), resolveOpen, envscope.ScopedOpenParams(cmd.CommandPath(), tenant, environment), findProjectRoot, component, baseURL, version)
		},
	}
	clicmd.AddDryRunFlag(cmd)
	cmd.Flags().StringVar(&tenant, "tenant", "", "Target a specific tenant (default: the current scope)")
	cmd.Flags().StringVar(&environment, "environment", "", "Target a specific environment; requires --tenant")
	cmd.Flags().StringVar(&component, "component", "", "Run only the named suite (a directory under e2e/); default runs every suite")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "Run the suites against this URL instead of the exposed service's, skipping the exposure and certificate checks; needs --version or an explicit --tenant/--environment")
	cmd.Flags().StringVar(&version, "version", "", "Version the suites expect (ERUN_E2E_VERSION) instead of the environment's deployed version")
	return cmd
}

func runE2ECommand(ctx core.Context, resolveOpen envscope.OpenResolver, params model.OpenParams, findProjectRoot projectseam.ProjectFinderFunc, component, baseURL, version string) error {
	plan, err := e2e.ResolveE2EPlan(findProjectRoot, component)
	if err != nil {
		return err
	}
	if len(plan.Suites) == 0 {
		ctx.Info("e2e: no e2e/<component>/Dockerfile suite found; nothing to run")
		return nil
	}

	targetNamed := !params.UseDefaultTenant || !params.UseDefaultEnvironment
	target, err := resolveE2ETarget(resolveOpen, params, targetNamed, baseURL, version)
	if err != nil {
		return err
	}

	e2eResult, err := e2e.RunE2E(ctx, model.E2EParams{
		Plan:        plan,
		Target:      target,
		BaseURL:     baseURL,
		Version:     version,
		TargetNamed: targetNamed,
	})
	if e2eResult == nil || ctx.DryRun || ctx.Output != model.OutputJSON {
		return err
	}
	if writeErr := ctx.WriteResult(e2eResult); writeErr != nil {
		return writeErr
	}
	return err
}

// resolveE2ETarget resolves no environment when --base-url and --version name the target.
func resolveE2ETarget(resolveOpen envscope.OpenResolver, params model.OpenParams, targetNamed bool, baseURL, version string) (model.E2ETarget, error) {
	if !targetNamed && strings.TrimSpace(baseURL) != "" && strings.TrimSpace(version) != "" {
		return model.E2ETarget{}, nil
	}
	result, err := resolveOpen(params)
	if err != nil {
		return model.E2ETarget{}, err
	}
	req := cloud.ShellLaunchParamsFromResult(result)
	return model.E2ETarget{
		Tenant:            req.Tenant,
		Environment:       req.Environment,
		Namespace:         req.Namespace,
		KubernetesContext: req.KubernetesContext,
	}, nil
}
