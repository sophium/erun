package doctor

import (
	"os"
	"strconv"
	"testing"
)

// writeKubectlStub points ERUN_KUBECTL_BIN at a script that prints stderr and
// exits non-zero (or exits 0 with no output when stderr is empty), so a test
// can drive checkKubernetesDeploymentWithContext's classification without a
// real cluster.
func writeKubectlStub(t *testing.T, stderr string, exitCode int) {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/kubectl-stub"
	script := "#!/bin/sh\n"
	if stderr != "" {
		script += "cat <<'EOF' >&2\n" + stderr + "\nEOF\n"
	}
	script += "exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write kubectl stub: %v", err)
	}
	t.Setenv("ERUN_KUBECTL_BIN", path)
}
