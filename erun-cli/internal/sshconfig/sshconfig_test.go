package sshconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpsertConfigContentAddsEntry(t *testing.T) {
	got := UpsertConfigContent("", HostEntry{
		Alias:        "erun-tenant-a-remote",
		HostKeyAlias: "erun-tenant-a-remote",
		HostName:     "127.0.0.1",
		Port:         62222,
		User:         "erun",
		IdentityFile: "/tmp/id_ed25519",
	})

	for _, want := range []string{
		"Host erun-tenant-a-remote",
		"HostName 127.0.0.1",
		"Port 62222",
		"User erun",
		"HostKeyAlias erun-tenant-a-remote",
		"IdentityFile /tmp/id_ed25519",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected config to contain %q, got:\n%s", want, got)
		}
	}
}

func TestUpsertConfigContentReplacesExistingHostBlock(t *testing.T) {
	existing := "Host github.com\n  HostName github.com\n\nHost erun-tenant-a-remote\n  HostName old\n  Port 22\n\nHost other\n  HostName other\n"
	got := UpsertConfigContent(existing, HostEntry{
		Alias:        "erun-tenant-a-remote",
		HostKeyAlias: "erun-tenant-a-remote",
		HostName:     "127.0.0.1",
		Port:         62222,
		User:         "erun",
	})

	if strings.Contains(got, "HostName old") {
		t.Fatalf("expected old host block to be replaced, got:\n%s", got)
	}
	for _, want := range []string{
		"Host github.com",
		"Host other",
		"Host erun-tenant-a-remote",
		"HostName 127.0.0.1",
		"Port 62222",
		"User erun",
		"HostKeyAlias erun-tenant-a-remote",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected config to contain %q, got:\n%s", want, got)
		}
	}
}

func TestUpsertDefaultConfigUsesUserHomeDir(t *testing.T) {
	homeDir := t.TempDir()
	prev := userHomeDir
	userHomeDir = func() (string, error) { return homeDir, nil }
	t.Cleanup(func() { userHomeDir = prev })

	path, err := UpsertDefaultConfig(HostEntry{
		Alias:        "erun-tenant-a-remote",
		HostKeyAlias: "erun-tenant-a-remote",
		HostName:     "127.0.0.1",
		Port:         62222,
		User:         "erun",
	})
	if err != nil {
		t.Fatalf("UpsertDefaultConfig failed: %v", err)
	}
	if path != filepath.Join(homeDir, ".ssh", "config") {
		t.Fatalf("unexpected config path: %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected ssh config to be written: %v", err)
	}
}
