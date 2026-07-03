// Package erun owns the integration suite's compiled erun binary and the
// helper that runs it as a subprocess. The harness compiles the binary once
// per `go test` invocation with coverage instrumentation enabled so the
// production code paths exercised by the integration suite contribute to a
// merged coverage profile.
package erun

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// CoverDirEnv is the environment variable instrumented Go binaries inspect to
// decide where to write coverage counter files. The harness sets it for every
// invocation so all subtests merge into a single profile.
const CoverDirEnv = "GOCOVERDIR"

// CoverPkgs is the package selector passed to `go build -cover -coverpkg`.
// Anything matching here counts toward the integration coverage report.
//
// The integration suite gates on the erun-cli (the root module
// `github.com/sophium/erun` plus its `cmd` and `internal/...` packages) and
// erun-common modules only. erun-mcp / erun-backend / erun-ui carry significant
// runtime code that integration --dry-run scenarios cannot exercise without
// substantial extra harness work, and pulling them into this profile would
// drag the gate down for reasons unrelated to the cli/common code we want
// covered today.
const CoverPkgs = "github.com/sophium/erun," +
	"github.com/sophium/erun/cmd," +
	"github.com/sophium/erun/internal/...," +
	"github.com/sophium/erun/erun-common"

var (
	buildOnce  sync.Once
	binaryPath string
	buildErr   error
	coverDir   string
)

// BinaryPath compiles the erun binary with coverage instrumentation on first
// call and returns its absolute path. Subsequent calls reuse the same binary.
// Tests must call this from a *testing.T because failures should mark the
// caller's test as failed rather than panic.
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

// CoverDir returns the directory where the instrumented binary writes counter
// files. It is created (or read from $GOCOVERDIR if the harness was invoked
// from outside) the first time BinaryPath is called.
func CoverDir(t testing.TB) string {
	t.Helper()
	BinaryPath(t)
	return coverDir
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

// repoRoot walks up from this source file's location to find the repo root
// (where erun-cli, erun-common, erun-mcp live as siblings).
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
	// Cwd is the working directory for the subprocess. Empty means the harness
	// uses a fresh temp dir.
	Cwd string
	// Env is the full environment for the subprocess (it replaces the parent
	// environment except for GOCOVERDIR which the harness always injects).
	// PATH should be included by callers that need it.
	Env []string
	// Stdin is fed to the subprocess on stdin.
	Stdin string
	// Timeout caps the subprocess runtime. Zero means 30s.
	Timeout time.Duration
}

// requireIsolatedEnv fails the test when a scenario invokes the binary
// without an isolated HOME and XDG_CONFIG_HOME. Every scenario must route
// its environment through env.New + Setup.Env() (possibly appended to);
// a scenario that omits them would silently read — or worse, write — the
// developer's real erun config, and its golden would capture machine
// state. Guard here so the mistake fails fast at the harness boundary.
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

// Run invokes the compiled binary with the given args. The caller's
// RunOptions.Env is used verbatim except GOCOVERDIR is always injected so
// counters land in the harness coverage dir.
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
	env = append(env, CoverDirEnv+"="+coverDir)
	// So a SIGQUIT on timeout dumps every goroutine's stack, not just the
	// current one — turning an opaque hang into a report of where it stuck.
	env = append(env, "GOTRACEBACK=all")
	cmd.Env = env
	// Always feed stdin from a buffer (empty when the scenario passes no
	// Stdin) so the subprocess never inherits the developer's terminal. This
	// keeps stdin a non-terminal pipe locally, matching CI's /dev/null stdin,
	// so stdin-TTY-gated branches (e.g. the interactive-gh-auth gate)
	// behave the same everywhere. Scenarios that need the interactive branch
	// opt in explicitly via the ERUN_FORCE_TTY seam.
	cmd.Stdin = bytes.NewBufferString(opts.Stdin)

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
		// SIGQUIT makes the Go runtime print all goroutine stacks to stderr
		// before dying; give it a moment to flush so the failure names the
		// blocked call. Fall back to Kill if it ignores the signal.
		_ = cmd.Process.Signal(syscall.SIGQUIT)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		t.Fatalf("erun timed out after %s; args=%v\n--- subprocess goroutine dump (stderr) ---\n%s", timeout, args, stderr.String())
	}

	return Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Combined: stdout.String() + stderr.String(),
		ExitCode: exitCode,
	}
}
