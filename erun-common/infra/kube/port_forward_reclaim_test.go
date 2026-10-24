package kube

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// seedPortForwardLogForTest writes the log a forward appends to, and the
// rotated generation its size cap leaves beside it, so a test can tell a
// removal that clears the whole record from one that clears only the state
// file.
func seedPortForwardLogForTest(t *testing.T, kind, tenant, environment string) string {
	t.Helper()
	logPath, err := PortForwardLogPath(kind, tenant, environment)
	if err != nil {
		t.Fatalf("portForwardLogPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("MkdirAll log dir: %v", err)
	}
	for _, path := range []string{logPath, logPath + ".1"} {
		if err := os.WriteFile(path, []byte("Handling connection for 26100\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return logPath
}

func requireMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected %s to be removed, stat err: %v", path, err)
	}
}

func requirePresent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected %s to survive, stat err: %v", path, err)
	}
}

// TestRemovePortForwardRecordKeepsTheLogOfAForwardThatIsStillRunning is the
// other half of the same decision: a forward holds the file it opened at start
// as its own stdout/stderr, so a log a still-running forward is writing to is
// the one thing a reclaim must leave alone. Unlinking it would take away the
// forward an operator is still reading while its writer kept consuming disk
// invisibly. The state file still goes: the local port it names is freed and
// reissued, so a record outliving its environment resolves to somebody else's
// forward.
func TestRemovePortForwardRecordKeepsTheLogOfAForwardThatIsStillRunning(t *testing.T) {
	redirectConfigHomeForTest(t)
	tenant, environment := "team", "dev"
	seedPortForwardStateFileForTest(t, tenant, environment, 26100)
	logPath := seedPortForwardLogForTest(t, "mcp", tenant, environment)
	statePath, err := PortForwardStatePath("mcp", tenant, environment)
	if err != nil {
		t.Fatalf("PortForwardStatePath: %v", err)
	}
	writePortForwardStateProcessID(t, statePath, os.Getpid())

	if err := RemovePortForwardRecord(core.Context{}, "mcp", tenant, environment); err != nil {
		t.Fatalf("remove record: %v", err)
	}

	requirePresent(t, logPath)
	requirePresent(t, logPath+".1")
	requireMissing(t, statePath)
}

// TestRemovePortForwardRecordKeepsALogSomeProcessStillHolds covers the case no
// record can answer: a forward whose environment was deleted keeps its log
// while it is still running, and the state file it would have been found
// through goes with the environment, so a later reclaim looking at the tree
// has nothing left to ask. Without the open-file check the delete path would
// keep a log the sweep then removed -- taking away what an operator is reading
// while the writer kept consuming disk through the unlinked file.
func TestRemovePortForwardRecordKeepsALogSomeProcessStillHolds(t *testing.T) {
	redirectConfigHomeForTest(t)
	tenant, environment := "team", "dev"
	// No processId: the record cannot answer the question on its own, which is
	// the whole point.
	seedPortForwardStateFileForTest(t, tenant, environment, 26100)
	logPath := seedPortForwardLogForTest(t, "mcp", tenant, environment)

	restore := portForwardLogHeldByProcess
	portForwardLogHeldByProcess = func(string) bool { return true }
	t.Cleanup(func() { portForwardLogHeldByProcess = restore })

	if err := RemovePortForwardRecord(core.Context{}, "mcp", tenant, environment); err != nil {
		t.Fatalf("remove record: %v", err)
	}

	requirePresent(t, logPath)
	requirePresent(t, logPath+".1")
}

// writePortForwardStateProcessID rewrites a seeded record with the process id
// a live forward would have recorded.
func writePortForwardStateProcessID(t *testing.T, statePath string, processID int) {
	t.Helper()
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var state PortForwardState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	state.ProcessID = processID
	updated, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(statePath, updated, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
}

// TestReclaimOrphanedPortForwardRecordsRemovesEnvironmentsThatNoLongerExist
// covers the residue a delete can no longer reach: logs belonging to tenants
// and environments the config store does not know, including files whose state
// file was already reclaimed and only the log is left.
func TestReclaimOrphanedPortForwardRecordsRemovesEnvironmentsThatNoLongerExist(t *testing.T) {
	redirectConfigHomeForTest(t)
	if err := config.SaveERunConfig(config.ERunConfig{}); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	if err := config.SaveTenantConfig(config.TenantConfig{Name: "acme"}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := config.SaveEnvConfig("acme", config.EnvConfig{Name: "dev"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	liveState, err := PortForwardStatePath("mcp", "acme", "dev")
	if err != nil {
		t.Fatalf("PortForwardStatePath: %v", err)
	}
	seedPortForwardStateFileForTest(t, "acme", "dev", 26100)
	liveLog := seedPortForwardLogForTest(t, "mcp", "acme", "dev")

	// A configured tenant's deleted environment: the state file was reclaimed,
	// only the log is left.
	orphanLog := seedPortForwardLogForTest(t, "mcp", "acme", "gone")

	// A tenant that is not configured at all, in another kind.
	retiredLog := seedPortForwardLogForTest(t, "sshd", "sapiens", "poc")

	if err := ReclaimOrphanedPortForwardRecords(core.Context{}); err != nil {
		t.Fatalf("reclaim: %v", err)
	}

	requirePresent(t, liveState)
	requirePresent(t, liveLog)
	requireMissing(t, orphanLog)
	requireMissing(t, orphanLog+".1")
	requireMissing(t, retiredLog)
	if _, err := os.Stat(filepath.Dir(retiredLog)); !os.IsNotExist(err) {
		t.Errorf("expected the removed tenant's directory to go with its last record, stat err: %v", err)
	}
}

// TestReclaimOrphanedPortForwardRecordsLeavesTheTreeAloneWhenTheConfigIsUnreadable
// pins the answer that must never be guessed: a config tree that is not there
// is not the same answer as a config tree with no tenants in it, and treating
// the two alike would remove every forward log on the host.
func TestReclaimOrphanedPortForwardRecordsLeavesTheTreeAloneWhenTheConfigIsUnreadable(t *testing.T) {
	redirectConfigHomeForTest(t)
	// A forward tree with no config tree beside it, which is what a process
	// resolving a different config home sees.
	retiredLog := seedPortForwardLogForTest(t, "mcp", "sapiens", "poc")

	if err := ReclaimOrphanedPortForwardRecords(core.Context{}); err != nil {
		t.Fatalf("reclaim: %v", err)
	}

	requirePresent(t, retiredLog)
}
