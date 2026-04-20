package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
)

type AppLauncher func(io.Writer, io.Writer, []string) error

func newAppCmd(launchApp AppLauncher) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "app",
		Short:         "Launch the ERun desktop app",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			ctx.TraceCommand("", resolveAppExecutable())
			if ctx.DryRun {
				return nil
			}
			if launchApp == nil {
				launchApp = launchAppProcess
			}
			return launchApp(ctx.Stdout, ctx.Stderr, nil)
		},
	}
	addDryRunFlag(cmd)
	return cmd
}

func launchAppProcess(stdout, stderr io.Writer, args []string) error {
	cmd := newAppProcessCommand(runtime.GOOS, resolveAppExecutable(), args)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("erun-app executable not found; build or install it first")
		}
		return err
	}
	return cmd.Process.Release()
}

func newAppProcessCommand(goos string, executable string, args []string) *exec.Cmd {
	cmd := exec.Command(executable, args...)
	if goos == "darwin" {
		cmd.Args[0] = "ERun"
	}
	return cmd
}

func resolveAppExecutable() string {
	executableName := "erun-app"
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	}

	executable, err := os.Executable()
	if err == nil {
		sibling := filepath.Join(filepath.Dir(executable), executableName)
		if info, statErr := os.Stat(sibling); statErr == nil && !info.IsDir() {
			return sibling
		}
		devBinary := filepath.Clean(filepath.Join(filepath.Dir(executable), "..", "..", "erun-ui", "bin", executableName))
		if info, statErr := os.Stat(devBinary); statErr == nil && !info.IsDir() {
			return devBinary
		}
	}
	return executableName
}
