// Package erun owns the integration suite's compiled erun binary and runs it
// as a subprocess. It builds the binary once per `go test` run with coverage
// instrumentation so the production paths the suite exercises merge into one
// coverage profile.
package erun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// CoverDirEnv names where instrumented binaries write coverage counters; the
// harness sets it on every invocation so all subtests merge into one profile.
const CoverDirEnv = "GOCOVERDIR"

// CoverPkgs selects which packages count toward the integration coverage gate.
// It covers erun-cli and erun-common only: erun-mcp/erun-backend/erun-ui carry
// runtime code that --dry-run scenarios cannot exercise without substantial
// harness work, so including them would drag the gate down for code unrelated
// to what this suite verifies.
const CoverPkgs = "github.com/sophium/erun," +
	"github.com/sophium/erun/cmd," +
	"github.com/sophium/erun/internal/...," +
	"github.com/sophium/erun/erun-common"

// waitDelay bounds how long Run waits on a finished child's output pipes after
// the child itself has exited (exec.Cmd.WaitDelay). The child's exit is the
// event a scenario is about; the pipes closing is only how os/exec usually
// learns of it, and a process the child spawned can hold them open long after
// the child is gone. Keeping this far below the harness's own per-run timeout
// is the point: a run that reaches it has already concluded, and a scenario
// that leaves a descendant behind must fail as a timeout rather than wait out
// the test binary's own deadline and panic the package.
const waitDelay = 10 * time.Second

var (
	buildOnce   sync.Once
	binaryPath  string
	buildErr    error
	coverDir    string
	procCounter int64
)

// BinaryPath returns the path to the coverage-instrumented erun binary,
// compiling it once on first call.
func BinaryPath(t testing.TB) string {
	t.Helper()
	buildOnce.Do(func() {
		binaryPath, buildErr = buildBinary()
	})
	if buildErr != nil {
		t.Fatalf("build erun: %v", buildErr)
	}
	return binaryPath
}

// CoverDir returns the directory where the instrumented binary writes coverage
// counter files.
func CoverDir(t testing.TB) string {
	t.Helper()
	BinaryPath(t)
	return coverDir
}

// PrivateCoverDir allocates a fresh subdirectory of the suite's shared
// coverage root, exclusively owned by whichever process is about to be
// spawned, and returns its path. Go's coverage runtime emits its meta-data
// file at process init (before main even runs), naming a temp file with only
// a nanosecond timestamp for uniqueness -- no PID. Every process running the
// same instrumented binary computes the same final meta-data filename, so two
// such processes racing to create-and-rename their own temp file into that
// name in one shared directory can have the loser's rename fail outright
// (the source temp file is gone by the time it runs, already renamed away by
// the winner), silently dropping that process's coverage from the merged
// profile without failing anything on its own. Giving every process its own
// directory makes that race structurally impossible instead of merely rare.
//
// Run calls this for every subprocess it starts. A caller that spawns the
// instrumented binary (or something that itself spawns it, like erun-mcp)
// without going through Run must call this directly and set GOCOVERDIR in
// that process's own environment.
func PrivateCoverDir(t testing.TB) string {
	t.Helper()
	CoverDir(t) // ensures the shared coverage root is built and initialized
	dir := filepath.Join(coverDir, fmt.Sprintf("p%d", atomic.AddInt64(&procCounter, 1)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create private coverage dir: %v", err)
	}
	return dir
}

func buildBinary() (string, error) {
	repoRoot, err := repoRoot()
	if err != nil {
		return "", err
	}
	binDir, err := os.MkdirTemp("", "erun-integration-bin-")
	if err != nil {
		return "", err
	}
	exe := filepath.Join(binDir, "erun")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}

	args := []string{
		"build",
		"-cover",
		"-coverpkg=" + CoverPkgs,
		"-o", exe,
		".",
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = filepath.Join(repoRoot, "erun-cli")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go build -cover failed: %v\n%s", err, buf.String())
	}

	coverDir = os.Getenv(CoverDirEnv)
	if coverDir == "" {
		coverDir, err = os.MkdirTemp("", "erun-integration-cover-")
		if err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(coverDir, 0o755); err != nil {
		return "", err
	}
	return exe, nil
}

func repoRoot() (string, error) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime.Caller failed")
	}
	dir := filepath.Dir(here)
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repo root not found from %s", here)
		}
		if _, err := os.Stat(filepath.Join(parent, "erun-cli")); err == nil {
			return parent, nil
		}
		dir = parent
	}
}

// Result captures everything an integration test wants to assert about a run.
type Result struct {
	Stdout   string
	Stderr   string
	Combined string
	ExitCode int
}

// RunOptions configures a single subprocess invocation.
type RunOptions struct {
	Cwd string
	// Env is the full environment for the subprocess (it replaces the parent
	// environment except for GOCOVERDIR which the harness always injects).
	// Build it from env.Setup.Env(), which carries the scrubbed PATH; a
	// scenario that needs an external binary appends its own stub routing.
	Env     []string
	Stdin   string
	Timeout time.Duration
	// StdinFromDevNull binds the subprocess's stdin directly to the OS null
	// device instead of the default in-memory pipe. A piped Stdin buffer is
	// never a character device, so it cannot exercise a stat-based TTY check
	// the same way the null device does — the null device is a character
	// device but not a terminal, which is exactly the input a stat-based
	// check confuses for one. Mutually exclusive with Stdin.
	StdinFromDevNull bool
}

// requireIsolatedEnv guards the harness boundary: a scenario that runs the
// binary without an isolated HOME/XDG_CONFIG_HOME would silently read — or
// worse, write — the developer's real erun config, and its golden would
// capture machine state.
func requireIsolatedEnv(t testing.TB, env []string) {
	t.Helper()
	home := ""
	xdgConfig := ""
	for _, kv := range env {
		if value, ok := strings.CutPrefix(kv, "HOME="); ok {
			home = strings.TrimSpace(value)
		}
		if value, ok := strings.CutPrefix(kv, "XDG_CONFIG_HOME="); ok {
			xdgConfig = strings.TrimSpace(value)
		}
	}
	if home == "" || xdgConfig == "" {
		t.Fatalf("erun.Run: RunOptions.Env must carry an isolated HOME and XDG_CONFIG_HOME (use env.New(t) and setup.Env()); got HOME=%q XDG_CONFIG_HOME=%q", home, xdgConfig)
	}
	if real := strings.TrimSpace(os.Getenv("HOME")); real != "" && home == real {
		t.Fatalf("erun.Run: RunOptions.Env points HOME at the developer's real home %q; scenarios must run against the env.New(t) tempdir", real)
	}
}

// Run invokes the compiled binary with the given args and returns its output.
func Run(t testing.TB, args []string, opts RunOptions) Result {
	t.Helper()
	requireIsolatedEnv(t, opts.Env)
	bin := BinaryPath(t)

	timeout := opts.Timeout
	if timeout == 0 {
		// This cap is a hang-net, not a latency SLA: a --dry-run command is
		// normally sub-second, but a real command can wall-clock for tens of
		// seconds on transient host name-service latency (a cold macOS resolver
		// stalls a name lookup ~15-30s) without the command being wrong. Keep
		// the cap well above that environmental variance so it fails only a
		// genuine deadlock, not a slow-but-correct run. If it ever fires, the
		// goroutine dump below names the blocked call.
		//
		// Firing is not proof of a deadlock, though, and this harness has to
		// stay usable when it is wrong: under contention -- this suite runs
		// beside a browser suite against one CPU budget -- a command can be
		// simply slow past this cap, and the run that follows a timeout must
		// still conclude within it. That is what waitDelay guarantees: the
		// timeout path's own wait is bounded, so the worst case for one
		// scenario is this cap plus waitDelay, never the test binary's whole
		// deadline.
		timeout = 120 * time.Second
	}

	// The context exists so that WaitDelay can be armed at all. WaitDelay is
	// documented to bound a wait "after the command has exited or (if the
	// command does not exit) after the context is cancelled", and the second
	// clause is the only one this harness can rely on: os/exec starts the
	// goroutine that enforces WaitDelay solely when the Cmd has a non-nil
	// context whose Done channel is non-nil, so a Cmd built without one accepts
	// a WaitDelay value and never acts on it. A child that does not exit -- a
	// command wedged past the timeout under the gate's CPU contention -- is
	// never reaped, so Process.Wait never returns, the post-exit path that also
	// consults WaitDelay is never reached, and Wait blocks forever however
	// large the delay is. Cancelling this context is what turns a timeout into
	// a bounded run.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	env := make([]string, 0, len(opts.Env)+2)
	env = append(env, opts.Env...)
	env = append(env, CoverDirEnv+"="+PrivateCoverDir(t))
	// So a SIGQUIT on timeout dumps every goroutine's stack, not just the
	// current one — turning an opaque hang into a report of where it stuck.
	env = append(env, "GOTRACEBACK=all")
	cmd.Env = env
	// Always feed stdin from a buffer (empty when the scenario passes no
	// Stdin) so the subprocess never inherits the developer's terminal. This
	// keeps stdin a non-terminal pipe locally, so stdin-TTY-gated branches
	// (e.g. the interactive-gh-auth gate) behave the same everywhere.
	// Scenarios that need the interactive branch opt in explicitly via the
	// ERUN_FORCE_TTY seam. A scenario that needs stdin bound to the real null
	// device (StdinFromDevNull) gets that instead — see the field doc.
	if opts.StdinFromDevNull {
		devNull, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatalf("open %s: %v", os.DevNull, err)
		}
		defer func() { _ = devNull.Close() }()
		cmd.Stdin = devNull
	} else {
		cmd.Stdin = bytes.NewBufferString(opts.Stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start erun: %v", err)
	}

	err := supervise(cmd, cancel, timeout, waitDelay)
	if errors.Is(err, errTimeout) {
		t.Fatalf("erun timed out after %s; args=%v\n--- subprocess goroutine dump (stderr) ---\n%s", timeout, args, stderr.String())
	}
	// supervise reports a run it could observe as nil or the child's own exit
	// error, having ruled out a timeout above; anything else is a command that
	// could not be run at all. The status lives on ProcessState either way --
	// a run whose wait was rescued by the delay reads its exit code there
	// rather than from an error that no longer describes the child.
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		t.Fatalf("erun exec error: %v", err)
	}
	result := Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Combined: stdout.String() + stderr.String(),
		ExitCode: cmd.ProcessState.ExitCode(),
	}
	reportUndeclaredBinaries(t, result.Combined)
	return result
}

// errTimeout reports a child that did not finish within its own timeout and
// was killed. It is a sentinel rather than an *exec.ExitError because the
// caller's message is about the harness's cap, not the signal the child died
// of.
var errTimeout = errors.New("erun: child exceeded its timeout")

// supervise waits for an already-started child, killing it if it outlives
// timeout. An exit it could observe comes back as the child's own result --
// nil, or an *exec.ExitError carrying its status -- with a wait rescued by
// delay reported as the completed run it is rather than as the trouble the
// wait itself ran into. errTimeout means the cap was what ended the run, and
// any other error means the child could not be run or observed at all.
//
// It arms delay on the child itself, rather than leaving that to its caller,
// because that is the bound this whole function exists around: a child leaves
// its stdout/stderr pipes open for as long as any process it started still
// holds them, and os/exec's own documentation names exactly that case as the
// reason WaitDelay exists. Without it, Wait -- and therefore this harness --
// waits for the *pipe* rather than the child, and no kill rescues it: the
// copier goroutines blocked on those pipes never look at signals at all, and a
// kill only ends the wait if the child was still running to receive it.
//
// Arming the delay is not on its own enough, which is why cancel is a
// parameter. WaitDelay bounds a wait only after the child exits or after the
// command's context is cancelled, and os/exec runs the goroutine that enforces
// the second clause only for a Cmd that has such a context. The cancellation
// on the timeout path below is therefore what makes the delay apply to the
// case that actually wedged this suite: a child that has *not* exited, so that
// Process.Wait never returns and the post-exit path -- which consults the
// delay independently -- is never reached. Cancelling also kills the child if
// it survived the signal below. With the context cancelled the wait becomes a
// deadline this harness enforces itself: the child is killed, the pipes it
// left open are closed, and the run concludes as the timeout it is rather than
// wedging the whole package past the test binary's own deadline.
//
// cancel, timeout and delay are parameters rather than the fixed values their
// caller passes so that wait can be driven directly at test speed.
func supervise(cmd *exec.Cmd, cancel context.CancelFunc, timeout, delay time.Duration) error {
	cmd.WaitDelay = delay
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		// A WaitDelay expiry is not a failure to observe the child: its status
		// was recorded on ProcessState before Wait began waiting on the pipes
		// a descendant of the child was still holding open. The caller reads
		// that status, so an expiry is reported as the completed run it is.
		if errors.Is(err, exec.ErrWaitDelay) {
			return nil
		}
		return err
	case <-time.After(timeout):
		// Give the signal a moment to flush the goroutine dump so the failure
		// names the blocked call, then cancel: cancellation is what arms the
		// delay, and os/exec's own watcher kills the child if it outlives the
		// signal. Waiting on done afterwards is bounded by delay for every
		// state the child can be in -- running, already reaped, or reaped with
		// a descendant holding its pipes -- so a timeout always concludes.
		_ = cmd.Process.Signal(syscall.SIGQUIT)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			cancel()
			<-done
		}
		return errTimeout
	}
}

// missingBinary matches the exec error Go reports when a command resolves no
// executable, in either platform's spelling.
var missingBinary = regexp.MustCompile(`exec: "([^"]+)": executable file not found in [$%]PATH%?`)

// reportUndeclaredBinaries turns a scenario's reliance on an ambient binary into
// an immediate failure. The suite's PATH is scrubbed, so this message means the
// command reached for an external binary the scenario never declared: on a host
// that happens to have it installed the scenario would instead capture that
// host's answer, and the golden would only reproduce where it was recorded.
func reportUndeclaredBinaries(t testing.TB, combined string) {
	t.Helper()
	seen := map[string]bool{}
	for _, match := range missingBinary.FindAllStringSubmatch(combined, -1) {
		name := match[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		t.Errorf("erun.Run: the command resolved no %q, so this scenario depends on whatever the host has installed; declare a stub (fixture.StubBinaryAdvanced + fixture.StubEnv) and re-record the golden", name)
	}
}
