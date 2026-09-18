package eruncommon

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestRemovePortForwardStateFilesRemovesARecordUnderTheLegacySpelling pins that
// deleting an environment clears the legacy spelling too. On a case-sensitive
// volume that spelling is a separate file rather than the same one, and the CLI
// reads a forward's state without the configured-environment guard the shared
// reader applies -- so a record left behind would resolve as a live forward for
// the environment that was just deleted, and the local port it names would be
// reissued to whichever environment is created next.
func TestRemovePortForwardStateFilesRemovesARecordUnderTheLegacySpelling(t *testing.T) {
	redirectConfigHomeForTest(t)
	tenant, environment := "acme", "dev"

	for _, kind := range portForwardStateKinds {
		for _, path := range portForwardStateLocationsForTest(t, kind, tenant, environment) {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(path, []byte(`{"localPort":1}`), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
		}
	}

	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, io.Discard, io.Discard)}
	if err := removePortForwardStateFiles(ctx, tenant, environment); err != nil {
		t.Fatalf("removePortForwardStateFiles: %v", err)
	}

	for _, kind := range portForwardStateKinds {
		for _, path := range portForwardStateLocationsForTest(t, kind, tenant, environment) {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("expected the deleted environment's record at %q to be removed, got stat err=%v", path, err)
			}
		}
	}
}

// portForwardStateLocationsForTest is the canonical location for one kind of
// forward plus the legacy lowercase spelling, including the case-insensitive
// volume where the two are one file.
func portForwardStateLocationsForTest(t *testing.T, kind, tenant, environment string) []string {
	t.Helper()
	canonical, err := PortForwardStatePath(kind, tenant, environment)
	if err != nil {
		t.Fatalf("PortForwardStatePath: %v", err)
	}
	legacy, err := LegacyPortForwardStatePath(kind, tenant, environment)
	if err != nil {
		t.Fatalf("LegacyPortForwardStatePath: %v", err)
	}
	return []string{canonical, legacy}
}
