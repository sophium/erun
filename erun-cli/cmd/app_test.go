package cmd

import (
	"bytes"
	"io"
	"testing"
)

func TestAppCommandLaunchesDesktopApp(t *testing.T) {
	var called bool
	cmd := newAppCmd(func(stdout, stderr io.Writer, args []string) error {
		called = true
		if len(args) != 0 {
			t.Fatalf("expected no app args, got %+v", args)
		}
		return nil
	})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !called {
		t.Fatal("expected launcher to be called")
	}
}

func TestAppCommandDryRunSkipsLaunch(t *testing.T) {
	var called bool
	cmd := newAppCmd(func(stdout, stderr io.Writer, args []string) error {
		called = true
		return nil
	})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if called {
		t.Fatal("expected launcher not to be called in dry-run mode")
	}
}

func TestNewAppProcessCommandSetsDarwinProcessName(t *testing.T) {
	cmd := newAppProcessCommand("darwin", "/tmp/erun-app", nil)

	if got, want := cmd.Path, "/tmp/erun-app"; got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
	if got, want := cmd.Args[0], "ERun"; got != want {
		t.Fatalf("Args[0] = %q, want %q", got, want)
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
