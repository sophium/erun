package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// reentryHelpers run when this binary is re-executed as a helper process;
// each returns false when it is not the one requested.
var reentryHelpers []func() (int, bool)

// TestMain dispatches re-executed helpers and isolates tests from the host's
// pod identity, timing dir, browser openers, and git maintenance.
func TestMain(m *testing.M) {
	for _, reenter := range reentryHelpers {
		if code, reentered := reenter(); reentered {
			os.Exit(code)
		}
	}
	cleanup, err := isolateTestSuite()
	if err != nil {
		fmt.Fprintf(os.Stderr, "isolate test suite: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// timingRecordDirEnv mirrors base/core's TimingRecordDirEnv without importing it.
const timingRecordDirEnv = "ERUN_TIMING_DIR"

func isolateTestSuite() (func(), error) {
	for _, name := range []string{"ERUN_TENANT", "ERUN_ENVIRONMENT"} {
		if err := os.Unsetenv(name); err != nil {
			return nil, err
		}
	}
	cleanup := func() {}
	if _, ok := os.LookupEnv(timingRecordDirEnv); !ok {
		dir, err := os.MkdirTemp("", "erun-timing-test-")
		if err != nil {
			return nil, err
		}
		if err := os.Setenv(timingRecordDirEnv, dir); err != nil {
			return nil, err
		}
		cleanup = func() { _ = os.RemoveAll(dir) }
	}
	if err := isolateSuiteBrowserOpeners(); err != nil {
		return nil, err
	}
	return cleanup, isolateSuiteGitMaintenance()
}

// isolateSuiteBrowserOpeners points opener seams at an inert binary unless a test sets its own.
func isolateSuiteBrowserOpeners() error {
	inert, err := exec.LookPath("true")
	if err != nil {
		return err
	}
	for _, name := range []string{"ERUN_OPEN_BIN", "ERUN_XDG_OPEN_BIN", "ERUN_CMD_BIN"} {
		if _, ok := os.LookupEnv(name); ok {
			continue
		}
		if err := os.Setenv(name, inert); err != nil {
			return err
		}
	}
	return nil
}

// isolateSuiteGitMaintenance disables git maintenance via a global config that
// includes the user's own, unless GIT_CONFIG_GLOBAL is already set.
func isolateSuiteGitMaintenance() error {
	if _, ok := os.LookupEnv("GIT_CONFIG_GLOBAL"); ok {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(home, ".config")
	}
	var b strings.Builder
	for _, include := range []string{filepath.Join(xdg, "git", "config"), filepath.Join(home, ".gitconfig")} {
		b.WriteString("[include]\n\tpath = " + include + "\n")
	}
	b.WriteString("[maintenance]\n\tauto = false\n[gc]\n\tauto = 0\n[receive]\n\tautogc = false\n")
	path, err := isolateSuiteGitConfigFile(b.String())
	if err != nil {
		return err
	}
	return os.Setenv("GIT_CONFIG_GLOBAL", path)
}

// isolateSuiteGitConfigFile names the file by content so suites share one file per config.
func isolateSuiteGitConfigFile(config string) (string, error) {
	sum := sha256.Sum256([]byte(config))
	path := filepath.Join(os.TempDir(), "erun-common-test-gitconfig-"+hex.EncodeToString(sum[:8]))
	if existing, err := os.ReadFile(path); err == nil && string(existing) == config {
		return path, nil
	}
	tmp, err := os.CreateTemp(os.TempDir(), "erun-common-test-gitconfig-*")
	if err != nil {
		return "", err
	}
	if _, err := tmp.WriteString(config); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return path, os.Rename(tmp.Name(), path)
}
