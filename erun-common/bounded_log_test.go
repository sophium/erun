package eruncommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testBoundedLogMaxBytes = 1024

func TestOpenBoundedAppendLogRotatesAnOvergrownLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", testBoundedLogMaxBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := OpenBoundedAppendLog(path, testBoundedLogMaxBytes, 0o644)
	if err != nil {
		t.Fatalf("open bounded append log: %v", err)
	}
	defer func() { _ = file.Close() }()

	rotated, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("over-cap log was not rotated to .1: %v", err)
	}
	if len(rotated) != testBoundedLogMaxBytes+1 {
		t.Fatalf("rotated generation size = %d, want %d", len(rotated), testBoundedLogMaxBytes+1)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("live log size = %d after rotation, want 0", info.Size())
	}
}

func TestOpenBoundedAppendLogLeavesAnUnderCapLogAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.log")
	existing := "Forwarding from 127.0.0.1:17000 -> 17000\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := OpenBoundedAppendLog(path, testBoundedLogMaxBytes, 0o644)
	if err != nil {
		t.Fatalf("open bounded append log: %v", err)
	}
	if _, err := file.WriteString("Handling connection for 17000\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), existing) {
		t.Fatalf("under-cap log lost earlier content: %q", got)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("under-cap log must not rotate: stat .1 err = %v", err)
	}
}
