package eruncommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveSSHConfigAliasRemovesOnlyTheNamedBlock(t *testing.T) {
	existing := UpsertSSHConfigContent("",
		SSHHostEntry{Alias: "erun-team-dev", HostName: "127.0.0.1", Port: 17022, User: "erun"})
	existing = UpsertSSHConfigContent(existing,
		SSHHostEntry{Alias: "erun-team-keep", HostName: "127.0.0.1", Port: 17122, User: "erun"})
	existing = "Host github.com\n  User git\n\n" + existing

	updated, removed := RemoveSSHConfigAliasContent(existing, "erun-team-dev")
	if !removed {
		t.Fatalf("expected the erun-team-dev block to be removed:\n%s", updated)
	}
	if strings.Contains(updated, "erun-team-dev") {
		t.Errorf("removed alias still present:\n%s", updated)
	}
	for _, want := range []string{"Host github.com", "  User git", "Host erun-team-keep", "  Port 17122"} {
		if !strings.Contains(updated, want) {
			t.Errorf("removal dropped unrelated config %q:\n%s", want, updated)
		}
	}
}

func TestRemoveSSHConfigAliasKeepsSharedMultiAliasLine(t *testing.T) {
	existing := "Host erun-team-dev erun-team-keep\n  User erun\n"

	updated, removed := RemoveSSHConfigAliasContent(existing, "erun-team-dev")
	if removed {
		t.Fatalf("a Host line naming another alias must not be deleted:\n%s", updated)
	}
	if updated != existing {
		t.Errorf("unchanged content must be returned verbatim, got:\n%s", updated)
	}
}

func TestRemoveSSHConfigAliasIsIdempotent(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	path := filepath.Join(homeDir, ".ssh", "config")
	entry := SSHHostEntry{Alias: "erun-team-dev", HostName: "127.0.0.1", Port: 17022, User: "erun"}
	if err := UpsertSSHConfig(path, entry); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	removed, err := RemoveSSHConfigAlias(path, "erun-team-dev")
	if err != nil || !removed {
		t.Fatalf("first removal: removed=%v err=%v", removed, err)
	}
	again, err := RemoveSSHConfigAlias(path, "erun-team-dev")
	if err != nil || again {
		t.Fatalf("second removal must be a no-op: removed=%v err=%v", again, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if got := string(data); got != "" {
		t.Errorf("emptied config should not leave a blank line behind, got %q", got)
	}
}
