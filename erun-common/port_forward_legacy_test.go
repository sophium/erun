package eruncommon

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pinLegacyUserConfigLayoutForTest makes the tree os.UserConfigDir named differ
// from the config home on every host: the macOS branch is keyed on the detected
// host, and the config home redirect points XDG_CONFIG_HOME away from
// ~/Library/Application Support.
func pinLegacyUserConfigLayoutForTest(t *testing.T) string {
	t.Helper()
	redirectConfigHomeForTest(t)
	t.Setenv("ERUN_HOST_OS_OVERRIDE", "darwin")
	root, ok := legacyPortForwardRoot()
	if !ok {
		t.Fatal("expected the os.UserConfigDir tree to differ from the config home")
	}
	return root
}

func seedLegacyPortForwardRecordForTest(t *testing.T, statePath string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(statePath, []byte(`{"localPort":26100}`), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	logPath := PortForwardLogPathForState(statePath)
	for _, path := range []string{logPath, logPath + ".1"} {
		if err := os.WriteFile(path, []byte("Handling connection for 26100\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return logPath
}

func legacyCacheStatePathForTest(t *testing.T, kind, tenant, environment string) string {
	t.Helper()
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("UserCacheDir: %v", err)
	}
	return filepath.Join(cacheDir, configRoot, kind, tenant, environment+".json")
}

// A dry run resolving the state path is a read every forward setup makes before
// its own dry-run check, so it must not move the operator's legacy record.
func TestResolvePortForwardStatePathDryRunDoesNotMigrateLegacyState(t *testing.T) {
	redirectConfigHomeForTest(t)
	legacyPath := legacyCacheStatePathForTest(t, "api", "acme", "dev")
	seedLegacyPortForwardRecordForTest(t, legacyPath)

	path, err := ResolvePortForwardStatePath("api", "acme", "dev", true)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if path != legacyPath {
		t.Fatalf("expected the dry run to report the still-legacy path %q, got %q", legacyPath, path)
	}
	requirePresent(t, legacyPath)
	statePath, err := PortForwardStatePath("api", "acme", "dev")
	if err != nil {
		t.Fatalf("PortForwardStatePath: %v", err)
	}
	requireMissing(t, statePath)
}

// The cache-dir record, its log and the log's rotated generation all move into
// the canonical tree, so nothing of the forward is stranded at the old place.
func TestResolvePortForwardStatePathMigratesTheCacheDirRecordWithItsLogs(t *testing.T) {
	redirectConfigHomeForTest(t)
	legacyPath := legacyCacheStatePathForTest(t, "api", "acme", "dev")
	legacyLog := seedLegacyPortForwardRecordForTest(t, legacyPath)

	path, err := ResolvePortForwardStatePath("api", "acme", "dev", false)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	statePath, err := PortForwardStatePath("api", "acme", "dev")
	if err != nil {
		t.Fatalf("PortForwardStatePath: %v", err)
	}
	if path != statePath {
		t.Fatalf("expected the migrated path %q, got %q", statePath, path)
	}
	newLog := PortForwardLogPathForState(statePath)
	requirePresent(t, statePath)
	requirePresent(t, newLog)
	requirePresent(t, newLog+".1")
	requireMissing(t, legacyPath)
	requireMissing(t, legacyLog)
	requireMissing(t, legacyLog+".1")
}

// A record written while the tree followed os.UserConfigDir is moved into the
// config tree, so a forward running across the upgrade is still found instead
// of being started a second time.
func TestResolvePortForwardStatePathMigratesTheUserConfigDirRecord(t *testing.T) {
	legacyRoot := pinLegacyUserConfigLayoutForTest(t)
	legacyPath := filepath.Join(legacyRoot, "mcp", "acme", "dev.json")
	seedLegacyPortForwardRecordForTest(t, legacyPath)

	path, err := ResolvePortForwardStatePath("mcp", "acme", "dev", false)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	statePath, err := PortForwardStatePath("mcp", "acme", "dev")
	if err != nil {
		t.Fatalf("PortForwardStatePath: %v", err)
	}
	if path != statePath {
		t.Fatalf("expected the migrated path %q, got %q", statePath, path)
	}
	requirePresent(t, statePath)
	requireMissing(t, legacyPath)
}

// The reproduction: a record nothing reopened since the upgrade still sits at a
// legacy location, and a delete that cleared only the canonical tree left it
// there for the next open to migrate into an environment of the same name
// created later. Delete must remove it, and its logs, and list it in the plan.
func TestRemovePortForwardRecordRemovesLegacyLocationRecords(t *testing.T) {
	legacyRoot := pinLegacyUserConfigLayoutForTest(t)
	userConfigState := filepath.Join(legacyRoot, "mcp", "team", "dev.json")
	userConfigLog := seedLegacyPortForwardRecordForTest(t, userConfigState)
	cacheState := legacyCacheStatePathForTest(t, "mcp", "team", "dev")
	cacheLog := seedLegacyPortForwardRecordForTest(t, cacheState)

	traces := &strings.Builder{}
	dryRun := Context{DryRun: true, Logger: NewLoggerWithWriters(VerbosityInfo, io.Discard, io.Discard).WithTraceSink(traces)}
	if err := RemovePortForwardRecord(dryRun, "mcp", "team", "dev"); err != nil {
		t.Fatalf("dry-run remove: %v", err)
	}
	for _, path := range []string{userConfigState, userConfigLog, userConfigLog + ".1", cacheState, cacheLog, cacheLog + ".1"} {
		if !strings.Contains(traces.String(), path) {
			t.Errorf("expected the dry-run plan to name %s, got:\n%s", path, traces.String())
		}
		requirePresent(t, path)
	}

	if err := RemovePortForwardRecord(Context{}, "mcp", "team", "dev"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	for _, path := range []string{userConfigState, userConfigLog, userConfigLog + ".1", cacheState, cacheLog, cacheLog + ".1"} {
		requireMissing(t, path)
	}
}

// A legacy record's log follows the same keep rule as the canonical one: a
// forward started before the upgrade and still running writes to it.
func TestRemovePortForwardRecordKeepsTheLegacyLogOfAForwardThatIsStillRunning(t *testing.T) {
	legacyRoot := pinLegacyUserConfigLayoutForTest(t)
	statePath := filepath.Join(legacyRoot, "mcp", "team", "dev.json")
	logPath := seedLegacyPortForwardRecordForTest(t, statePath)
	writePortForwardStateProcessID(t, statePath, os.Getpid())

	if err := RemovePortForwardRecord(Context{}, "mcp", "team", "dev"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	requirePresent(t, logPath)
	requirePresent(t, logPath+".1")
	requireMissing(t, statePath)
}

// The sweep walks the os.UserConfigDir tree as well: a deleted environment's
// record there is never opened again, so migration never reaches it either.
func TestReclaimOrphanedPortForwardRecordsWalksTheUserConfigDirTree(t *testing.T) {
	legacyRoot := pinLegacyUserConfigLayoutForTest(t)
	if err := SaveERunConfig(ERunConfig{}); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	if err := SaveTenantConfig(TenantConfig{Name: "acme"}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := SaveEnvConfig("acme", EnvConfig{Name: "dev"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	liveState := filepath.Join(legacyRoot, "mcp", "acme", "dev.json")
	liveLog := seedLegacyPortForwardRecordForTest(t, liveState)
	orphanState := filepath.Join(legacyRoot, "sshd", "retired", "poc.json")
	orphanLog := seedLegacyPortForwardRecordForTest(t, orphanState)

	if err := ReclaimOrphanedPortForwardRecords(Context{}); err != nil {
		t.Fatalf("reclaim: %v", err)
	}

	requirePresent(t, liveState)
	requirePresent(t, liveLog)
	requireMissing(t, orphanState)
	requireMissing(t, orphanLog)
	requireMissing(t, orphanLog+".1")
	requireMissing(t, filepath.Dir(orphanState))
}
