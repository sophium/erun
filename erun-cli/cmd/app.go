package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

type AppLauncher func(io.Writer, io.Writer, []string) error

func newAppCmd(launchApp AppLauncher) *cobra.Command {
	var (
		headless bool
		port     int
	)
	cmd := &cobra.Command{
		Use:           "app",
		Short:         "Launch the ERun desktop app",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			executable := resolveAppExecutable()
			appArgs := buildAppLaunchArgs(headless, port)
			traceArgs := executable
			if len(appArgs) > 0 {
				traceArgs = executable + " " + strings.Join(appArgs, " ")
			}
			ctx.TraceCommand("", traceArgs)
			if ctx.DryRun {
				return nil
			}
			if launchApp == nil {
				launchApp = launchAppProcess
			}
			return launchApp(ctx.Stdout, ctx.Stderr, appArgs)
		},
	}
	cmd.Flags().BoolVar(&headless, "headless", false, "Run the desktop backend without a Wails window and serve the frontend over HTTP")
	cmd.Flags().IntVar(&port, "port", 0, "HTTP listen port for --headless mode (defaults to the desktop binary's default)")
	addDryRunFlag(cmd)
	return cmd
}

// buildAppLaunchArgs returns the argv tail passed to erun-app. Only the
// headless flags are forwarded today; everything else stays on the CLI side.
func buildAppLaunchArgs(headless bool, port int) []string {
	if !headless {
		return nil
	}
	args := []string{"--headless"}
	if port > 0 {
		args = append(args, "--port", strconv.Itoa(port))
	}
	return args
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
	if goos == "darwin" && filepath.Ext(executable) == ".app" {
		openArgs := []string{"-n", executable}
		if len(args) > 0 {
			openArgs = append(openArgs, "--args")
			openArgs = append(openArgs, args...)
		}
		return eruncommon.Command("open", openArgs...)
	}

	cmd := eruncommon.Command(executable, args...)
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
		if resolved := resolveAppExecutableNear(executable, executableName); resolved != "" {
			return resolved
		}
	}
	return executableName
}

func resolveAppExecutableNear(executable, executableName string) string {
	executableDir := filepath.Dir(executable)
	if runtime.GOOS == "darwin" {
		if bundle := firstExistingDir(
			filepath.Join(executableDir, "ERun.app"),
			filepath.Clean(filepath.Join(executableDir, "..", "..", "erun-ui", "bin", "ERun.app")),
		); bundle != "" {
			return bundle
		}
	}
	return firstExistingFile(
		filepath.Join(executableDir, executableName),
		filepath.Clean(filepath.Join(executableDir, "..", "..", "erun-ui", "bin", executableName)),
	)
}

func firstExistingDir(paths ...string) string {
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path
		}
	}
	return ""
}

func firstExistingFile(paths ...string) string {
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}
