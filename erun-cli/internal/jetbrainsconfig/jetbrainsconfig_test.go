package jetbrainsconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStableConfigIDDeterministic(t *testing.T) {
	first := StableConfigID("erun-erun-remote")
	second := StableConfigID("erun-erun-remote")
	if first != second {
		t.Fatalf("expected deterministic config ID, got %q and %q", first, second)
	}
	if len(first) != 36 {
		t.Fatalf("unexpected config ID format: %q", first)
	}
}

func TestUpsertOptionsFilesCreatesJetBrainsSSHProjectFiles(t *testing.T) {
	optionsDir := t.TempDir()
	entry := ProjectEntry{
		ConfigID:       StableConfigID("erun-erun-remote"),
		HostAlias:      "erun-erun-remote",
		User:           "erun",
		IdentityFile:   "/Users/test/.ssh/id_ed25519",
		ProjectPath:    "/home/erun/git/erun",
		Port:           62222,
		ProductCode:    "IU",
		TimestampMilli: 1776697818596,
	}

	if err := UpsertOptionsFiles(optionsDir, entry); err != nil {
		t.Fatalf("UpsertOptionsFiles failed: %v", err)
	}

	assertFileContains(t, filepath.Join(optionsDir, "sshConfigs.xml"),
		`host="erun-erun-remote"`,
		`id="`+entry.ConfigID+`"`,
		`keyPath="/Users/test/.ssh/id_ed25519"`,
		`port="62222"`,
		`username="erun"`,
	)
	assertFileContains(t, filepath.Join(optionsDir, "sshRecentConnectionsHost.xml"),
		`<option value="`+entry.ConfigID+`"></option>`,
	)
	assertFileContains(t, filepath.Join(optionsDir, "sshRecentConnections.v2.xml"),
		`<option name="configId" value="`+entry.ConfigID+`"></option>`,
		`<option name="projectPath" value="/home/erun/git/erun"></option>`,
		`<option name="productCode" value="IU"></option>`,
	)
}

func TestUpsertOptionsFilesUpdatesExistingEntriesWithoutDuplicates(t *testing.T) {
	optionsDir := t.TempDir()
	first := ProjectEntry{
		ConfigID:       StableConfigID("erun-erun-remote"),
		HostAlias:      "erun-erun-remote",
		User:           "erun",
		IdentityFile:   "/Users/test/.ssh/id_ed25519",
		ProjectPath:    "/home/erun/git/erun",
		Port:           62222,
		ProductCode:    "IU",
		TimestampMilli: 1,
	}
	second := first
	second.ProjectPath = "/home/erun/git/erun-next"
	second.TimestampMilli = 2

	if err := UpsertOptionsFiles(optionsDir, first); err != nil {
		t.Fatalf("first UpsertOptionsFiles failed: %v", err)
	}
	if err := UpsertOptionsFiles(optionsDir, second); err != nil {
		t.Fatalf("second UpsertOptionsFiles failed: %v", err)
	}

	configs := readFile(t, filepath.Join(optionsDir, "sshConfigs.xml"))
	if strings.Count(configs, `host="erun-erun-remote"`) != 1 {
		t.Fatalf("expected one sshConfig entry, got:\n%s", configs)
	}

	recent := readFile(t, filepath.Join(optionsDir, "sshRecentConnections.v2.xml"))
	if strings.Count(recent, `<option name="configId" value="`+first.ConfigID+`"></option>`) != 1 {
		t.Fatalf("expected one configId entry, got:\n%s", recent)
	}
	for _, want := range []string{
		`<option name="projectPath" value="/home/erun/git/erun"></option>`,
		`<option name="projectPath" value="/home/erun/git/erun-next"></option>`,
	} {
		if !strings.Contains(recent, want) {
			t.Fatalf("expected recent projects to contain %q, got:\n%s", want, recent)
		}
	}
}

func assertFileContains(t *testing.T, path string, wants ...string) {
	t.Helper()
	data := readFile(t, path)
	for _, want := range wants {
		if !strings.Contains(data, want) {
			t.Fatalf("expected %s to contain %q, got:\n%s", path, want, data)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
