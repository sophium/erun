package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/internal"
	"github.com/spf13/cobra"
)

func newOpenCmd(resolveOpen func(common.OpenParams) (common.OpenResult, error), saveTenantConfig func(common.TenantConfig) error, runInitForArgs func(common.Context, []string) error, promptRunner PromptRunner, openShell OpenShellRunner, runManagedDeploy func(common.Context, common.OpenResult) error, checkKubernetesDeployment common.KubernetesDeploymentCheckerFunc, resolveRuntimeDeploySpec func(common.OpenResult) (common.DeploySpec, error), deployHelmChart common.HelmChartDeployerFunc) *cobra.Command {
	var noShell bool
	var snapshot bool
	var noSnapshot bool

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
			snapshotOverride, err := resolveSnapshotFlagOverride(cmd, snapshot, noSnapshot)
			if err != nil {
				return err
			}
			result, err = applyOpenSnapshotPreference(result, snapshotOverride, saveTenantConfig)
			if err != nil {
				return err
			}
			return runResolvedOpenCommand(ctx, result, openOptions{NoShell: noShell}, promptRunner, openShell, runManagedDeploy, checkKubernetesDeployment, resolveRuntimeDeploySpec, deployHelmChart)
		},
	}

	addDryRunFlag(cmd)
	cmd.Flags().BoolVar(&noShell, "no-shell", false, "Print shell commands to switch kubectl context, namespace, and worktree locally")
	addSnapshotFlags(cmd, &snapshot, &noSnapshot, "Build and deploy a local snapshot when opening the local environment")
	return cmd
}

type openOptions struct {
	NoShell bool
}

func applyOpenSnapshotPreference(result common.OpenResult, enabled *bool, saveTenantConfig func(common.TenantConfig) error) (common.OpenResult, error) {
	if enabled == nil || !strings.EqualFold(strings.TrimSpace(result.Environment), common.DefaultEnvironment) {
		return result, nil
	}

	result.TenantConfig.SetSnapshot(*enabled)
	if saveTenantConfig == nil {
		return result, nil
	}
	if err := saveTenantConfig(result.TenantConfig); err != nil {
		return common.OpenResult{}, err
	}
	return result, nil
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

func runResolvedOpenCommand(ctx common.Context, result common.OpenResult, options openOptions, promptRunner PromptRunner, openShell OpenShellRunner, runManagedDeploy func(common.Context, common.OpenResult) error, checkKubernetesDeployment common.KubernetesDeploymentCheckerFunc, resolveRuntimeDeploySpec func(common.OpenResult) (common.DeploySpec, error), deployHelmChart common.HelmChartDeployerFunc) error {
	namespace := common.KubernetesNamespaceName(result.Tenant, result.Environment)
	if options.NoShell {
		ctx.TraceCommand("", "kubectl", "config", "use-context", strings.TrimSpace(result.EnvConfig.KubernetesContext))
		ctx.TraceCommand("", "kubectl", "config", "set-context", "--current", "--namespace="+namespace)
		ctx.TraceCommand("", "cd", result.RepoPath)
		return emitLocalShellSetupForOpenResult(result, promptRunner, ctx.Stdout, ctx.Stderr)
	}

	shellReq := common.ShellLaunchParamsFromResult(result)
	if resolveRuntimeDeploySpec != nil && deployHelmChart != nil {
		execution, err := resolveRuntimeDeploySpec(result)
		if err != nil {
			return err
		}
		execution, err = maybeCreateMissingRuntimeChart(ctx, result, promptRunner, resolveRuntimeDeploySpec, execution)
		if err != nil {
			return err
		}

		shouldDeploy := len(execution.Builds) > 0
		if !shouldDeploy && checkKubernetesDeployment != nil {
			deployed, err := checkKubernetesDeployment(common.KubernetesDeploymentCheckParams{
				Name:              common.RuntimeReleaseName(result.Tenant),
				Namespace:         namespace,
				KubernetesContext: result.EnvConfig.KubernetesContext,
				ExpectedRepoPath:  common.RemoteShellWorktreePath(shellReq),
			})
			if err != nil {
				return err
			}
			shouldDeploy = !deployed
		}

		if shouldDeploy {
			ctx.Logger.Debug("deploying the devops runtime before opening the shell")
			if err := common.RunDeploySpec(
				ctx,
				execution,
				common.DockerImageBuilder,
				func(ctx common.Context, pushInput common.DockerPushSpec) error {
					return common.RunDockerPush(ctx, pushInput, common.DockerImagePusher)
				},
				wrapOpenHelmDeployWithSpinner(ctx, execution.Deploy.ReleaseName, deployHelmChart),
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

	for {
		err := openShell(ctx, shellReq)
		if !errors.Is(err, common.ErrShellReattachDeploy) {
			return err
		}
		if runManagedDeploy == nil {
			return err
		}
		if err := runManagedDeploy(ctx, result); err != nil {
			return err
		}
	}
}

func wrapOpenHelmDeployWithSpinner(ctx common.Context, releaseName string, deployHelmChart common.HelmChartDeployerFunc) common.HelmChartDeployerFunc {
	if deployHelmChart == nil {
		return nil
	}
	return func(params common.HelmDeployParams) error {
		return runWithSpinner(
			ctx,
			" deploying "+releaseName+" with helm",
			"deployment updated: "+releaseName+"\n",
			func() error {
				return deployHelmChart(params)
			},
		)
	}
}

func maybeCreateMissingRuntimeChart(ctx common.Context, result common.OpenResult, promptRunner PromptRunner, resolveRuntimeDeploySpec func(common.OpenResult) (common.DeploySpec, error), execution common.DeploySpec) (common.DeploySpec, error) {
	if ctx.DryRun || promptRunner == nil || resolveRuntimeDeploySpec == nil {
		return execution, nil
	}
	if !common.IsDefaultDevopsChartPath(execution.Deploy.ChartPath) {
		return execution, nil
	}

	moduleName := common.RuntimeReleaseName(result.Tenant)
	ok, err := confirmPrompt(promptRunner, fmt.Sprintf("create %s chart in %s", moduleName, result.RepoPath))
	if err != nil {
		return common.DeploySpec{}, err
	}
	if !ok {
		return execution, nil
	}

	if err := common.EnsureDefaultDevopsChart(ctx, result.RepoPath, result.Tenant, result.Environment); err != nil {
		return common.DeploySpec{}, err
	}

	return resolveRuntimeDeploySpec(result)
}

func emitLocalShellSetupForOpenResult(result common.OpenResult, promptRunner PromptRunner, stdout, stderr io.Writer) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	if file, ok := stdout.(*os.File); ok {
		if info, err := file.Stat(); err == nil && (info.Mode()&os.ModeCharDevice) != 0 {
			if err := maybeConfigureOpenNoShellAlias(result, promptRunner, os.Getenv("SHELL"), stderr); err != nil {
				return err
			}
		}
	}

	_, err := io.WriteString(stdout, common.LocalShellSetupScript(result))
	return err
}

func maybeConfigureOpenNoShellAlias(result common.OpenResult, promptRunner PromptRunner, shellPath string, stderr io.Writer) error {
	aliasName := openNoShellAliasName(result)
	startupFile, aliasConfigured := detectOpenNoShellAliasStartupFile(result, shellPath)
	if aliasConfigured {
		for _, line := range openNoShellHintLines(result, shellPath) {
			_, _ = fmt.Fprintln(stderr, line)
		}
		return nil
	}
	if startupFile == "" || promptRunner == nil {
		for _, line := range openNoShellHintLines(result, shellPath) {
			_, _ = fmt.Fprintln(stderr, line)
		}
		return nil
	}

	ok, err := confirmPrompt(promptRunner, fmt.Sprintf("add %s to %s", aliasName, startupFile))
	if err != nil {
		return err
	}
	if !ok {
		for _, line := range openNoShellHintLines(result, shellPath) {
			_, _ = fmt.Fprintln(stderr, line)
		}
		return nil
	}

	if err := appendOpenNoShellAlias(startupFile, openNoShellAliasCommand(result, shellPath)); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stderr, "added %s to %s\n", aliasName, startupFile)
	_, _ = fmt.Fprintf(stderr, "open a new shell to use %s\n", aliasName)
	return nil
}

func openNoShellHintLines(result common.OpenResult, shellPath string) []string {
	aliasName := openNoShellAliasName(result)
	aliasCommand := openNoShellAliasCommand(result, shellPath)
	startupFile, aliasConfigured := detectOpenNoShellAliasStartupFile(result, shellPath)
	if aliasConfigured {
		return []string{
			fmt.Sprintf("configured in your shell startup file: open a new shell to use %s", aliasName),
		}
	}
	if startupFile == "" {
		return []string{
			"one-liner alias:",
			aliasCommand,
		}
	}
	return []string{
		"one-liner alias:",
		aliasCommand,
	}
}

func openNoShellAliasName(result common.OpenResult) string {
	if strings.TrimSpace(result.Title) != "" {
		return strings.TrimSpace(result.Title)
	}
	return strings.TrimSpace(result.Tenant) + "-" + strings.TrimSpace(result.Environment)
}

func openNoShellAliasCommand(result common.OpenResult, shellPath string) string {
	aliasName := openNoShellAliasName(result)
	command := fmt.Sprintf("erun open %s %s --no-shell", result.Tenant, result.Environment)
	if filepath.Base(strings.TrimSpace(shellPath)) == "fish" {
		return "alias " + aliasName + " 'eval (" + command + ")'"
	}
	return "alias " + aliasName + `='eval "$(` + command + `)"'`
}

func detectOpenNoShellAliasStartupFile(result common.OpenResult, shellPath string) (string, bool) {
	homeDir, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(homeDir) == "" {
		return "", false
	}

	preferred, candidates := openNoShellStartupFiles(homeDir, shellPath)
	for _, candidate := range candidates {
		configured, err := startupFileHasAlias(candidate, openNoShellAliasName(result))
		if err != nil {
			continue
		}
		if configured {
			return candidate, true
		}
	}
	return preferred, false
}

func openNoShellStartupFiles(homeDir, shellPath string) (string, []string) {
	switch filepath.Base(strings.TrimSpace(shellPath)) {
	case "bash":
		preferred := filepath.Join(homeDir, ".bashrc")
		return preferred, []string{
			preferred,
			filepath.Join(homeDir, ".bash_profile"),
			filepath.Join(homeDir, ".profile"),
		}
	case "fish":
		preferred := filepath.Join(homeDir, ".config", "fish", "config.fish")
		return preferred, []string{preferred}
	default:
		preferred := filepath.Join(homeDir, ".zshrc")
		return preferred, []string{preferred}
	}
}

func startupFileHasAlias(path, aliasName string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "alias "+aliasName+"=") || strings.HasPrefix(trimmed, "alias "+aliasName+" ") {
			return true, nil
		}
	}
	return false, nil
}

func appendOpenNoShellAlias(path, aliasCommand string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.Contains(string(data), aliasCommand) {
		return nil
	}

	content := string(data)
	if strings.TrimSpace(content) != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += aliasCommand + "\n"

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
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
