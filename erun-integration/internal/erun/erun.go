// Package erun builds the coverage-instrumented erun binary once per test run and runs it as a subprocess.
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

	"github.com/sophium/erun/erun-integration/internal/harnessexec"
)

// CoverDirEnv names where instrumented binaries write coverage counters.
const CoverDirEnv = "GOCOVERDIR"

// CoverPkgs limits the coverage gate to erun-cli and erun-common; other modules' runtime code
// is unreachable from --dry-run scenarios.
const CoverPkgs = "github.com/sophium/erun," +
	"github.com/sophium/erun/cmd," +
	"github.com/sophium/erun/internal/...," +
	"github.com/sophium/erun/erun-common"

// waitDelay bounds the wait on a child's pipes after it exits, since a descendant can hold them open.
// It stays far below the per-run timeout so a leaked descendant fails as a timeout, not a package panic.
const waitDelay = 10 * time.Second

var (
	buildOnce   sync.Once
	binaryPath  string
	errBuild    error
	coverDir    string
	procCounter int64
)

// BinaryPath returns the instrumented erun binary, compiling it on first call.
func BinaryPath(t testing.TB) string {
	t.Helper()
	buildOnce.Do(func() {
		binaryPath, errBuild = buildBinary()
	})
	if errBuild != nil {
		t.Fatalf("build erun: %v", errBuild)
	}
	return binaryPath
}

// CoverDir returns the shared coverage counter directory.
func CoverDir(t testing.TB) string {
	t.Helper()
	BinaryPath(t)
	return coverDir
}

// PrivateCoverDir allocates a coverage directory owned by one process. Go's coverage runtime names
// its meta-data temp file without a PID, so processes sharing a directory can silently lose coverage
// in a rename race. Callers spawning the binary outside Run must use it and set GOCOVERDIR.
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
	cmd := harnessexec.Command("go", args...)
	cmd.Dir = filepath.Join(repoRoot, "erun-cli")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go build -cover failed: %w\n%s", err, buf.String())
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

type Result struct {
	Stdout   string
	Stderr   string
	Combined string
	ExitCode int
}

type RunOptions struct {
	Cwd string
	// Env replaces the parent environment except GOCOVERDIR; build it from env.Setup.Env().
	Env     []string
	Stdin   string
	Timeout time.Duration
	// StdinFromDevNull binds stdin to the null device, a character device that is not a terminal,
	// which a pipe cannot emulate for stat-based TTY checks. Mutually exclusive with Stdin.
	StdinFromDevNull bool
}

// requireIsolatedEnv refuses a run that could read or write the developer's real erun config.
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
	if realHome := strings.TrimSpace(os.Getenv("HOME")); realHome != "" && home == realHome {
		t.Fatalf("erun.Run: RunOptions.Env points HOME at the developer's real home %q; scenarios must run against the env.New(t) tempdir", realHome)
	}
}

func Run(t testing.TB, args []string, opts RunOptions) Result {
	t.Helper()
	requireIsolatedEnv(t, opts.Env)
	bin := BinaryPath(t)

	timeout := opts.Timeout
	if timeout == 0 {
		// A hang-net, not a latency SLA: real commands can stall ~15-30s on a cold resolver or under
		// CPU contention. The worst case per scenario is this cap plus waitDelay.
		timeout = 120 * time.Second
	}

	// WaitDelay only bounds a wait on a child that exited; cancelling this context is what bounds a
	// child wedged past the timeout. supervise cancels it.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := harnessexec.CommandContext(ctx, bin, args...)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	env := make([]string, 0, len(opts.Env)+2)
	env = append(env, opts.Env...)
	env = append(env, CoverDirEnv+"="+PrivateCoverDir(t))
	// SIGQUIT on timeout dumps every goroutine's stack.
	env = append(env, "GOTRACEBACK=all")
	cmd.Env = env
	// Never inherit the developer's terminal: stdin is a non-terminal pipe unless the scenario opts
	// into ERUN_FORCE_TTY or StdinFromDevNull.
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
	// After a WaitDelay rescue the exit status lives on ProcessState, not the error.
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
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

// errTimeout is a sentinel because the failure is the harness's cap, not the child's signal.
var errTimeout = errors.New("erun: child exceeded its timeout")

// supervise waits for a started child, killing it past timeout. A wait rescued by delay reports the
// completed run; errTimeout means the cap ended it. cancel is required: os/exec enforces WaitDelay for
// a child that never exits only once its context is cancelled. Parameters allow testing at speed.
func supervise(cmd *exec.Cmd, cancel context.CancelFunc, timeout, delay time.Duration) error {
	cmd.WaitDelay = delay
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		// ProcessState was recorded before Wait blocked on pipes a descendant held open.
		if errors.Is(err, exec.ErrWaitDelay) {
			return nil
		}
		return err
	case <-time.After(timeout):
		// Let the goroutine dump flush, then cancel, which arms delay and kills a surviving child.
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

// missingBinary matches Go's exec error for an unresolved command on either platform.
var missingBinary = regexp.MustCompile(`exec: "([^"]+)": executable file not found in [$%]PATH%?`)

// reportUndeclaredBinaries fails a scenario that reached for an ambient binary it never stubbed.
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
