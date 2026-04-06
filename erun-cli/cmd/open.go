package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sophium/erun/internal"
	"github.com/sophium/erun/internal/bootstrap"
	"github.com/sophium/erun/internal/opener"
	"github.com/spf13/cobra"
)

func NewOpenCmd(deps Dependencies, verbosity *int) *cobra.Command {
	var noShell bool

	cmd := &cobra.Command{
		Use:          "open [TENANT] [ENVIRONMENT]",
		Short:        "Open a shell in the tenant environment worktree",
		Args:         cobra.MaximumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := openRequestForArgs(args)
			if err != nil {
				return err
			}

			err = runOpenCommand(cmd, deps, req, openOptions{NoShell: noShell})
			if shouldRunInitForOpenCommand(err) {
				initReq, initErr := initRequestForRootCommand(deps, args)
				if initErr != nil {
					return initErr
				}
				return runInitCommand(cmd, deps, verbosity, initReq)
			}
			return err
		},
	}

	addDryRunFlag(cmd)
	cmd.Flags().BoolVar(&noShell, "no-shell", false, "Print shell commands to switch kubectl context, namespace, and worktree locally")
	return cmd
}

type openOptions struct {
	NoShell bool
}

func openRequestForArgs(args []string) (opener.Request, error) {
	switch len(args) {
	case 0:
		return opener.Request{
			UseDefaultTenant:      true,
			UseDefaultEnvironment: true,
		}, nil
	case 1:
		return opener.Request{
			Environment:      args[0],
			UseDefaultTenant: true,
		}, nil
	case 2:
		return opener.Request{
			Tenant:      args[0],
			Environment: args[1],
		}, nil
	default:
		return opener.Request{}, fmt.Errorf("accepts 0 to 2 arg(s), received %d", len(args))
	}
}

func runOpenCommand(cmd *cobra.Command, deps Dependencies, req opener.Request, options openOptions) error {
	result, err := resolveOpenCommand(deps, req)
	if err != nil {
		return err
	}
	return launchOpenResult(cmd, deps, result, options)
}

func resolveOpenCommand(deps Dependencies, req opener.Request) (opener.Result, error) {
	return newOpenService(deps).Resolve(req)
}

func launchOpenResult(cmd *cobra.Command, deps Dependencies, result opener.Result, options ...openOptions) error {
	option := openOptions{}
	if len(options) > 0 {
		option = options[0]
	}

	emitOpenResultTrace(cmd, result, option)
	if option.NoShell {
		if isDryRunCommand(cmd) {
			return nil
		}
		return emitLocalShellSetupForOpenResult(result, cmd.OutOrStdout(), cmd.ErrOrStderr())
	}
	if err := ensureOpenRuntimeAvailable(cmd, deps, result); err != nil {
		return err
	}
	if isDryRunCommand(cmd) {
		return nil
	}

	launcher := deps.LaunchShell
	if launcher == nil {
		launcher = opener.DefaultShellLauncher
	}
	return launcher(opener.ShellLaunchRequestFromResult(result))
}

func emitOpenResultTrace(cmd *cobra.Command, result opener.Result, options openOptions) {
	if !commandTraceEnabled(cmd) {
		return
	}

	namespace := bootstrap.KubernetesNamespaceName(result.Tenant, result.Environment)
	notes := []string{
		"decision: resolved tenant=" + result.Tenant,
		"decision: resolved environment=" + result.Environment,
		"decision: resolved namespace=" + namespace,
		"decision: resolved kubernetes context=" + strings.TrimSpace(result.EnvConfig.KubernetesContext),
	}
	if options.NoShell {
		notes = append(notes, "decision: using local shell setup mode because --no-shell was requested")
	} else {
		notes = append(notes, "decision: using remote shell mode through deployment/"+devopsComponentName)
	}
	emitTraceNotes(cmd, cmd.ErrOrStderr(), notes...)

	if options.NoShell {
		emitCommandTrace(cmd, cmd.ErrOrStderr(), CommandTrace{
			Name: "kubectl",
			Args: []string{"config", "use-context", strings.TrimSpace(result.EnvConfig.KubernetesContext)},
		})
		emitCommandTrace(cmd, cmd.ErrOrStderr(), CommandTrace{
			Name: "kubectl",
			Args: []string{"config", "set-context", "--current", "--namespace=" + namespace},
		})
		emitCommandTrace(cmd, cmd.ErrOrStderr(), CommandTrace{
			Name: "cd",
			Args: []string{result.RepoPath},
		})
		return
	}

	preview, err := opener.PreviewShellLaunch(opener.ShellLaunchRequestFromResult(result))
	if err != nil {
		emitTraceNotes(cmd, cmd.ErrOrStderr(), "decision: unable to render the full remote shell preview: "+err.Error())
		return
	}
	emitCommandTrace(cmd, cmd.ErrOrStderr(), CommandTrace{
		Name: "kubectl",
		Args: preview.WaitArgs,
	})
	execArgs := append([]string{}, preview.ExecArgs...)
	if len(execArgs) > 0 {
		execArgs[len(execArgs)-1] = "<bootstrap-script>"
	}
	emitCommandTrace(cmd, cmd.ErrOrStderr(), CommandTrace{
		Name: "kubectl",
		Args: execArgs,
	})
	emitTraceBlock(cmd, cmd.ErrOrStderr(), "bootstrap-script", preview.Script)
	emitTraceNotes(cmd, cmd.ErrOrStderr(), "decision: the remote shell bootstrap preview redacts host credential file contents while preserving the command shape")
}

func emitLocalShellSetupForOpenResult(result opener.Result, stdout, stderr io.Writer) error {
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

	_, err := io.WriteString(stdout, localShellSetupScript(result))
	return err
}

func ensureOpenRuntimeAvailable(cmd *cobra.Command, deps Dependencies, result opener.Result) error {
	if deps.CheckKubernetesDeployment == nil || deps.DeployHelmChart == nil {
		return nil
	}

	namespace := bootstrap.KubernetesNamespaceName(result.Tenant, result.Environment)
	deployed, err := deps.CheckKubernetesDeployment(KubernetesDeploymentCheckRequest{
		Name:              devopsComponentName,
		Namespace:         namespace,
		KubernetesContext: result.EnvConfig.KubernetesContext,
	})
	if err != nil {
		return err
	}
	if deployed {
		emitTraceNotes(cmd, cmd.ErrOrStderr(), "decision: the devops runtime is already deployed in the target namespace")
		return nil
	}
	emitTraceNotes(cmd, cmd.ErrOrStderr(), "decision: the devops runtime is missing and will be deployed before opening the shell")
	if isDryRunCommand(cmd) {
		buildPlans, deployPlan, err := resolveDeployExecutionForTarget(deps, result, devopsComponentName, "")
		if err != nil {
			return err
		}
		emitResolvedDeployExecutionTrace(cmd, buildPlans, deployPlan)
		return nil
	}

	return deployComponentForTarget(deps, result, devopsComponentName, cmd.OutOrStdout(), cmd.ErrOrStderr())
}

func newOpenService(deps Dependencies) opener.Service {
	deps = withDependencyDefaults(deps)
	return opener.Service{
		Store:       deps.Store,
		LaunchShell: deps.LaunchShell,
	}
}

func openerIsDefaultError(err error) bool {
	return errors.Is(err, opener.ErrDefaultTenantNotConfigured) ||
		errors.Is(err, opener.ErrDefaultEnvironmentNotConfigured) ||
		errors.Is(err, internal.ErrNotInitialized)
}

func shouldInitOpenCommand(err error) bool {
	return errors.Is(err, opener.ErrKubernetesContextNotConfigured)
}

func shouldRunInitForOpenCommand(err error) bool {
	return shouldInitRootCommand(err) ||
		errors.Is(err, opener.ErrTenantNotFound) ||
		errors.Is(err, opener.ErrEnvironmentNotFound)
}

func localShellSetupScript(result opener.Result) string {
	commands := []string{
		fmt.Sprintf("kubectl config use-context %s >/dev/null", shellSnippetQuote(strings.TrimSpace(result.EnvConfig.KubernetesContext))),
		fmt.Sprintf("kubectl config set-context --current --namespace=%s >/dev/null", shellSnippetQuote(bootstrap.KubernetesNamespaceName(result.Tenant, result.Environment))),
		fmt.Sprintf("cd %s", shellSnippetQuote(result.RepoPath)),
	}
	return strings.Join(commands, " &&\n") + "\n"
}

func shellSnippetQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
