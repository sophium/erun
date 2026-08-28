package eruncommon

import (
	"os"
	"strconv"
	"strings"
	"testing"
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

// TestFetchObservedHelmReleaseReadsChartFromHelmList locks the parse layer
// against the real shapes captured from a live cluster: `helm status -o
// json`'s top-level keys are exactly apply_method/config/info/manifest/
// name/namespace/version — no "chart" key at all — while `helm list -o
// json` carries chart ("erun-devops-1.0.206") and app_version ("1.0.206").
// A golden-file test that only renders an already-parsed ObservedHelmRelease
// would never catch a bug in this parse step; this test feeds the parser the
// actual JSON text helm emits.
func TestFetchObservedHelmReleaseReadsChartFromHelmList(t *testing.T) {
	statusStdout := `{"name":"erun-devops","namespace":"erun-ux","version":675,` +
		`"info":{"status":"deployed"},"config":{},"manifest":"","apply_method":"helm"}`
	listStdout := `[{"name":"erun-devops","namespace":"erun-ux","revision":"675",` +
		`"updated":"2026-08-28 15:57:27.098572 +0300 +0300","status":"deployed",` +
		`"chart":"erun-devops-1.0.206","app_version":"1.0.206"}]`
	writeHelmObserveStub(t, statusStdout, listStdout, 0, "")

	release := fetchObservedHelmRelease([]string{"status", "erun-devops"}, []string{"list"}, "erun-devops", "erun-ux")

	if !release.Found {
		t.Fatalf("expected release to be found")
	}
	if release.Error != "" {
		t.Fatalf("expected no error, got %q", release.Error)
	}
	if release.Chart != "erun-devops" {
		t.Fatalf("Chart = %q, want %q", release.Chart, "erun-devops")
	}
	if release.ChartVersion != "1.0.206" {
		t.Fatalf("ChartVersion = %q, want %q", release.ChartVersion, "1.0.206")
	}
	if release.AppVersion != "1.0.206" {
		t.Fatalf("AppVersion = %q, want %q", release.AppVersion, "1.0.206")
	}
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

	release := fetchObservedHelmRelease([]string{"status", "erun-devops"}, []string{"list"}, "erun-devops", "erun-ux")

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
