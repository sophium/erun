package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenPortForwardLogBoundsAnOvergrownLog covers the growth direction: a
// port-forward log past the cap is rolled to its ".1" generation before the
// next forward appends, so the live file restarts and the pair stays bounded.
func TestOpenPortForwardLogBoundsAnOvergrownLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "local.log")
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(logPath, portForwardLogMaxBytes+1); err != nil {
		t.Fatal(err)
	}

	file, err := openPortForwardLog(logPath)
	if err != nil {
		t.Fatalf("open port-forward log: %v", err)
	}
	defer func() { _ = file.Close() }()

	info, err := os.Stat(logPath + ".1")
	if err != nil {
		t.Fatalf("over-cap log was not rotated to .1: %v", err)
	}
	if info.Size() != portForwardLogMaxBytes+1 {
		t.Fatalf("rotated generation size = %d, want %d", info.Size(), portForwardLogMaxBytes+1)
	}
	live, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if live.Size() != 0 {
		t.Fatalf("live log size = %d after rotation, want 0", live.Size())
	}
}

// TestOpenPortForwardLogKeepsAnUnderCapLog covers the other direction: the
// ordinary small log an operator opens the file for is appended to, not
// discarded.
func TestOpenPortForwardLogKeepsAnUnderCapLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "local.log")
	existing := "error: unable to forward port because pod is not running. Current status=Pending\n"
	if err := os.WriteFile(logPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	file, err := openPortForwardLog(logPath)
	if err != nil {
		t.Fatalf("open port-forward log: %v", err)
	}
	if _, err := file.WriteString("Forwarding from 127.0.0.1:17000 -> 17000\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), existing) {
		t.Fatalf("under-cap log lost earlier content: %q", got)
	}
	if _, err := os.Stat(logPath + ".1"); !os.IsNotExist(err) {
		t.Fatalf("under-cap log must not rotate: stat .1 err = %v", err)
	}
}
