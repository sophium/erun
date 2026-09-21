// Package erun owns the integration suite's compiled erun binary and runs it
// as a subprocess. It builds the binary once per `go test` run with coverage
// instrumentation so the production paths the suite exercises merge into one
// coverage profile.
package erun

import (
	"bytes"
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

	cmd := exec.Command(bin, args...)
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

	err := supervise(cmd, timeout, waitDelay)
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
// timeout. It returns the child's own Wait error -- nil, an *exec.ExitError,
// or ErrWaitDelay, all of which describe an observed exit -- or errTimeout if
// the cap was what ended the run.
//
// It arms delay on the child itself, rather than leaving that to its caller,
// because that is the bound this whole function exists around: a child that
// exits leaves its stdout/stderr pipes open for as long as any process it
// started still holds them, and os/exec's own documentation names exactly
// that case as the reason WaitDelay exists. Without it, Wait -- and therefore
// this harness -- waits for the *pipe* rather than the child, and no timeout
// can rescue it: a SIGKILL to a process that has already exited is a no-op,
// and the copier goroutines blocked on those pipes never look at signals at
// all. Owning the assignment here also keeps it reachable from a test that
// drives the same wait with a short delay and a child chosen to hold its own
// pipes open, which is the case that used to block forever.
//
// timeout and delay are arguments rather than the fixed caps their callers
// pass so that wait can be driven directly at test speed.
func supervise(cmd *exec.Cmd, timeout, delay time.Duration) error {
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
		// names the blocked call; fall back to Kill if the process ignores it.
		// Both waits are bounded above by waitDelay, so a child that has
		// already exited -- but whose pipes are still held open by something
		// it started -- is reported as the timeout it is instead of wedging
		// the whole package past the test binary's own deadline.
		_ = cmd.Process.Signal(syscall.SIGQUIT)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
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
