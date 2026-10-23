// Package harnessexec is the integration harness's only way to spawn a child
// process.
//
// The suite runs real subprocesses on purpose: the compiled CLI under test,
// the Go toolchain that builds it and the stub binaries, plus the git, atlas
// and shell tools the fixtures drive. Each one of those is a chance to wedge
// the whole test binary through os/exec rather than through anything a
// scenario asserts, so every one of them is built here.
//
// The wedge is not a slow child. It is a finished one. os/exec gives a child
// its stdout and stderr as pipes and drains them from goroutines of its own,
// and Cmd.Wait waits for those goroutines rather than for the child. A
// process the child started still holds the write end after the child itself
// is gone, so the drain never reaches EOF and Wait blocks on a child that has
// already exited -- with nothing left to wait for, and no signal that would
// end it, because a kill only concludes a wait whose child was still running
// to receive it. The block is indistinguishable from a hang in whichever test
// happened to be running when it bit, which is what made it move from one
// call site to the next as each was patched.
//
// Cmd.WaitDelay is the exact bound for that state: its own documentation
// describes it as the time to wait for I/O "after the command has exited",
// which is precisely the descendant-holds-the-pipe case, and on expiry os/exec
// closes the pipes Wait is draining. It needs no context to apply to a child
// that has exited -- awaitGoroutines starts its own timer when the Cmd has
// none -- so arming it in one constructor is what makes the unbounded wait
// impossible by construction rather than unlikely. A bare exec.Command
// anywhere in the harness would reopen it, so exec_bound_test.go fails the
// build when one appears outside this package.
//
// CommandContext covers the other stuck shape, a child that never exits at
// all: there the drain never begins, so the post-exit path that consults
// WaitDelay is never reached and only cancelling the command's context ends
// the wait. A caller that knows its own deadline supplies one -- Run's cap on
// the CLI under test is the real case -- and HangNet is the backstop for a
// caller that does not.
package harnessexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// WaitDelay bounds the harness's wait on a finished child's output pipes.
//
// It is deliberately far below the harness's own per-run deadline rather than
// a latency SLA: output that has not drained this long after the child exited
// is a descendant holding the pipe, not a slow write, and a scenario that
// leaves one behind must fail as its own bounded failure rather than wait out
// the test binary's deadline and panic the package.
const WaitDelay = 10 * time.Second

// HangNet bounds how long a harness child may run without exiting.
//
// It is a backstop, not a latency SLA, and it is set longer than the suite's
// own package deadline so it can never fail a healthy child: that deadline is
// the fixed -timeout the erun-cli image's test stage passes, a value this
// constant can be compared against rather than a number that moves with the
// environment. What this catches is a
// run whose deadline was raised past that cap or disabled outright, where an
// unbounded wait would otherwise never conclude at all.
//
// The deadline used to be Go's ten-minute default, with nothing passing
// -timeout; the suite was then failed by its own clock on a contended node
// while still making progress, so the gate now sets one. That is the case this
// comment always anticipated -- raising the deadline is exactly what makes a
// bounded wait unbounded if the backstop does not move with it.
//
// A child that is supposed to outlive its caller -- the harness starts an
// emcp server and port holders a scenario keeps alive on purpose -- is killed
// by its own test cleanup long before this; the net exists for the child
// nothing else would end.
const HangNet = 60 * time.Minute

// Command builds a child process with the harness's bounds armed. It is the
// only constructor the harness uses; see the package comment for why.
func Command(name string, args ...string) *exec.Cmd {
	return command(context.Background(), WaitDelay, HangNet, name, args...)
}

// CommandContext builds a child process with the harness's bounds armed,
// cancelled alongside parent.
//
// Pass a context with a deadline when the child's own lifetime needs a bound
// tighter than HangNet -- the CLI under test is the case that matters, since
// only it spawns processes that outlive it, and Run already caps it. Pass
// context.Background() to take the backstop alone.
func CommandContext(parent context.Context, name string, args ...string) *exec.Cmd {
	return command(parent, WaitDelay, HangNet, name, args...)
}

// command is what Command and CommandContext both delegate to. The two bounds
// are parameters rather than the constants their callers pass for the same
// reason Run's supervisor takes its timeout and delay that way: so a test can
// drive a bound at test speed instead of waiting one out.
func command(parent context.Context, delay, net time.Duration, name string, args ...string) *exec.Cmd {
	ctx, cancel := context.WithCancel(parent)
	// The backstop is what references cancel, and cancelling is what ends a
	// wait on a child that never exited. A command that concludes first
	// leaves the timer to fire harmlessly: os/exec's cancellation watcher has
	// already returned by then, so the shot at a reaped process is a no-op.
	time.AfterFunc(net, cancel)
	cmd := exec.CommandContext(ctx, name, args...)
	// The bound this package exists for. It applies to a child that has
	// exited with no context and no cancellation at all, which is why a call
	// site cannot substitute for it by arming a context of its own.
	cmd.WaitDelay = delay
	if isGit(name) {
		if config, err := harnessGitConfig(); err == nil {
			cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+config)
		}
	}
	return cmd
}

// A git child runs with automatic maintenance off. After a commit, fetch or
// push, git detaches `git maintenance run --auto` -- for a local push, in the
// receiving repository too -- and that process outlives the command, so under
// load it can still be writing into a scenario's repositories when
// t.TempDir's cleanup removes them, failing the scenario with "directory not
// empty". The setting travels as GIT_CONFIG_GLOBAL, which git keeps for the
// receive-pack it runs for a local remote (it drops -c and GIT_CONFIG_COUNT
// there); the file includes the developer's own global config so identity and
// everything else the fixtures rely on are unchanged. The CLI under test runs
// git with the scenario's own HOME instead, which env.New configures the same
// way. A caller that sets cmd.Env itself replaces this.
func isGit(name string) bool {
	base := strings.TrimSuffix(filepath.Base(name), ".exe")
	return base == "git"
}

var (
	harnessGitConfigOnce sync.Once
	harnessGitConfigPath string
	harnessGitConfigErr  error
)

// harnessGitConfig writes, once per process, the global git config git
// children run with: the developer's global config plus maintenance off. Its
// name is derived from its content, so repeated runs reuse one file.
func harnessGitConfig() (string, error) {
	harnessGitConfigOnce.Do(func() {
		var b strings.Builder
		for _, include := range hostGlobalGitConfigs() {
			b.WriteString("[include]\n\tpath = " + include + "\n")
		}
		b.WriteString(MaintenanceOffGitConfig)
		sum := sha256.Sum256([]byte(b.String()))
		path := filepath.Join(os.TempDir(), "erun-integration-gitconfig-"+hex.EncodeToString(sum[:8]))
		if existing, err := os.ReadFile(path); err == nil && string(existing) == b.String() {
			harnessGitConfigPath = path
			return
		}
		tmp, err := os.CreateTemp(os.TempDir(), "erun-integration-gitconfig-*")
		if err != nil {
			harnessGitConfigErr = err
			return
		}
		_, werr := tmp.WriteString(b.String())
		if err := errors.Join(werr, tmp.Close()); err != nil {
			_ = os.Remove(tmp.Name())
			harnessGitConfigErr = err
			return
		}
		if err := os.Rename(tmp.Name(), path); err != nil {
			_ = os.Remove(tmp.Name())
			harnessGitConfigErr = err
			return
		}
		harnessGitConfigPath = path
	})
	return harnessGitConfigPath, harnessGitConfigErr
}

// hostGlobalGitConfigs lists the global config files git would read for the
// developer; a missing include is ignored by git.
func hostGlobalGitConfigs() []string {
	if explicit := os.Getenv("GIT_CONFIG_GLOBAL"); explicit != "" {
		return []string{explicit}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(home, ".config")
	}
	return []string{filepath.Join(xdg, "git", "config"), filepath.Join(home, ".gitconfig")}
}

// MaintenanceOffGitConfig turns off the background maintenance git would
// otherwise detach after commits, fetches and pushes.
const MaintenanceOffGitConfig = "[maintenance]\n\tauto = false\n[gc]\n\tauto = 0\n[receive]\n\tautogc = false\n"
