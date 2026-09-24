package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

type AppLauncher func(io.Writer, io.Writer, string, []string) error

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
			// The copy this launch settled on is resolved once and carried into
			// the launch, so the line the operator reads names the executable
			// that actually starts rather than a second, later lookup of it.
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
				launchApp = launchAppProcess
			}
			return launchApp(ctx.Stdout, ctx.Stderr, executable, appArgs)
		},
	}
	cmd.Flags().BoolVar(&headless, "headless", false, "Run the desktop backend without a Wails window and serve the frontend over HTTP")
	cmd.Flags().IntVar(&port, "port", 0, "HTTP listen port for --headless mode (defaults to the desktop binary's default)")
	addDryRunFlag(cmd)
	addCommands(cmd, newAppRestartCmd())
	return cmd
}

func newAppRestartCmd() *cobra.Command {
	var orchestratorID string
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the running desktop app in place to pick up a rebuild",
		Long: "Restart the already-running erun-app desktop process: it asks that process to run the exact " +
			"rebuild+restart its own Restart button runs — write the resume hand-off (naming the conversation " +
			"the orchestrator was actually live on, per --orchestrator), relaunch a fresh copy of itself, then " +
			"quit. It never spawns a second desktop instance or signals a process directly; it only ever asks " +
			"the one already running to restart itself, so it cannot leave two instances up or a process " +
			"half-killed. It resolves the running desktop from a marker that process wrote at startup and " +
			"verifies that process is still alive before doing anything — a stale marker (the desktop already " +
			"exited) or no marker at all (nothing running) is refused outright rather than guessed at, since a " +
			"relaunch armed against a dead target would silently do nothing. A --dry-run asks that same process " +
			"what a restart triggered now would reopen, naming every orchestrator that would not come back to " +
			"the conversation its last session was working in — a restart reopens an orchestrator on the " +
			"conversation attached to it or on the one derived from its id — so those conversations can be " +
			"attached before the restart instead of reported after it.",
		Example:       "  erun app restart\n  erun app restart --orchestrator my-orchestrator\n  erun app restart --dry-run",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAppRestartCommand(commandContext(cmd), orchestratorID)
		},
	}
	cmd.Flags().StringVar(&orchestratorID, "orchestrator", "", "Orchestrator id to resume after the restart (defaults to $ERUN_ORCHESTRATOR_ID; a bare restart with neither set reopens whatever was last open, idle)")
	addDryRunFlag(cmd)
	addOutputFlag(cmd)
	return cmd
}

// runAppRestartCommand resolves the orchestrator asking for the restart (the
// flag, else the environment an orchestrator session already carries), then
// defers entirely to eruncommon.RestartDesktopApp: the running desktop is the
// only thing that can correctly resolve which conversation to resume, so this
// command never re-implements that resolution.
func runAppRestartCommand(ctx eruncommon.Context, orchestratorID string) error {
	id := strings.TrimSpace(orchestratorID)
	if id == "" {
		id = strings.TrimSpace(os.Getenv(eruncommon.OrchestratorIDEnvVar))
	}
	ctx.Trace(fmt.Sprintf("app restart: orchestrator=%q", id))
	markerPath := eruncommon.DefaultDesktopControlMarkerPath()
	ctx.Trace(fmt.Sprintf("app restart: resolving the running desktop app from %s", markerPath))
	outcome := eruncommon.RestartDesktopApp(context.Background(), eruncommon.DefaultDesktopRestartDeps(), id, ctx.DryRun)
	ctx.Trace(fmt.Sprintf("app restart: status=%s pid=%d controlPort=%d", outcome.Status, outcome.PID, outcome.ControlPort))
	if outcome.Status == eruncommon.DesktopRestartRefused || outcome.Status == eruncommon.DesktopRestartFailed {
		_ = ctx.WriteResult(outcome)
		return fmt.Errorf("restart %s: %s", outcome.Status, outcome.Reason)
	}
	if ctx.Output != eruncommon.OutputJSON {
		if ctx.DryRun {
			appRestartDryRunReport(ctx, outcome)
		} else {
			_, _ = fmt.Fprintln(ctx.Stdout, appRestartSummary(outcome))
		}
	}
	return ctx.WriteResult(outcome)
}

// appRestartSummary renders the two non-error outcomes; RestartDesktopApp
// never returns would-restart for a real (non-dry-run) call, so this only
// ever prints for a successful restart.
func appRestartSummary(outcome eruncommon.DesktopRestartOutcome) string {
	return fmt.Sprintf("restarted the running desktop app (pid %d)", outcome.PID)
}

// appRestartDryRunReport renders what a restart would reopen. A dry run is the
// only moment any of it is still actionable: an orchestrator that would come
// back on a conversation other than the one its own session was working in can
// be attached BEFORE the restart, where the notice the next launch raises can
// only be acted on by attaching and restarting a second time.
//
// Its counterweight is the case where the plan could not be read at all. That is
// reported as itself and never rendered as an empty plan: "nothing would be
// stranded" is precisely what an answer nobody received must not be mistaken
// for, and the outcome is still would-restart either way, because the marker and
// the liveness probe already established that a real target is there.
func appRestartDryRunReport(ctx eruncommon.Context, outcome eruncommon.DesktopRestartOutcome) {
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
	stranded := make([]eruncommon.DesktopRestartReopen, 0, len(outcome.Preview))
	for _, reopen := range outcome.Preview {
		if reopen.Notice != "" {
			stranded = append(stranded, reopen)
		}
	}
	if len(stranded) == 0 {
		_, _ = fmt.Fprintf(ctx.Stdout, "it would reopen %s, each on the conversation it was working in\n",
			describeOrchestratorCount(len(outcome.Preview)))
		return
	}
	_, _ = fmt.Fprintf(ctx.Stdout, "it would reopen %s, and %d of them will NOT come back to the conversation their last session was working in:\n",
		describeOrchestratorCount(len(outcome.Preview)), len(stranded))
	for _, reopen := range stranded {
		_, _ = fmt.Fprintf(ctx.Stdout, "  %s\n", reopen.Notice)
	}
	_, _ = fmt.Fprintln(ctx.Stdout,
		"Attach each one in the desktop's Manage → Conversation before restarting, and the restart reopens it there.")
}

// describeOrchestratorCount renders a reopened-orchestrator count for an
// operator-facing line.
func describeOrchestratorCount(count int) string {
	if count == 1 {
		return "1 orchestrator"
	}
	return fmt.Sprintf("%d orchestrators", count)
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

func launchAppProcess(stdout, stderr io.Writer, executable string, args []string) error {
	cmd := newAppProcessCommand(runtime.GOOS, executable, args)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		// The build-or-install hint is for the default lookup finding nothing. When
		// the operator set an explicit override, surface the raw error so the bad
		// path stays visible — on Windows a missing extensionless path is itself
		// ErrNotFound, which the hint would otherwise mask.
		if errors.Is(err, exec.ErrNotFound) && strings.TrimSpace(os.Getenv("ERUN_ERUN_APP_BIN")) == "" {
			return fmt.Errorf("erun-app executable not found; build or install it first")
		}
		return err
	}
	return cmd.Process.Release()
}

// newAppProcessCommand delegates to the shared eruncommon.DesktopAppCommand so
// the CLI and the desktop app's self-restart build the launch identically.
func newAppProcessCommand(goos string, executable string, args []string) *exec.Cmd {
	return eruncommon.DesktopAppCommand(goos, executable, args)
}

// resolveAppExecutable resolves which desktop app to launch and the line
// reporting that choice, falling back to the bare program name — found on PATH
// — when this CLI sits beside no copy at all.
func resolveAppExecutable() (string, string) {
	hostOS := eruncommon.DetectHost().OS
	executableName := eruncommon.DesktopAppName
	if hostOS == eruncommon.HostOSWindows {
		executableName += ".exe"
	}

	executable, err := os.Executable()
	if err == nil {
		if resolved, chosen := resolveAppExecutableNear(executable, executableName, hostOS); resolved != "" {
			return resolved, chosen
		}
	}
	return executableName, ""
}

// desktopAppLayout is one on-disk layout a desktop app copy can occupy
// relative to this CLI.
type desktopAppLayout struct {
	// Launch is what a launch has to name: on macOS the .app bundle, which is
	// the shape eruncommon.DesktopAppCommand opens as a fresh instance, and
	// elsewhere the binary itself.
	Launch string
	// Bundle marks an .app directory. It is both the reason Binary sits inside
	// Launch and the shape Launch must have to count as a copy at all — a bare
	// file named ERun.app is not one.
	Bundle bool
	// Binary is the path inside Launch whose modification time moves when a
	// rebuild replaces the executable in place instead of recreating the
	// layout. It is empty when Launch is itself the executable.
	Binary string
}

// desktopAppLayouts lists, in preference order, where a desktop app copy can
// sit relative to the running CLI: beside the binary, which is where a
// packaged install and a hand-placed copy put it, and in the checkout's
// erun-ui/bin, which is where a local ./erun-ui/build.sh writes its rebuild.
func desktopAppLayouts(executableDir, executableName string, hostOS eruncommon.HostOS) []desktopAppLayout {
	checkoutBin := filepath.Clean(filepath.Join(executableDir, "..", "..", "erun-ui", "bin"))
	if hostOS == eruncommon.HostOSDarwin {
		bundleBinary := filepath.Join("Contents", "MacOS", executableName)
		return []desktopAppLayout{
			{Launch: filepath.Join(executableDir, desktopAppBundleName), Bundle: true, Binary: bundleBinary},
			{Launch: filepath.Join(checkoutBin, desktopAppBundleName), Bundle: true, Binary: bundleBinary},
		}
	}
	return []desktopAppLayout{
		{Launch: filepath.Join(executableDir, executableName)},
		{Launch: filepath.Join(checkoutBin, executableName)},
	}
}

// presentDesktopApp is a candidate layout that exists on disk, with the time
// it was last written.
type presentDesktopApp struct {
	launch   string
	modified time.Time
}

// resolveAppExecutableNear resolves which copy of the desktop app this CLI
// should launch, and the line reporting the choice.
//
// Two layouts can hold a copy at once, and taking the first that exists lets a
// stale copy beside the CLI shadow a fresh rebuild in erun-ui/bin for good:
// the launch still succeeds, so the desktop quietly keeps running the old
// build, and every restart relaunches that same copy, because a desktop
// restarts from the copy it is running from. The freshness of the copy has to
// decide, not its position in that list.
//
// The one signal both layouts carry on both platforms is when the copy was
// last written, and "newer" means exactly that: the copy whose own
// modification time is greatest wins. For a macOS bundle that time is the
// later of the .app directory and the binary inside it, because a rebuild may
// either recreate the bundle (./erun-ui/build.sh removes and rebuilds it) or
// replace the executable within it. Only a strictly newer copy displaces one
// listed ahead of it, so a tie keeps this list's order and the copy nearest
// the CLI stays the default when the filesystem cannot separate them. A tie is
// the only case freshness cannot answer: a layout that stat reports at all has
// already yielded its own stamp.
func resolveAppExecutableNear(executable, executableName string, hostOS eruncommon.HostOS) (string, string) {
	var present []presentDesktopApp
	for _, layout := range desktopAppLayouts(filepath.Dir(executable), executableName, hostOS) {
		info, err := os.Stat(layout.Launch)
		if err != nil || info.IsDir() != layout.Bundle {
			continue
		}
		present = append(present, presentDesktopApp{launch: layout.Launch, modified: layoutModified(layout, info)})
	}
	if len(present) == 0 {
		return "", ""
	}
	chosen := present[0]
	for _, candidate := range present[1:] {
		if candidate.modified.After(chosen.modified) {
			chosen = candidate
		}
	}
	return chosen.launch, appLaunchTrace(present, chosen.launch)
}

// layoutModified reports when a candidate copy was last written, taking the
// later of the layout itself and the executable inside it.
func layoutModified(layout desktopAppLayout, info os.FileInfo) time.Time {
	modified := info.ModTime()
	if layout.Binary == "" {
		return modified
	}
	if binary, err := os.Stat(filepath.Join(layout.Launch, layout.Binary)); err == nil && binary.ModTime().After(modified) {
		modified = binary.ModTime()
	}
	return modified
}

// appLaunchTrace names the copy a launch settled on, so an operator whose
// rebuild appears to have done nothing can see which copy actually started.
// With one copy there is nothing to choose between and the line is just the
// answer; with several, the copies passed over are named too.
func appLaunchTrace(present []presentDesktopApp, chosen string) string {
	if len(present) < 2 {
		return fmt.Sprintf("app: launching %s", chosen)
	}
	passedOver := make([]string, 0, len(present)-1)
	for _, candidate := range present {
		if candidate.launch != chosen {
			passedOver = append(passedOver, candidate.launch)
		}
	}
	return fmt.Sprintf("app: launching %s (newest of %d copies found; not launched: %s)",
		chosen, len(present), strings.Join(passedOver, ", "))
}
