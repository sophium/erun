package open

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/briandowns/spinner"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/command/orchestrator"
	"github.com/sophium/erun/erun-common/primitive/open"
	"golang.org/x/term"
)

type OpenShellRunner func(core.Context, config.ShellLaunchParams) error

const desktopAppSessionEnvVar = orchestrator.DesktopSessionEnvVar

func NewOpenShellRunner(waitForShellDeployment func(config.ShellLaunchParams) error, execShell seam.ShellLauncherFunc) OpenShellRunner {
	if waitForShellDeployment == nil {
		waitForShellDeployment = open.WaitForShellDeployment
	}
	if execShell == nil {
		execShell = open.ExecShell
	}

	return func(ctx core.Context, req config.ShellLaunchParams) error {
		if err := runOpenShellWait(ctx, req, waitForShellDeployment); err != nil {
			return err
		}
		return execShell(req)
	}
}

func runOpenShellWait(ctx core.Context, req config.ShellLaunchParams, waitForShellDeployment func(config.ShellLaunchParams) error) error {
	if waitForShellDeployment == nil {
		return fmt.Errorf("wait for shell deployment is required")
	}
	return runWithSpinner(
		ctx,
		" waiting for "+config.RuntimeReleaseName(req.Tenant)+" in "+req.Namespace,
		"deployment available: "+config.RuntimeReleaseName(req.Tenant)+"\n",
		func() error {
			return waitForShellDeployment(req)
		},
	)
}

func isTerminalWriter(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

func runWithSpinner(ctx core.Context, suffix, finalMessage string, run func() error) error {
	if run == nil {
		return fmt.Errorf("run function is required")
	}
	if !shouldUseSpinner(ctx.Stderr) {
		return run()
	}

	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond, spinner.WithWriter(ctx.Stderr))
	s.Suffix = suffix
	s.Start()

	err := run()
	if err == nil {
		s.FinalMSG = finalMessage
	}
	s.Stop()
	return err
}

func shouldUseSpinner(w io.Writer) bool {
	if os.Getenv(desktopAppSessionEnvVar) != "" {
		return false
	}
	return isTerminalWriter(w)
}
