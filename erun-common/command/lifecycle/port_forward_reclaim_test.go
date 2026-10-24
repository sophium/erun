package lifecycle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
)

// seedPortForwardLogForTest writes the log a forward appends to, and the
// rotated generation its size cap leaves beside it, so a test can tell a
// removal that clears the whole record from one that clears only the state
// file.
func seedPortForwardLogForTest(t *testing.T, kind, tenant, environment string) string {
	t.Helper()
	logPath, err := kube.PortForwardLogPath(kind, tenant, environment)
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

// TestRunDeleteEnvironmentRemovesTheForwardLogBesideTheStateFile is the
// reproduction: a delete reclaimed the environment's port-forward state file
// but stranded the log next to it, so the forward tree kept records -- and
// whole tenant directories -- for environments that no longer existed, which
// nothing would ever open, restart, or rotate again.
func TestRunDeleteEnvironmentRemovesTheForwardLogBesideTheStateFile(t *testing.T) {
	redirectConfigHomeForTest(t)
	tenant, environment := "team", "dev"
	seedPortForwardStateFileForTest(t, tenant, environment, 26100)
	logPath := seedPortForwardLogForTest(t, "mcp", tenant, environment)
	statePath, err := kube.PortForwardStatePath("mcp", tenant, environment)
	if err != nil {
		t.Fatalf("PortForwardStatePath: %v", err)
	}

	store := &deleteSSHConfigStore{envs: map[string][]config.EnvConfig{tenant: {{Name: environment}}}}
	if _, err := RunDeleteEnvironment(core.Context{}, DeleteEnvironmentParams{
		Tenant: tenant, Environment: environment,
	}, store, nil); err != nil {
		t.Fatalf("delete: %v", err)
	}

	requireMissing(t, statePath)
	requireMissing(t, logPath)
	requireMissing(t, logPath+".1")
}
