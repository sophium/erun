package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The bug: --help, -h, and an unrecognized flag all silently
// launched the full desktop application -- orchestrator wiring, workspace
// mirror syncs, and outbound network calls included -- instead of printing
// usage or a clear error. These tests drive the real main() entry point in a
// subprocess (via TestHelperProcess below) so they exercise exactly what a
// user invoking the compiled `erun-app` binary would hit, including the
// side effects the bug actually caused.

func TestParseHeadlessFlagsRecognizesHelp(t *testing.T) {
	for _, arg := range []string{"--help", "-help", "-h"} {
		result := parseHeadlessFlags([]string{arg})
		if !result.Help {
			t.Errorf("parseHeadlessFlags(%q): Help = false, want true", arg)
		}
		if result.Unknown != "" {
			t.Errorf("parseHeadlessFlags(%q): Unknown = %q, want empty", arg, result.Unknown)
		}
	}
}

func TestParseHeadlessFlagsRecognizesUnknownFlag(t *testing.T) {
	result := parseHeadlessFlags([]string{"--nonexistent-flag-xyz"})
	if result.Unknown != "--nonexistent-flag-xyz" {
		t.Errorf("Unknown = %q, want --nonexistent-flag-xyz", result.Unknown)
	}
	if result.Help {
		t.Errorf("Help = true, want false")
	}
}

func TestParseHeadlessFlagsPreservesKnownFlags(t *testing.T) {
	result := parseHeadlessFlags([]string{"--headless", "--port", "34199"})
	if !result.Headless {
		t.Errorf("Headless = false, want true")
	}
	if result.Port != 34199 {
		t.Errorf("Port = %d, want 34199", result.Port)
	}
	if result.Help || result.Unknown != "" {
		t.Errorf("Help/Unknown set for a run made entirely of recognized flags: %+v", result)
	}
}

func TestParseHeadlessFlagsNoArgsIsAnOrdinaryRun(t *testing.T) {
	result := parseHeadlessFlags(nil)
	if result.Headless || result.Help || result.Unknown != "" {
		t.Errorf("no-args parse should be a plain launch, got %+v", result)
	}
	if result.Port != defaultHeadlessPort {
		t.Errorf("Port = %d, want default %d", result.Port, defaultHeadlessPort)
	}
}

// TestHelperProcess is not a real test. It is re-executed as a subprocess by
// runERunAppMain below with GO_WANT_ERUN_APP_HELPER_PROCESS=1 set, so that
// tests can drive the actual main() entry point -- and therefore its real
// side effects -- under a throwaway HOME instead of just asserting on the
// pure flag-parsing helper.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_ERUN_APP_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	os.Args = append([]string{"erun-app"}, args...)
	main()
}

// runERunAppMain re-executes the current test binary as a fresh process that
// calls main() with the given CLI args, under an isolated, throwaway HOME so
// nothing it does can touch the operator's real config. Returns the process's
// stdout, stderr, and the home directory used, so a caller can assert on
// exactly what a user invoking `erun-app` would see and on the filesystem
// state such an invocation left behind.
func runERunAppMain(t *testing.T, args ...string) (stdout, stderr string, exitCode int, home string) {
	t.Helper()
	home = t.TempDir()

	cmdArgs := append([]string{"-test.run=TestHelperProcess", "--"}, args...)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Env = append(os.Environ(),
		"GO_WANT_ERUN_APP_HELPER_PROCESS=1",
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
	)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("running erun-app helper process: %v", err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode, home
}

// assertNoSideEffects fails the test if the help/error path left anything
// behind under home -- no durable app log, no ERun config dir, nothing. This
// is the assertion the bug report's own repro cared about most: a --help
// probe must not be observably different from never having run the binary.
func assertNoSideEffects(t *testing.T, home string) {
	t.Helper()
	var found []string
	_ = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == home {
			return nil
		}
		found = append(found, strings.TrimPrefix(path, home))
		return nil
	})
	if len(found) > 0 {
		t.Errorf("expected no side effects under HOME, found: %v", found)
	}
}

func TestHelpFlagPrintsUsageExitsCleanAndHasNoSideEffects(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			stdout, _, exitCode, home := runERunAppMain(t, flag)
			if exitCode != 0 {
				t.Errorf("exit code = %d, want 0", exitCode)
			}
			if !strings.Contains(stdout, "Usage:") || !strings.Contains(stdout, "--headless") {
				t.Errorf("stdout does not look like usage text: %q", stdout)
			}
			assertNoSideEffects(t, home)
		})
	}
}

func TestUnknownFlagFailsExitsNonZeroAndHasNoSideEffects(t *testing.T) {
	stdout, stderr, exitCode, home := runERunAppMain(t, "--nonexistent-flag-xyz")
	if exitCode == 0 {
		t.Errorf("exit code = 0, want non-zero for an unrecognized flag")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty (error path should write to stderr)", stdout)
	}
	if !strings.Contains(stderr, "--nonexistent-flag-xyz") {
		t.Errorf("stderr does not name the offending flag: %q", stderr)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Errorf("stderr does not include usage text: %q", stderr)
	}
	assertNoSideEffects(t, home)
}
