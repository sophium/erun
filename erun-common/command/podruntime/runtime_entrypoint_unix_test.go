//go:build !windows

package podruntime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

func waitForRuntimeCondition(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func writeRuntimeStub(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Running the installed hook command is the check, not finding a string: a
// command that is installed but does not reach the report verb is the same
// inertness the hooks exist to end. A session with no environment in scope must
// report nothing rather than guess one.
func TestRuntimeAISessionHookCommandReachesTheReportVerb(t *testing.T) {
	bin := t.TempDir()
	argvFile := filepath.Join(t.TempDir(), "erun-argv")
	writeRuntimeStub(t, bin, "erun", "cat >/dev/null\nprintf '%s\\n' \"$*\" >>"+argvFile+"\n")
	for _, binding := range AISessionHookBindings() {
		command := runtimeAISessionHookCommand(binding.ModelEvent)
		run := func(env ...string) string {
			_ = os.Remove(argvFile)
			cmd := exec.Command("/bin/sh", "-c", command)
			cmd.Env = append([]string{"PATH=" + bin + ":/usr/bin:/bin"}, env...)
			cmd.Stdin = strings.NewReader(`{"session_id":"0d5b1e0a-6f2b-4a0e-9a1f-2b1f8d3c4e77"}`)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("hook command failed: %v\n%s", err, out)
			}
			data, _ := os.ReadFile(argvFile)
			return strings.TrimSpace(string(data))
		}
		want := "activity ai-session hook --event " + string(binding.ModelEvent) + " --tool claude"
		if got := run("ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev"); got != want {
			t.Fatalf("%s: the hook should run `erun %s`, got %q", binding.ToolEvent, want, got)
		}
		if got := run(); got != "" {
			t.Fatalf("%s: a hook with no environment in scope must not report, got %q", binding.ToolEvent, got)
		}
	}
}

// The MCP edge is supervised: a crashed server is restarted with the same flags
// and the restart is logged, so a crash loop stays visible in the container log.
func TestRuntimeMCPIsRestartedWhenItExits(t *testing.T) {
	bin := t.TempDir()
	record := t.TempDir()
	writeRuntimeStub(t, bin, "emcp", "printf '%s\\n' \"$*\" >>"+filepath.Join(record, "argv")+"\nprintf '%s\\n' \"$$\" >>"+filepath.Join(record, "pids")+"\nexec sleep 300\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	boot := newTestRuntimeBoot(t, map[string]string{"ERUN_TENANT": "team", "ERUN_ENVIRONMENT": "dev", "ERUN_MCP_PORT": "17000"})
	var log syncBuffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log), Stdout: &log, Stderr: &log}
	supervisor := newRuntimeSupervisor(ctx)
	t.Cleanup(func() { supervisor.shutdown(syscall.SIGTERM) })
	if err := startRuntimeMCP(ctx, boot, supervisor); err != nil {
		t.Fatal(err)
	}
	readLines := func(name string) []string {
		data, _ := os.ReadFile(filepath.Join(record, name))
		return strings.Fields(strings.ReplaceAll(string(data), " ", "_"))
	}
	waitForRuntimeCondition(t, "emcp to start", func() bool { return len(readLines("pids")) >= 1 })
	if !strings.Contains(log.snapshot(), "starting erun MCP on 0.0.0.0:17000/mcp, metrics on 0.0.0.0:9100 (enabled=true)") {
		t.Fatalf("the edge start should be logged, got %q", log.snapshot())
	}
	firstPID := readLines("pids")[0]
	if err := exec.Command("kill", "-KILL", firstPID).Run(); err != nil {
		t.Fatal(err)
	}
	waitForRuntimeCondition(t, "emcp to be restarted", func() bool { return len(readLines("pids")) >= 2 })
	if !strings.Contains(log.snapshot(), "erun MCP exited; restarting (attempt 2)") {
		t.Fatalf("a restart should be logged, got %q", log.snapshot())
	}
	argv := readLines("argv")
	if argv[0] != argv[1] {
		t.Fatalf("a restarted server must get the same flags:\n%s\n%s", argv[0], argv[1])
	}
	supervisor.shutdown(syscall.SIGTERM)
	if supervisor.ownedCount() != 0 {
		t.Fatal("shutting down must leave no server running")
	}
}

// runtimeSourceOrigin builds a bare repository whose default branch has moved
// past the tag v1.2.3, so a clone that skipped its checkout is visible: it would
// sit on the later commit instead of the tag's.
func runtimeSourceOrigin(t *testing.T, git, dir string) (origin, tagCommit string) {
	t.Helper()
	src := filepath.Join(dir, "src")
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = src
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=erun", "GIT_AUTHOR_EMAIL=erun@example.invalid", "GIT_COMMITTER_NAME=erun", "GIT_COMMITTER_EMAIL=erun@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(src, "VERSION"), []byte("1.2.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "VERSION")
	run("commit", "-q", "-m", "release")
	run("tag", "v1.2.3")
	tagCommit = run("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(src, "VERSION"), []byte("1.2.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-q", "-am", "next")
	origin = filepath.Join(dir, "origin.git")
	run("clone", "-q", "--bare", src, origin)
	return origin, tagCommit
}

// The init reaps every zombie child that is not recorded as owned, so a child
// the boot runs outside the supervisor can be reaped by the init before os/exec
// waits for it. The wait then fails with "no child processes": a clone that
// succeeded reads as failed, its checkout is skipped, and the next boot sees a
// .git and never checks the release ref out. Every git child the boot runs must
// therefore be owned for as long as it runs. The stub holds each invocation
// until the test has looked; supervisor.start records a child as owned in the
// same critical section that starts it, so taking the lock settles the answer.
func TestRuntimeBootGitChildrenAreOwnedWhileTheyRun(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is a prerequisite of this test: %v", err)
	}
	work := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(work, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	origin, tagCommit := runtimeSourceOrigin(t, realGit, work)

	gate := filepath.Join(work, "gate")
	if err := os.MkdirAll(gate, 0o755); err != nil {
		t.Fatal(err)
	}
	pidsFile := filepath.Join(gate, "pids")
	writeRuntimeStub(t, gate, "git", "echo $$ >>'"+pidsFile+"'\nwhile [ ! -e '"+gate+"/release.'$$ ]; do sleep 0.01; done\nexec '"+realGit+"' \"$@\"\n")
	t.Setenv("ERUN_GIT_BIN", filepath.Join(gate, "git"))

	repo := filepath.Join(work, "git", "repo")
	boot := newTestRuntimeBoot(t, map[string]string{"ERUN_REPO_URL": origin, "ERUN_REPO_REF": "v1.2.3", "ERUN_REPO_PATH": repo})
	var log syncBuffer
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, &log, &log), Stdout: &log, Stderr: &log}
	supervisor := newRuntimeSupervisor(ctx)

	finish := watchRuntimeGitGate(supervisor, gate, pidsFile)
	if err := ensureRuntimeGitSafeDirectory(ctx, boot, supervisor); err != nil {
		t.Fatalf("safe.directory: %v", err)
	}
	if err := ensureRuntimeSource(ctx, boot, supervisor); err != nil {
		t.Fatalf("runtime source: %v", err)
	}
	invocations, unowned := finish()

	if invocations < 4 {
		t.Fatalf("expected the boot to run git for safe.directory (read, add), clone and checkout, saw %d invocation(s)\n%s", invocations, log.snapshot())
	}
	if len(unowned) > 0 {
		t.Fatalf("%d of %d git children ran without being owned by the supervisor, so the init's reaper could take their exit status (pids %v)", len(unowned), invocations, unowned)
	}
	head, err := exec.Command(realGit, "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(head)) != tagCommit {
		t.Fatalf("the runtime source should be checked out at v1.2.3 (%s), got %q (%v)\n%s", tagCommit, strings.TrimSpace(string(head)), err, log.snapshot())
	}
	safe, _ := exec.Command(realGit, "config", "--global", "--get-all", "safe.directory").Output()
	if !strings.Contains(string(safe), repo) {
		t.Fatalf("the repository should be a git safe.directory, got %q", safe)
	}
}

// watchRuntimeGitGate releases each git invocation the gate stub holds, after
// recording whether the supervisor owned it. finish stops the watch and returns
// how many invocations it saw and the pids that were not owned.
func watchRuntimeGitGate(supervisor *runtimeSupervisor, gate, pidsFile string) (finish func() (int, []int)) {
	var unowned []int
	invocations := 0
	stop := make(chan struct{})
	watched := make(chan struct{})
	release := func(field string, released map[int]bool) {
		pid, err := strconv.Atoi(field)
		if err != nil || released[pid] {
			return
		}
		supervisor.mu.Lock()
		_, owned := supervisor.owned[pid]
		supervisor.mu.Unlock()
		if !owned {
			unowned = append(unowned, pid)
		}
		invocations++
		released[pid] = true
		_ = os.WriteFile(filepath.Join(gate, "release."+field), nil, 0o644)
	}
	go func() {
		defer close(watched)
		released := map[int]bool{}
		for {
			data, _ := os.ReadFile(pidsFile)
			for _, field := range strings.Fields(string(data)) {
				release(field, released)
			}
			select {
			case <-stop:
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	return func() (int, []int) {
		close(stop)
		<-watched
		return invocations, unowned
	}
}

func (b *syncBuffer) snapshot() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
