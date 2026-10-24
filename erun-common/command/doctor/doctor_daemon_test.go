package doctor

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

func doctorTestContext() core.Context {
	return core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, io.Discard, io.Discard)}
}

// A failed root df (daemon root inside Docker Desktop's VM) must not hide the readings.
func TestDoctorHostDaemonToleratesAReadItCannotMake(t *testing.T) {
	binDir := t.TempDir()
	writeDockerStub(t, binDir, localDockerStubScript("Images  21  0  11.32GB  11.32GB (100%)"))
	t.Setenv("PATH", binDir)

	req := config.ShellLaunchParams{Tenant: "team", Environment: "local", Type: config.EnvironmentTypeHost}
	daemon := ResolveDoctorDockerDaemon(req)
	result, err := runDoctorDockerSteps(doctorTestContext(), nil, req, daemon, "doctor-inspect", doctorInspectionSteps())
	if err != nil {
		t.Fatalf("a df this machine cannot reach must not fail the daemon's own read: %v", err)
	}
	if !strings.Contains(result.Stdout, "Images  21") {
		t.Fatalf("the daemon's table must reach the report:\n%s", result.Stdout)
	}
}

// localDockerStubScript answers the store reading and the human table; every
// other docker invocation fails, like df against a VM-hosted root.
func localDockerStubScript(formatLines ...string) string {
	script := []string{
		`#!/bin/sh`,
		`case "$*" in`,
		`  *"system df --format"*) printf '%s\n' '` + strings.Join(formatLines, "' '") + `' ;;`,
	}
	if len(formatLines) > 0 {
		script = append(script, `  *"system df"*) printf '%s\n' '`+formatLines[len(formatLines)-1]+`' ;;`)
	}
	script = append(script, `  *"prune"*) printf '%s\n' 'Total reclaimed space: 4.105MB' ;;`)
	return strings.Join(append(script, `  *) exit 1 ;;`, `esac`, `exit 0`), "\n")
}

func TestDoctorHostDaemonReadFailureIsReportedNotInvented(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	req := config.ShellLaunchParams{Tenant: "team", Environment: "local", Type: config.EnvironmentTypeHost}
	daemon := ResolveDoctorDockerDaemon(req)
	if _, err := runDoctorDockerSteps(doctorTestContext(), nil, req, daemon, "doctor-inspect", doctorInspectionSteps()); err == nil {
		t.Fatalf("an unreachable local docker must be reported, not rendered as an empty daemon")
	}
}

func writeDockerStub(t *testing.T, dir, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o700); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}
}

// docker's "Total reclaimed space" cannot show a no-op prune, so the summary
// compares before and after.
func TestDoctorPruneSummaryTellsAPruneThatFreedNothingFromOneThatReclaimed(t *testing.T) {
	sidecar := model.DoctorDockerDaemon{Kind: model.DoctorDockerDaemonDindSidecar, Label: "the erun-dind sidecar"}
	readings := map[string]DoctorDockerStore{
		"before": {},
		"after":  {},
	}
	summary := DoctorPruneSummary(DoctorDockerResult{Daemon: sidecar, Readings: readings})
	for _, want := range []string{"the erun-dind sidecar", "nothing was reclaimable before the prune", "elsewhere"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q does not contain %q", summary, want)
		}
	}
}
