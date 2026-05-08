package cmd

import (
	"path/filepath"
	"testing"
)

// The dry-run + launcher dispatch behavior is covered by the integration
// suite (erun-integration/app_test.go). The cases below stay as unit tests
// because they exercise platform-specific exec.Cmd construction — the
// integration harness only runs on the host platform, so the darwin
// branches below cannot be reached from a dry-run scenario.

func TestNewAppProcessCommandSetsDarwinProcessName(t *testing.T) {
	cmd := newAppProcessCommand("darwin", "/tmp/erun-app", nil)

	if got, want := cmd.Path, "/tmp/erun-app"; got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
	if got, want := cmd.Args[0], "ERun"; got != want {
		t.Fatalf("Args[0] = %q, want %q", got, want)
	}
}

func TestNewAppProcessCommandOpensDarwinBundle(t *testing.T) {
	cmd := newAppProcessCommand("darwin", "/tmp/ERun.app", []string{"--flag"})

	if got, want := filepath.Base(cmd.Path), "open"; got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
	wantArgs := []string{"open", "-n", "/tmp/ERun.app", "--args", "--flag"}
	if len(cmd.Args) != len(wantArgs) {
		t.Fatalf("Args = %+v, want %+v", cmd.Args, wantArgs)
	}
	for index := range wantArgs {
		if cmd.Args[index] != wantArgs[index] {
			t.Fatalf("Args[%d] = %q, want %q", index, cmd.Args[index], wantArgs[index])
		}
	}
}

func TestNewAppProcessCommandKeepsExecutableNameOutsideDarwin(t *testing.T) {
	cmd := newAppProcessCommand("linux", "/tmp/erun-app", []string{"--flag"})

	if got, want := cmd.Args[0], "/tmp/erun-app"; got != want {
		t.Fatalf("Args[0] = %q, want %q", got, want)
	}
	if got, want := cmd.Args[1], "--flag"; got != want {
		t.Fatalf("Args[1] = %q, want %q", got, want)
	}
}
