//go:build linux

package podruntime

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

// Init duties are process-wide, so each case runs in its own copy of the test binary.
const runtimeEntrypointHelperEnv = "ERUN_TEST_RUNTIME_ENTRYPOINT_ARGS"

// TestRuntimeEntrypointHelperProcess is the child-process entrypoint, not a test.
func TestRuntimeEntrypointHelperProcess(t *testing.T) {
	raw := os.Getenv(runtimeEntrypointHelperEnv)
	if raw == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		os.Exit(90)
	}
	home, _ := os.UserHomeDir()
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, os.Stderr, os.Stderr), Stdout: os.Stdout, Stderr: os.Stderr}
	code, err := RunRuntimeEntrypoint(ctx, RuntimeEntrypointParams{Command: args, ERunPath: "/bin/false", HomeDir: home})
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
	}
	os.Exit(code)
}

func runtimeEntrypointHelper(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeEntrypointHelperProcess$")
	cmd.Env = append(os.Environ(),
		runtimeEntrypointHelperEnv+"="+string(encoded),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"ERUN_OUTPUTS_DIR="+filepath.Join(home, "outputs"),
		"ERUN_GO_BUILD_CACHE_MAX_BYTES=0",
		"ERUN_MCP_ENABLED=false",
		"ERUN_SSHD_ENABLED=false",
	)
	cmd.Dir = home
	return cmd
}

func runtimeHelperExitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run the entrypoint helper: %v", err)
	}
	return exitErr.ExitCode()
}

func TestRuntimeEntrypointExitsWithTheCommandsCode(t *testing.T) {
	out, err := runtimeEntrypointHelper(t, "sh", "-c", "exit 7").CombinedOutput()
	if code := runtimeHelperExitCode(t, err); code != 7 {
		t.Fatalf("the container must exit with its command's code, got %d\n%s", code, out)
	}
}

func TestRuntimeEntrypointReportsACommandKilledByASignal(t *testing.T) {
	out, err := runtimeEntrypointHelper(t, "sh", "-c", "kill -TERM $$").CombinedOutput()
	if code := runtimeHelperExitCode(t, err); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("a command killed by SIGTERM must exit 143, got %d\n%s", code, out)
	}
}

// The command leaves an exiting orphan, then fails with 3 if a zombie remains.
func TestRuntimeEntrypointReapsOrphanedZombies(t *testing.T) {
	script := `(sleep 0.1 &); sleep 1
for stat in /proc/[0-9]*/stat; do
  set -- $(cat "$stat" 2>/dev/null)
  if [ "$4" = "$PPID" ] && [ "$3" = "Z" ]; then exit 3; fi
done
exit 0`
	out, err := runtimeEntrypointHelper(t, "sh", "-c", script).CombinedOutput()
	if code := runtimeHelperExitCode(t, err); code != 0 {
		t.Fatalf("an orphan that exited must be reaped, got exit %d\n%s", code, out)
	}
}

func TestRuntimeEntrypointForwardsTerminationToTheCommand(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := runtimeEntrypointHelper(t, "sh", "-c", `trap 'exit 42' TERM; touch `+ready+`; while :; do sleep 0.1; done`)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitForRuntimeFile(t, ready)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if code := runtimeHelperExitCode(t, cmd.Wait()); code != 42 {
		t.Fatalf("the command's own handling of the forwarded SIGTERM decides the exit code, got %d", code)
	}
}

func TestRuntimeEntrypointDevopsBootStopsOnSIGTERM(t *testing.T) {
	cmd := runtimeEntrypointHelper(t, "devops")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	home := cmd.Dir
	waitForRuntimeFile(t, filepath.Join(home, ".erun-shell-hook.bashrc"))
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if code := runtimeHelperExitCode(t, cmd.Wait()); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("the devops boot should exit 143 on SIGTERM, got %d", code)
	}
}

func waitForRuntimeFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

const runtimeSourceHelperEnv = "ERUN_TEST_RUNTIME_SOURCE"

// TestRuntimeSourceHelperProcess is a child-process helper, not a test.
func TestRuntimeSourceHelperProcess(t *testing.T) {
	raw := os.Getenv(runtimeSourceHelperEnv)
	if raw == "" {
		return
	}
	var params struct{ URL, Ref, Repo string }
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		os.Exit(90)
	}
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, os.Stderr, os.Stderr), Stdout: os.Stderr, Stderr: os.Stderr}
	supervisor := newRuntimeSupervisor(ctx)
	stop := supervisor.startInit()
	sweeping := make(chan struct{})
	go func() {
		for {
			select {
			case <-sweeping:
				return
			default:
				supervisor.reapOrphans()
			}
		}
	}()
	boot := newRuntimeBoot(RuntimeEntrypointParams{Env: func(key string) string {
		return map[string]string{"ERUN_REPO_URL": params.URL, "ERUN_REPO_REF": params.Ref, "ERUN_REPO_PATH": params.Repo}[key]
	}, HomeDir: filepath.Dir(params.Repo)})
	_ = ensureRuntimeSource(ctx, boot, supervisor)
	var head strings.Builder
	revParse := exec.Command("git", "-C", params.Repo, "rev-parse", "HEAD")
	revParse.Stdout = &head
	_ = supervisor.run(revParse)
	close(sweeping)
	stop()
	_, _ = os.Stdout.WriteString(strings.TrimSpace(head.String()))
	os.Exit(0)
}

func TestRuntimeSourceIsCheckedOutWhileTheInitReaps(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is a prerequisite of this test: %v", err)
	}
	work := t.TempDir()
	origin, tagCommit := runtimeSourceOrigin(t, git, work)
	encoded, err := json.Marshal(map[string]string{"URL": origin, "Ref": "v1.2.3", "Repo": filepath.Join(work, "home", "git", "repo")})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeSourceHelperProcess$")
	cmd.Env = append(os.Environ(), runtimeSourceHelperEnv+"="+string(encoded), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(work, "gitconfig"))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helper: %v\n%s", err, stderr.String())
	}
	if got := strings.TrimSpace(string(out)); got != tagCommit {
		t.Fatalf("the runtime source should be checked out at v1.2.3 (%s), got %q\n%s", tagCommit, got, stderr.String())
	}
}

func TestRuntimeForegroundReceivesASignalThatArrivedBeforeItStarted(t *testing.T) {
	supervisor := newRuntimeSupervisor(core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, os.Stderr, os.Stderr)})
	supervisor.handleSignal(syscall.SIGTERM)
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := supervisor.runForeground([]string{"sleep", "30"})
		done <- result{code, err}
	}()
	select {
	case got := <-done:
		if got.err != nil || got.code != 128+int(syscall.SIGTERM) {
			t.Fatalf("the pending SIGTERM should stop the command with 143, got %d (%v)", got.code, got.err)
		}
	case <-time.After(10 * time.Second):
		supervisor.signalOwned(os.Kill)
		t.Fatal("a SIGTERM that arrived before the command started was dropped: the command kept running")
	}
}
