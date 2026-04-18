package cmd

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/briandowns/spinner"
	common "github.com/sophium/erun/erun-common"
	"golang.org/x/term"
)

type OpenShellRunner func(common.Context, common.ShellLaunchParams) error

func newOpenShellRunner(waitForShellDeployment func(common.ShellLaunchParams) error, execShell common.ShellLauncherFunc) OpenShellRunner {
	if waitForShellDeployment == nil {
		waitForShellDeployment = common.WaitForShellDeployment
	}
	if execShell == nil {
		execShell = common.ExecShell
	}

	return func(ctx common.Context, req common.ShellLaunchParams) error {
		if err := runOpenShellWait(ctx, req, waitForShellDeployment); err != nil {
			return err
		}
		return execShell(req)
	}
}

func runOpenShellWait(ctx common.Context, req common.ShellLaunchParams, waitForShellDeployment func(common.ShellLaunchParams) error) error {
	if waitForShellDeployment == nil {
		return fmt.Errorf("wait for shell deployment is required")
	}
	return runWithSpinner(
		ctx,
		" waiting for "+common.RuntimeReleaseName(req.Tenant)+" in "+req.Namespace,
		"deployment available: "+common.RuntimeReleaseName(req.Tenant)+"\n",
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

func runWithSpinner(ctx common.Context, suffix, finalMessage string, run func() error) error {
	if run == nil {
		return fmt.Errorf("run function is required")
	}
	if !isTerminalWriter(ctx.Stderr) {
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
