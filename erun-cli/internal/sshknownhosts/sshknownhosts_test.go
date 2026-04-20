package sshknownhosts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpsertKnownHostsContentReplacesAliasAndHostEntries(t *testing.T) {
	existing := "github.com ssh-ed25519 AAAA\n[127.0.0.1]:62222 ssh-ed25519 OLD\nerun-tenant-a-remote ssh-ed25519 OLDALIAS\n"
	got := upsertKnownHostsContent(existing, "erun-tenant-a-remote", "[127.0.0.1]:62222", []string{
		"[127.0.0.1]:62222 ssh-ed25519 NEW",
	})

	if strings.Contains(got, "OLD") {
		t.Fatalf("expected old host entry to be removed, got:\n%s", got)
	}
	for _, want := range []string{
		"github.com ssh-ed25519 AAAA",
		"[127.0.0.1]:62222 ssh-ed25519 NEW",
		"erun-tenant-a-remote ssh-ed25519 NEW",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected known_hosts to contain %q, got:\n%s", want, got)
		}
	}
}

func TestUpsertDefaultKnownHostUsesUserHomeDir(t *testing.T) {
	homeDir := t.TempDir()
	binDir := t.TempDir()
	prevHome := userHomeDir
	userHomeDir = func() (string, error) { return homeDir, nil }
	t.Cleanup(func() { userHomeDir = prevHome })

	keyscanPath := filepath.Join(binDir, "ssh-keyscan")
	if err := os.WriteFile(keyscanPath, []byte(`#!/bin/sh
echo "[127.0.0.1]:62222 ssh-ed25519 AAAATEST"
`), 0o755); err != nil {
		t.Fatalf("write ssh-keyscan stub: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	path, err := UpsertDefaultKnownHost("erun-tenant-a-remote", "127.0.0.1", 62222)
	if err != nil {
		t.Fatalf("UpsertDefaultKnownHost failed: %v", err)
	}
	if path != filepath.Join(homeDir, ".ssh", "known_hosts") {
		t.Fatalf("unexpected known_hosts path: %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	for _, want := range []string{
		"[127.0.0.1]:62222 ssh-ed25519 AAAATEST",
		"erun-tenant-a-remote ssh-ed25519 AAAATEST",
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("unexpected known_hosts content:\n%s", data)
		}
	}
}

func TestUpsertDefaultKnownHostAcceptsScannedKeyOnNonZeroExit(t *testing.T) {
	homeDir := t.TempDir()
	binDir := t.TempDir()
	prevHome := userHomeDir
	userHomeDir = func() (string, error) { return homeDir, nil }
	t.Cleanup(func() { userHomeDir = prevHome })

	keyscanPath := filepath.Join(binDir, "ssh-keyscan")
	if err := os.WriteFile(keyscanPath, []byte(`#!/bin/sh
echo "[127.0.0.1]:62222 ssh-ed25519 AAAATEST"
echo "write (127.0.0.1): Broken pipe" >&2
exit 1
`), 0o755); err != nil {
		t.Fatalf("write ssh-keyscan stub: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	path, err := UpsertDefaultKnownHost("erun-tenant-a-remote", "127.0.0.1", 62222)
	if err != nil {
		t.Fatalf("UpsertDefaultKnownHost failed: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	if !strings.Contains(string(data), "erun-tenant-a-remote ssh-ed25519 AAAATEST") {
		t.Fatalf("unexpected known_hosts content:\n%s", data)
	}
}
