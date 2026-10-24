package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/cmd/shared/desktop"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
	"github.com/spf13/cobra"
)

type AppLauncher func(io.Writer, io.Writer, string, []string) error

func NewAppCmd(launchApp AppLauncher) *cobra.Command {
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
			ctx := clicmd.CommandContext(cmd)
			// Resolve once so the reported path is the executable that actually starts.
			executable, chosen := resolveAppExecutable()
			if chosen != "" {
				ctx.Trace(chosen)
			}
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
				launchApp = LaunchAppProcess
			}
			return launchApp(ctx.Stdout, ctx.Stderr, executable, appArgs)
		},
	}
	cmd.Flags().BoolVar(&headless, "headless", false, "Run the desktop backend without a Wails window and serve the frontend over HTTP")
	cmd.Flags().IntVar(&port, "port", 0, "HTTP listen port for --headless mode (defaults to the desktop binary's default)")
	clicmd.AddDryRunFlag(cmd)
	clicmd.AddCommands(cmd, newAppRestartCmd())
	return cmd
}

func newAppRestartCmd() *cobra.Command {
	var orchestratorID string
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the running desktop app in place to pick up a rebuild",
		Long: "Restart the already-running erun-app desktop process: it asks that process to run the exact " +
			"rebuild+restart its own Restart button runs — record which orchestrator owns the terminal pane " +
			"afterwards (per --orchestrator), relaunch a fresh copy of itself, then quit. Orchestrators run as " +
			"Claude Code background agents, so the restart does not interrupt them: the new desktop reattaches " +
			"each one to its agent, mid-turn included. It never spawns a second desktop instance or signals a process directly; it only ever asks " +
			"the one already running to restart itself, so it cannot leave two instances up or a process " +
			"half-killed. It resolves the running desktop from a marker that process wrote at startup and " +
			"verifies that process is still alive before doing anything — a stale marker (the desktop already " +
			"exited) or no marker at all (nothing running) is refused outright rather than guessed at, since a " +
			"relaunch armed against a dead target would silently do nothing. A --dry-run asks that same process " +
			"what a restart triggered now would reopen, naming every orchestrator the new desktop would refuse " +
			"to reopen and why, so it can be fixed before the restart instead of reported after it.",
		Example:       "  erun app restart\n  erun app restart --orchestrator my-orchestrator\n  erun app restart --dry-run",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAppRestartCommand(clicmd.CommandContext(cmd), orchestratorID)
		},
	}
	cmd.Flags().StringVar(&orchestratorID, "orchestrator", "", "Orchestrator id that owns the terminal pane after the restart (defaults to $ERUN_ORCHESTRATOR_ID; a bare restart with neither set gives it to the most recently opened one)")
	clicmd.AddDryRunFlag(cmd)
	clicmd.AddOutputFlag(cmd)
	return cmd
}

// runAppRestartCommand defers to core.RestartDesktopApp: only the running desktop holds the open orchestrators.
func runAppRestartCommand(ctx core.Context, orchestratorID string) error {
	id := strings.TrimSpace(orchestratorID)
	if id == "" {
		id = strings.TrimSpace(os.Getenv(jobstate.OrchestratorIDEnvVar))
	}
	ctx.Trace(fmt.Sprintf("app restart: orchestrator=%q", id))
	markerPath := core.DefaultDesktopControlMarkerPath()
	ctx.Trace(fmt.Sprintf("app restart: resolving the running desktop app from %s", markerPath))
	outcome := core.RestartDesktopApp(context.Background(), core.DefaultDesktopRestartDeps(), id, ctx.DryRun)
	ctx.Trace(fmt.Sprintf("app restart: status=%s pid=%d controlPort=%d", outcome.Status, outcome.PID, outcome.ControlPort))
	if outcome.Status == model.DesktopRestartRefused || outcome.Status == model.DesktopRestartFailed {
		_ = ctx.WriteResult(outcome)
		return fmt.Errorf("restart %s: %s", outcome.Status, outcome.Reason)
	}
	if ctx.Output != model.OutputJSON {
		if ctx.DryRun {
			appRestartDryRunReport(ctx, outcome)
		} else {
			_, _ = fmt.Fprintln(ctx.Stdout, appRestartSummary(outcome))
		}
	}
	return ctx.WriteResult(outcome)
}

func appRestartSummary(outcome model.DesktopRestartOutcome) string {
	return fmt.Sprintf("restarted the running desktop app (pid %d)", outcome.PID)
}

// appRestartDryRunReport reports orchestrators the next launch would refuse to reopen.
// An unreadable plan is reported as such, never rendered as an empty one.
func appRestartDryRunReport(ctx core.Context, outcome model.DesktopRestartOutcome) {
	if ctx.Stdout == nil {
		return
	}
	_, _ = fmt.Fprintf(ctx.Stdout, "would restart the running desktop app (pid %d)\n", outcome.PID)
	if outcome.PreviewUnavailable != "" {
		_, _ = fmt.Fprintln(ctx.Stdout, outcome.PreviewUnavailable)
		return
	}
	if len(outcome.Preview) == 0 {
		_, _ = fmt.Fprintln(ctx.Stdout, "it has no orchestrator to reopen")
		return
	}
	refused := make([]model.DesktopRestartReopen, 0, len(outcome.Preview))
	for _, reopen := range outcome.Preview {
		if reopen.Notice != "" {
			refused = append(refused, reopen)
		}
	}
	if len(refused) == 0 {
		_, _ = fmt.Fprintf(ctx.Stdout, "it would reopen %s, each reattached to its running background agent\n",
			describeOrchestratorCount(len(outcome.Preview)))
		return
	}
	_, _ = fmt.Fprintf(ctx.Stdout, "it would reopen %s, and %d of them would NOT be reopened:\n",
		describeOrchestratorCount(len(outcome.Preview)), len(refused))
	for _, reopen := range refused {
		_, _ = fmt.Fprintf(ctx.Stdout, "  %s\n", reopen.Notice)
	}
}

func describeOrchestratorCount(count int) string {
	if count == 1 {
		return "1 orchestrator"
	}
	return fmt.Sprintf("%d orchestrators", count)
}

// buildAppLaunchArgs forwards only the headless flags to erun-app.
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

func LaunchAppProcess(stdout, stderr io.Writer, executable string, args []string) error {
	cmd := newAppProcessCommand(runtime.GOOS, executable, args)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		// An explicit override surfaces the raw error; on Windows a missing path is also ErrNotFound.
		if errors.Is(err, exec.ErrNotFound) && strings.TrimSpace(os.Getenv("ERUN_ERUN_APP_BIN")) == "" {
			return fmt.Errorf("erun-app executable not found; build or install it first")
		}
		return err
	}
	return cmd.Process.Release()
}

// newAppProcessCommand shares core.DesktopAppCommand with the desktop's self-restart.
func newAppProcessCommand(goos string, executable string, args []string) *exec.Cmd {
	return core.DesktopAppCommand(goos, executable, args)
}

// resolveAppExecutable returns the desktop app to launch and the line reporting it.
func resolveAppExecutable() (string, string) {
	hostOS := core.DetectHost().OS
	executableName := core.DesktopAppName
	if hostOS == model.HostOSWindows {
		executableName += ".exe"
	}

	executable, err := os.Executable()
	if err == nil {
		if resolved, chosen := desktop.ResolveAppExecutableNear(executable, executableName, hostOS); resolved != "" {
			return resolved, chosen
		}
	}
	return executableName, ""
}
