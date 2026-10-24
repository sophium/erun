package doctor

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/build"
)

func doctorTestContext() core.Context {
	return core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, io.Discard, io.Discard)}
}

func TestResolveDoctorDockerDaemonSelectsTheDaemonHoldingBuildImages(t *testing.T) {
	cases := []struct {
		name          string
		envType       config.EnvironmentType
		wantKind      DoctorDockerDaemonKind
		wantContainer string
		wantLabel     string
		wantReason    string
	}{
		{
			name:          "a remote-agent environment builds in the dind sidecar",
			envType:       config.EnvironmentTypeRemoteAgent,
			wantKind:      DoctorDockerDaemonDindSidecar,
			wantContainer: build.RuntimeDindContainerName,
			wantLabel:     "the erun-dind sidecar",
		},
		{
			name:       "an unresolved type names what to fix instead of guessing",
			envType:    "",
			wantKind:   DoctorDockerDaemonNone,
			wantLabel:  "no known build daemon",
			wantReason: "Set the environment's type",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := config.ShellLaunchParams{Tenant: "team", Environment: "dev", Namespace: "team-dev", Type: tc.envType}
			assertDoctorDaemonDecision(t, ResolveDoctorDockerDaemon(req), tc.wantKind, tc.wantContainer, tc.wantLabel, tc.wantReason)
		})
	}
}

func assertDoctorDaemonDecision(t *testing.T, daemon DoctorDockerDaemon, wantKind DoctorDockerDaemonKind, wantContainer, wantLabel, wantReason string) {
	t.Helper()
	if daemon.Kind != wantKind {
		t.Fatalf("kind = %q, want %q (daemon %+v)", daemon.Kind, wantKind, daemon)
	}
	if daemon.Container != wantContainer {
		t.Fatalf("container = %q, want %q", daemon.Container, wantContainer)
	}
	if daemon.Label != wantLabel {
		t.Fatalf("label = %q, want %q", daemon.Label, wantLabel)
	}
	if strings.TrimSpace(daemon.Where) == "" {
		t.Fatalf("every decision must say where the daemon is, or why there is none")
	}
	assertDoctorDaemonReason(t, daemon, wantReason)
	if daemon.Unavailable() != (wantKind == DoctorDockerDaemonNone) {
		t.Fatalf("Unavailable() = %v for kind %q", daemon.Unavailable(), daemon.Kind)
	}
	// A resolved daemon needs a transport: a container or this machine.
	if daemon.Container == "" && !daemon.Unavailable() && daemon.Kind != DoctorDockerDaemonHost {
		t.Fatalf("a pod-hosted daemon needs its container named: %+v", daemon)
	}
}

func assertDoctorDaemonReason(t *testing.T, daemon DoctorDockerDaemon, wantReason string) {
	t.Helper()
	if wantReason == "" {
		return
	}
	if !strings.Contains(daemon.Where, wantReason) {
		t.Fatalf("reason %q does not name %q", daemon.Where, wantReason)
	}
}

// Pruning a daemon without the build images would report success while
// reclaiming nothing.
func TestDoctorDockerStorageRefusesAnEnvironmentWithNoBuildDaemon(t *testing.T) {
	for _, envType := range []config.EnvironmentType{""} {
		t.Run("type="+string(envType), func(t *testing.T) {
			runnerCalls := 0
			runner := func(config.ShellLaunchParams, string, string) (kube.RemoteCommandResult, error) {
				runnerCalls++
				return kube.RemoteCommandResult{}, nil
			}
			req := config.ShellLaunchParams{Tenant: "frs", Environment: "build", Namespace: "frs-build", Type: envType}
			ctx := doctorTestContext()

			_, err := RunDoctorAction(ctx, runner, req, DoctorActionPruneImages)
			if !errors.As(err, &DoctorDockerDaemonUnavailableError{}) {
				t.Fatalf("prune error = %v, want DoctorDockerDaemonUnavailableError", err)
			}
			if !strings.Contains(err.Error(), "build images") {
				t.Fatalf("the refusal must say why it cannot prune, got: %v", err)
			}
			if _, err := RunDoctorInspection(ctx, runner, req); !errors.As(err, &DoctorDockerDaemonUnavailableError{}) {
				t.Fatalf("inspection error = %v, want DoctorDockerDaemonUnavailableError", err)
			}
			if runnerCalls != 0 {
				t.Fatalf("nothing may be dispatched when no daemon holds the build images; runner called %d time(s)", runnerCalls)
			}
		})
	}
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
	sidecar := DoctorDockerDaemon{Kind: DoctorDockerDaemonDindSidecar, Label: "the erun-dind sidecar"}
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
