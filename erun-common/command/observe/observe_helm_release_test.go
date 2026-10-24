package observe

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/orchestration/clusterread"
)

// writeHelmObserveStub points ERUN_HELM_BIN at a script that dispatches on
// the subcommand (status vs list), so a test can drive
// fetchObservedHelmRelease against the real, distinct shapes `helm status
// -o json` and `helm list -o json` return without a live cluster.
func writeHelmObserveStub(t *testing.T, statusStdout, listStdout string, exitCode int, stderr string) {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/helm-stub"
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  status)\n" +
		"    cat <<'ERUN_TEST_HELM_STATUS'\n" + statusStdout + "\nERUN_TEST_HELM_STATUS\n" +
		"    ;;\n" +
		"  list)\n" +
		"    cat <<'ERUN_TEST_HELM_LIST'\n" + listStdout + "\nERUN_TEST_HELM_LIST\n" +
		"    ;;\n" +
		"esac\n"
	if stderr != "" {
		script += "cat <<'ERUN_TEST_HELM_STDERR' >&2\n" + stderr + "\nERUN_TEST_HELM_STDERR\n"
	}
	script += "exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write helm stub: %v", err)
	}
	t.Setenv("ERUN_HELM_BIN", path)
}

// TestFetchObservedHelmReleaseChartUnreadableReportsError is the bug this
// test file exists to prevent regressing: when helm list cannot resolve
// chart/appVersion (here, it returns no matching entry even though helm
// status found the release), Chart/ChartVersion/AppVersion must stay empty
// and Error must say so — never render the empty string as if it were a
// real "no chart" reading.
func TestFetchObservedHelmReleaseChartUnreadableReportsError(t *testing.T) {
	statusStdout := `{"name":"erun-devops","namespace":"erun-ux","version":675,"info":{"status":"deployed"},"config":{}}`
	writeHelmObserveStub(t, statusStdout, "[]", 0, "")

	release := clusterread.FetchObservedHelmRelease([]string{"status", "erun-devops"}, []string{"list"}, "erun-devops", "erun-ux")

	if !release.Found {
		t.Fatalf("expected release to be found (helm status succeeded)")
	}
	if release.Chart != "" || release.ChartVersion != "" || release.AppVersion != "" {
		t.Fatalf("expected chart fields to stay empty when unreadable, got Chart=%q ChartVersion=%q AppVersion=%q",
			release.Chart, release.ChartVersion, release.AppVersion)
	}
	if release.Error == "" {
		t.Fatalf("expected a non-empty Error explaining chart/appVersion could not be determined")
	}
	if !strings.Contains(release.Error, "could not be determined") {
		t.Fatalf("Error must explain the field could not be read, got %q", release.Error)
	}
}
