package main

import (
	"bytes"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These drive the real main() in a subprocess to catch its side effects.

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

// TestHelperProcess is not a real test; runERunAppMain re-executes it to run main().
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

// runERunAppMain runs main() in a subprocess under a throwaway HOME.
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
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("running erun-app helper process: %v", err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode, home
}

func assertNoSideEffects(t *testing.T, home string) {
	t.Helper()
	var found []string
	_ = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err == nil && path != home {
			found = append(found, strings.TrimPrefix(path, home))
		}
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

func TestParseHeadlessFlagsRecognizesHelpInAnyPosition(t *testing.T) {
	for _, args := range [][]string{
		{"--help"},
		{"-h"},
		{"-help"},
		{"--port", "34123", "--help"},
		{"--headless", "-h"},
	} {
		if result := parseHeadlessFlags(args); !result.Help {
			t.Errorf("parseHeadlessFlags(%v): Help = false, want true", args)
		}
	}
	for _, args := range [][]string{
		{},
		{"--headless"},
		{"--headless", "--port", "34123"},
		{"--port=34123"},
		{"help"},
	} {
		if result := parseHeadlessFlags(args); result.Help {
			t.Errorf("parseHeadlessFlags(%v): Help = true, want false", args)
		}
	}
}

func TestCLIUsageDocumentsTheRealFlags(t *testing.T) {
	for _, want := range []string{"--headless", "--port", "--help"} {
		if !strings.Contains(cliUsage, want) {
			t.Errorf("usage does not document %s:\n%s", want, cliUsage)
		}
	}
}

// The worker harness's probe-then-bind can lose its port to another process.
func TestListenHeadlessFallsBackWhenTheRequestedPortIsHeld(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("holding a port: %v", err)
	}
	defer func() { _ = held.Close() }()

	listener, err := listenHeadless(held.Addr().String())
	if err != nil {
		t.Fatalf("listenHeadless(%q) with the port held: %v", held.Addr().String(), err)
	}
	defer func() { _ = listener.Close() }()

	if listener.Addr().String() == held.Addr().String() {
		t.Fatalf("listener bound %s, which another process holds", listener.Addr())
	}
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dialing the announced address %s: %v", listener.Addr(), err)
	}
	_ = conn.Close()
}

func TestListenHeadlessKeepsNonAddressInUseFailuresFatal(t *testing.T) {
	listener, err := listenHeadless("127.0.0.1:not-a-port")
	if err == nil {
		_ = listener.Close()
		t.Fatal("listenHeadless accepted an unusable address")
	}
}

// Only a windowed launch (launchd's minimal environment) needs the import;
// headless must keep the caller's PATH stubs.
func TestOnlyAWindowedLaunchImportsTheLoginShellEnvironment(t *testing.T) {
	if !desktopDeps(true).skipLoginShellEnv {
		t.Fatal("a headless backend must keep the environment its caller supplied")
	}
	if desktopDeps(false).skipLoginShellEnv {
		t.Fatal("a windowed launch must still import the login shell's environment")
	}
}
