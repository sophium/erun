package kube

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// pinLegacyUserConfigLayoutForTest makes os.UserConfigDir differ from the config home on every host.
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
	return filepath.Join(cacheDir, config.ConfigRoot, kind, tenant, environment+".json")
}

// Resolving the path is a read, so a dry run must not move the legacy record.
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

// A delete must also remove a record left at a legacy location, or a later
// same-named environment would inherit it.
func TestRemovePortForwardRecordRemovesLegacyLocationRecords(t *testing.T) {
	legacyRoot := pinLegacyUserConfigLayoutForTest(t)
	userConfigState := filepath.Join(legacyRoot, "mcp", "team", "dev.json")
	userConfigLog := seedLegacyPortForwardRecordForTest(t, userConfigState)
	cacheState := legacyCacheStatePathForTest(t, "mcp", "team", "dev")
	cacheLog := seedLegacyPortForwardRecordForTest(t, cacheState)

	traces := &strings.Builder{}
	dryRun := core.Context{DryRun: true, Logger: core.NewLoggerWithWriters(core.VerbosityInfo, io.Discard, io.Discard).WithTraceSink(traces)}
	if err := RemovePortForwardRecord(dryRun, "mcp", "team", "dev"); err != nil {
		t.Fatalf("dry-run remove: %v", err)
	}
	for _, path := range []string{userConfigState, userConfigLog, userConfigLog + ".1", cacheState, cacheLog, cacheLog + ".1"} {
		if !strings.Contains(traces.String(), path) {
			t.Errorf("expected the dry-run plan to name %s, got:\n%s", path, traces.String())
		}
		requirePresent(t, path)
	}

	if err := RemovePortForwardRecord(core.Context{}, "mcp", "team", "dev"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	for _, path := range []string{userConfigState, userConfigLog, userConfigLog + ".1", cacheState, cacheLog, cacheLog + ".1"} {
		requireMissing(t, path)
	}
}

func TestRemovePortForwardRecordKeepsTheLegacyLogOfAForwardThatIsStillRunning(t *testing.T) {
	legacyRoot := pinLegacyUserConfigLayoutForTest(t)
	statePath := filepath.Join(legacyRoot, "mcp", "team", "dev.json")
	logPath := seedLegacyPortForwardRecordForTest(t, statePath)
	writePortForwardStateProcessID(t, statePath, os.Getpid())

	if err := RemovePortForwardRecord(core.Context{}, "mcp", "team", "dev"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	requirePresent(t, logPath)
	requirePresent(t, logPath+".1")
	requireMissing(t, statePath)
}

func TestReclaimOrphanedPortForwardRecordsWalksTheUserConfigDirTree(t *testing.T) {
	legacyRoot := pinLegacyUserConfigLayoutForTest(t)
	if err := config.SaveERunConfig(config.ERunConfig{}); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	if err := config.SaveTenantConfig(config.TenantConfig{Name: "acme"}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := config.SaveEnvConfig("acme", config.EnvConfig{Name: "dev"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	liveState := filepath.Join(legacyRoot, "mcp", "acme", "dev.json")
	liveLog := seedLegacyPortForwardRecordForTest(t, liveState)
	orphanState := filepath.Join(legacyRoot, "sshd", "retired", "poc.json")
	orphanLog := seedLegacyPortForwardRecordForTest(t, orphanState)

	if err := ReclaimOrphanedPortForwardRecords(core.Context{}); err != nil {
		t.Fatalf("reclaim: %v", err)
	}

	requirePresent(t, liveState)
	requirePresent(t, liveLog)
	requireMissing(t, orphanState)
	requireMissing(t, orphanLog)
	requireMissing(t, orphanLog+".1")
	requireMissing(t, filepath.Dir(orphanState))
}
