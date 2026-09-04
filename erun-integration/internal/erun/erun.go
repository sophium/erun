// Package erun owns the integration suite's compiled erun binary and runs it
// as a subprocess. It builds the binary once per `go test` run with coverage
// instrumentation so the production paths the suite exercises merge into one
// coverage profile.
package erun

import (
	"bytes"
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

var (
	buildOnce         sync.Once
	binaryPath        string
	buildErr          error
	coverDir          string
	invocationCounter int64
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

// CoverDir returns the parent directory under which each invocation of the
// instrumented binary writes its own coverage counter subdirectory (see
// Run). Coverage tooling that wants every counter file must read this
// directory's immediate subdirectories, not its own contents.
func CoverDir(t testing.TB) string {
	t.Helper()
	BinaryPath(t)
	return coverDir
}

// nextInvocationCoverDir allocates a fresh, never-reused subdirectory of
// coverDir for one subprocess invocation. Go's coverage runtime writes meta
// and counter files via write-then-rename; sharing one GOCOVERDIR across
// concurrently-running subprocesses lets one invocation's rename lose its
// source to another's, which corrupts nothing on disk but makes the
// subprocess print an "error: coverage meta-data emit failed" line to
// stderr that then breaks output-comparing goldens. Giving every invocation
// its own directory removes the shared resource instead of serializing
// around it.
func nextInvocationCoverDir(t testing.TB) string {
	t.Helper()
	id := atomic.AddInt64(&invocationCounter, 1)
	dir := filepath.Join(coverDir, fmt.Sprintf("%08d", id))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("erun.Run: create per-invocation cover dir: %v", err)
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
		timeout = 120 * time.Second
	}

	cmd := exec.Command(bin, args...)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	env := make([]string, 0, len(opts.Env)+2)
	env = append(env, opts.Env...)
	env = append(env, CoverDirEnv+"="+nextInvocationCoverDir(t))
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

	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start erun: %v", err)
	}
	go func() { done <- cmd.Wait() }()

	exitCode := 0
	select {
	case err := <-done:
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				t.Fatalf("erun exec error: %v", err)
			}
		}
	case <-time.After(timeout):
		// Give the signal a moment to flush the goroutine dump so the failure
		// names the blocked call; fall back to Kill if the process ignores it.
		_ = cmd.Process.Signal(syscall.SIGQUIT)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		t.Fatalf("erun timed out after %s; args=%v\n--- subprocess goroutine dump (stderr) ---\n%s", timeout, args, stderr.String())
	}

	result := Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Combined: stdout.String() + stderr.String(),
		ExitCode: exitCode,
	}
	reportUndeclaredBinaries(t, result.Combined)
	return result
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
