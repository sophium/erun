// Package harnessexec is the integration harness's only way to spawn a child
// process. Every child gets Cmd.WaitDelay, because a descendant still holding a
// finished child's stdout pipe otherwise blocks Cmd.Wait forever, and a HangNet
// context backstop for a child that never exits. exec_bound_test.go forbids a
// bare exec.Command elsewhere in the harness.
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

// WaitDelay bounds the wait on a finished child's output pipes; output still
// undrained this long after exit means a descendant holds the pipe.
const WaitDelay = 10 * time.Second

// HangNet bounds how long a harness child may run. It is a backstop set above
// the suite's -timeout so it never fails a healthy child; raise it with that
// timeout.
const HangNet = 60 * time.Minute

// Command builds a child process with both harness bounds armed.
func Command(name string, args ...string) *exec.Cmd {
	return command(context.Background(), WaitDelay, HangNet, name, args...)
}

// CommandContext is Command cancelled alongside parent; pass a deadline when
// the child needs a bound tighter than HangNet.
func CommandContext(parent context.Context, name string, args ...string) *exec.Cmd {
	return command(parent, WaitDelay, HangNet, name, args...)
}

// command takes the bounds as parameters so tests can drive them at test speed.
func command(parent context.Context, delay, net time.Duration, name string, args ...string) *exec.Cmd {
	ctx, cancel := context.WithCancel(parent)
	// The timer firing after the command concluded is a harmless no-op.
	time.AfterFunc(net, cancel)
	cmd := exec.CommandContext(ctx, name, args...)
	// Applies even to a child that exited with no context, so a caller's own
	// context cannot substitute for it.
	cmd.WaitDelay = delay
	if isGit(name) {
		if config, err := harnessGitConfig(); err == nil {
			cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+config)
		}
	}
	return cmd
}

// isGit selects children that get maintenance-off config via GIT_CONFIG_GLOBAL:
// detached `git maintenance run --auto` otherwise outlives the command and races
// t.TempDir cleanup ("directory not empty"). GIT_CONFIG_GLOBAL, unlike -c,
// reaches the receive-pack of a local push. A caller setting cmd.Env replaces it.
func isGit(name string) bool {
	base := strings.TrimSuffix(filepath.Base(name), ".exe")
	return base == "git"
}

var (
	harnessGitConfigOnce sync.Once
	harnessGitConfigPath string
	errHarnessGitConfig  error
)

// harnessGitConfig writes, once per process, the developer's global git config
// plus maintenance off, at a content-derived path.
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
			errHarnessGitConfig = err
			return
		}
		_, werr := tmp.WriteString(b.String())
		if err := errors.Join(werr, tmp.Close()); err != nil {
			_ = os.Remove(tmp.Name())
			errHarnessGitConfig = err
			return
		}
		if err := os.Rename(tmp.Name(), path); err != nil {
			_ = os.Remove(tmp.Name())
			errHarnessGitConfig = err
			return
		}
		harnessGitConfigPath = path
	})
	return harnessGitConfigPath, errHarnessGitConfig
}

// hostGlobalGitConfigs lists the developer's global git config files.
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

// MaintenanceOffGitConfig disables git's detached background maintenance.
const MaintenanceOffGitConfig = "[maintenance]\n\tauto = false\n[gc]\n\tauto = 0\n[receive]\n\tautogc = false\n"
