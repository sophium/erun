package podruntime

import (
	"os"
	"strconv"
	"testing"
)

// writeDoctorHelmStub points ERUN_HELM_BIN at a script that prints stdout and
// stderr and exits with the given code, so a test can drive RunDeployDiagnosis
// against the real, distinct shapes a live `helm status` invocation returns
// (plain text, not the `-o json` shape observe reads) without a live cluster.
func writeDoctorHelmStub(t *testing.T, stdout, stderr string, exitCode int) {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/helm-stub"
	script := "#!/bin/sh\n"
	if stdout != "" {
		script += "cat <<'ERUN_TEST_HELM_STDOUT'\n" + stdout + "\nERUN_TEST_HELM_STDOUT\n"
	}
	if stderr != "" {
		script += "cat <<'ERUN_TEST_HELM_STDERR' >&2\n" + stderr + "\nERUN_TEST_HELM_STDERR\n"
	}
	script += "exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write helm stub: %v", err)
	}
	t.Setenv("ERUN_HELM_BIN", path)
}
