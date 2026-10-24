package kube

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// seedPortForwardLogForTest writes a log and its rotated generation.
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

// A running forward still writes to its log, so the log stays; the state file goes
// because its port gets reissued.
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

// The state file is gone, so only the open-file check can protect a running forward's log.
func TestRemovePortForwardRecordKeepsALogSomeProcessStillHolds(t *testing.T) {
	redirectConfigHomeForTest(t)
	tenant, environment := "team", "dev"
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

func writePortForwardStateProcessID(t *testing.T, statePath string, processID int) {
	t.Helper()
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var state model.PortForwardState
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

func TestReclaimOrphanedPortForwardRecordsRemovesEnvironmentsThatNoLongerExist(t *testing.T) {
	redirectConfigHomeForTest(t)
	if err := config.SaveERunConfig(config.ERunConfig{}); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	if err := config.SaveTenantConfig(model.TenantConfig{Name: "acme"}); err != nil {
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

	orphanLog := seedPortForwardLogForTest(t, "mcp", "acme", "gone")

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

// A missing config tree is not an empty one; treating them alike would remove every forward log.
func TestReclaimOrphanedPortForwardRecordsLeavesTheTreeAloneWhenTheConfigIsUnreadable(t *testing.T) {
	redirectConfigHomeForTest(t)
	retiredLog := seedPortForwardLogForTest(t, "mcp", "sapiens", "poc")

	if err := ReclaimOrphanedPortForwardRecords(core.Context{}); err != nil {
		t.Fatalf("reclaim: %v", err)
	}

	requirePresent(t, retiredLog)
}
