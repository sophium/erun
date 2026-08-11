package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// machOArtifactBytes opens with the little-endian 64-bit Mach-O magic a
// darwin/arm64 cross-build produces. The desktop classifies by content, so this
// is all it takes to reach the signing branch.
var machOArtifactBytes = append([]byte{0xCF, 0xFA, 0xED, 0xFE}, []byte("erun darwin arm64 artifact")...)

// stubCodesign routes the shared codesign lookup at a script that records its
// argv and reports the file as carrying no signature, so a test can watch what
// the desktop does without a macOS host.
func stubCodesign(t *testing.T, dir string) string {
	t.Helper()
	log := filepath.Join(dir, "codesign-calls.log")
	script := filepath.Join(dir, "codesign")
	body := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> '" + log + "'\n" +
		"case \"$1\" in -d) exit 1 ;; esac\n" +
		"exit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write codesign stub: %v", err)
	}
	t.Setenv("ERUN_CODESIGN_BIN", script)
	return log
}

func seedOrchestratorArtifact(t *testing.T, name string, content []byte) string {
	t.Helper()
	dir, err := ensureOrchestratorOutputsDir("erun")
	if err != nil {
		t.Fatalf("ensure outputs dir: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	return path
}

// arm64 macOS SIGKILLs a Mach-O carrying no signature at all, without printing
// anything, so the desktop signs one before handing it to the operator to run —
// and says so, because the file was modified on its way in.
func TestRunOrchestratorOutputOnHostSignsAnUnsignedDarwinArtifact(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the codesign stub is a POSIX shell script")
	}
	home := withOrchestratorConfigHome(t)
	artifact := seedOrchestratorArtifact(t, "erun-darwin-arm64", machOArtifactBytes)
	// ERUN_HOST_OS_OVERRIDE pins the darwin branch so the test runs anywhere.
	t.Setenv("ERUN_HOST_OS_OVERRIDE", "darwin")
	codesignLog := stubCodesign(t, home)

	emits := newCapturedEmits()
	app := &App{emitFn: emits.fn(), deps: erunUIDeps{launchHostArtifact: func(string, string) error { return nil }}}
	if err := app.RunOrchestratorOutputOnHost("erun", "erun-darwin-arm64"); err != nil {
		t.Fatalf("run: %v", err)
	}

	calls, err := os.ReadFile(codesignLog)
	if err != nil {
		t.Fatalf("read codesign call log: %v", err)
	}
	if !strings.Contains(string(calls), "-s - -f") {
		t.Fatalf("expected an ad-hoc signing call, got:\n%s", calls)
	}
	info, err := os.Stat(artifact)
	if err != nil {
		t.Fatalf("stat artifact: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("expected the signed artifact to be executable, got mode %v", info.Mode().Perm())
	}
	requireOnlyNotification(t, emits, "info", "Ad-hoc signed")
}

func requireOnlyNotification(t *testing.T, emits *capturedEmits, kind, contains string) {
	t.Helper()
	events := emits.events(appNotificationEvent)
	if len(events) != 1 {
		t.Fatalf("expected one notification, got %+v", events)
	}
	payload, ok := events[0].(appNotificationPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", events[0])
	}
	if payload.Kind != kind || !strings.Contains(payload.Message, contains) {
		t.Fatalf("unexpected notification: %+v", payload)
	}
}

// codesign exists only on macOS and so does the problem: on any other host the
// artifact is handed over exactly as it arrived, and nothing is said about it.
func TestRunOrchestratorOutputOnHostLeavesANonDarwinHostAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the codesign stub is a POSIX shell script")
	}
	home := withOrchestratorConfigHome(t)
	artifact := seedOrchestratorArtifact(t, "erun-darwin-arm64", machOArtifactBytes)
	t.Setenv("ERUN_HOST_OS_OVERRIDE", "linux")
	codesignLog := stubCodesign(t, home)

	emits := newCapturedEmits()
	app := &App{emitFn: emits.fn(), deps: erunUIDeps{launchHostArtifact: func(string, string) error { return nil }}}
	if err := app.RunOrchestratorOutputOnHost("erun", "erun-darwin-arm64"); err != nil {
		t.Fatalf("run: %v", err)
	}

	if _, err := os.Stat(codesignLog); !os.IsNotExist(err) {
		t.Fatalf("codesign must not run off darwin, stat err = %v", err)
	}
	info, err := os.Stat(artifact)
	if err != nil {
		t.Fatalf("stat artifact: %v", err)
	}
	if info.Mode().Perm()&0o111 != 0 {
		t.Fatalf("expected the artifact left untouched, got mode %v", info.Mode().Perm())
	}
	if got := emits.events(appNotificationEvent); len(got) != 0 {
		t.Fatalf("expected no notification off darwin, got %+v", got)
	}
}
