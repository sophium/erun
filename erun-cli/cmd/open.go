package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/internal"
	"github.com/spf13/cobra"
)

func newOpenCmd(resolveOpen func(common.OpenParams) (common.OpenResult, error), runInitForArgs func(common.Context, []string) error, launchShell common.ShellLauncherFunc, checkKubernetesDeployment common.KubernetesDeploymentCheckerFunc, resolveRuntimeDeploySpec func(common.OpenResult) (common.DeploySpec, error), deployHelmChart common.HelmChartDeployerFunc) *cobra.Command {
	var noShell bool

	cmd := &cobra.Command{
		Use:          "open [TENANT] [ENVIRONMENT]",
		Short:        "Open a shell in the tenant environment worktree",
		Args:         cobra.MaximumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			result, initRan, err := resolveOpenWithInitStop(ctx, args, shouldRunInitForOpenCommand, resolveOpen, runInitForArgs)
			if err != nil {
				return err
			}
			if initRan {
				return nil
			}
			return runResolvedOpenCommand(ctx, result, openOptions{NoShell: noShell}, launchShell, checkKubernetesDeployment, resolveRuntimeDeploySpec, deployHelmChart)
		},
	}

	addDryRunFlag(cmd)
	cmd.Flags().BoolVar(&noShell, "no-shell", false, "Print shell commands to switch kubectl context, namespace, and worktree locally")
	return cmd
}

type openOptions struct {
	NoShell bool
}

func resolveOpenArgs(args []string, resolveOpen func(common.OpenParams) (common.OpenResult, error)) (common.OpenParams, common.OpenResult, error) {
	params, err := common.OpenParamsForArgs(args)
	if err != nil {
		return common.OpenParams{}, common.OpenResult{}, err
	}

	result, err := resolveOpen(params)
	return params, result, err
}

func runInitBeforeOpen(ctx common.Context, args []string, runInitForArgs func(common.Context, []string) error) error {
	ctx.Logger.Debug("running init before resolving open target")
	return runInitForArgs(ctx, args)
}

func resolveOpenWithInitStop(ctx common.Context, args []string, shouldRunInit func(error) bool, resolveOpen func(common.OpenParams) (common.OpenResult, error), runInitForArgs func(common.Context, []string) error) (common.OpenResult, bool, error) {
	_, result, err := resolveOpenArgs(args, resolveOpen)
	if !shouldRunInit(err) {
		return result, false, err
	}

	if initErr := runInitBeforeOpen(ctx, args, runInitForArgs); initErr != nil {
		return common.OpenResult{}, true, initErr
	}

	return common.OpenResult{}, true, nil
}

func resolveOpenWithInitRetry(ctx common.Context, args []string, shouldRunInit func(error) bool, resolveOpen func(common.OpenParams) (common.OpenResult, error), runInitForArgs func(common.Context, []string) error) (common.OpenResult, bool, error) {
	params, result, err := resolveOpenArgs(args, resolveOpen)
	if !shouldRunInit(err) {
		return result, false, err
	}

	if initErr := runInitBeforeOpen(ctx, args, runInitForArgs); initErr != nil {
		return common.OpenResult{}, true, initErr
	}

	result, err = resolveOpen(params)
	return result, true, err
}

func runResolvedOpenCommand(ctx common.Context, result common.OpenResult, options openOptions, launchShell common.ShellLauncherFunc, checkKubernetesDeployment common.KubernetesDeploymentCheckerFunc, resolveRuntimeDeploySpec func(common.OpenResult) (common.DeploySpec, error), deployHelmChart common.HelmChartDeployerFunc) error {
	namespace := common.KubernetesNamespaceName(result.Tenant, result.Environment)
	if options.NoShell {
		ctx.TraceCommand("", "kubectl", "config", "use-context", strings.TrimSpace(result.EnvConfig.KubernetesContext))
		ctx.TraceCommand("", "kubectl", "config", "set-context", "--current", "--namespace="+namespace)
		ctx.TraceCommand("", "cd", result.RepoPath)
		return emitLocalShellSetupForOpenResult(result, ctx.Stdout, ctx.Stderr)
	}

	shellReq := common.ShellLaunchParamsFromResult(result)
	if checkKubernetesDeployment != nil && deployHelmChart != nil {
		deployed, err := checkKubernetesDeployment(common.KubernetesDeploymentCheckParams{
			Name:              common.RuntimeReleaseName(result.Tenant),
			Namespace:         namespace,
			KubernetesContext: result.EnvConfig.KubernetesContext,
			ExpectedRepoPath:  common.RemoteShellWorktreePath(shellReq),
		})
		if err != nil {
			return err
		}
		if !deployed {
			ctx.Logger.Debug("deploying the devops runtime before opening the shell")
			execution, err := resolveRuntimeDeploySpec(result)
			if err != nil {
				return err
			}
			if err := common.RunDeploySpec(
				ctx,
				execution,
				common.DockerImageBuilder,
				func(ctx common.Context, pushInput common.DockerPushSpec) error {
					return common.RunDockerPush(ctx, pushInput, common.DockerImagePusher)
				},
				deployHelmChart,
			); err != nil {
				return err
			}
		}
	}

	if preview, err := common.PreviewShellLaunch(shellReq); err == nil {
		ctx.TraceCommand("", "kubectl", preview.WaitArgs...)
		execArgs := append([]string{}, preview.ExecArgs...)
		if len(execArgs) > 0 {
			execArgs[len(execArgs)-1] = "<bootstrap-script>"
		}
		ctx.TraceCommand("", "kubectl", execArgs...)
		ctx.TraceBlock("bootstrap-script", preview.Script)
	} else {
		ctx.Logger.Debug("unable to render remote shell bootstrap trace: " + err.Error())
	}

	if ctx.DryRun {
		return nil
	}

	return launchShell(shellReq)
}

func emitLocalShellSetupForOpenResult(result common.OpenResult, stdout, stderr io.Writer) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	if file, ok := stdout.(*os.File); ok {
		if info, err := file.Stat(); err == nil && (info.Mode()&os.ModeCharDevice) != 0 {
			_, _ = fmt.Fprintln(stderr, `run this to update the current shell: eval "$(erun open --no-shell)"`)
		}
	}

	_, err := io.WriteString(stdout, common.LocalShellSetupScript(result))
	return err
}

func openerIsDefaultError(err error) bool {
	return errors.Is(err, common.ErrDefaultTenantNotConfigured) ||
		errors.Is(err, common.ErrDefaultEnvironmentNotConfigured) ||
		errors.Is(err, common.ErrNotInitialized)
}

func shouldInitOpenCommand(err error) bool {
	return errors.Is(err, common.ErrKubernetesContextNotConfigured)
}

func shouldRunInitForOpenCommand(err error) bool {
	return shouldInitRootCommand(err) ||
		errors.Is(err, common.ErrTenantNotFound) ||
		errors.Is(err, common.ErrEnvironmentNotFound)
}

func shouldInitRootCommand(err error) bool {
	return openerIsDefaultError(err) ||
		shouldInitOpenCommand(err) ||
		errors.Is(err, common.ErrNotInGitRepository) ||
		internal.IsReported(err)
}
