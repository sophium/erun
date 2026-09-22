package eruncommon

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func doctorTestContext() Context {
	return Context{Logger: NewLoggerWithWriters(VerbosityInfo, io.Discard, io.Discard)}
}

// TestResolveDoctorDockerDaemonSelectsTheDaemonHoldingBuildImages pins the
// daemon-selection seam: which docker daemon a doctor read or prune acts on is
// decided by the environment, not by a container name fixed in the command.
// The daemon matters because a prune of the wrong one reclaims nothing this
// environment's builds can use while still printing docker's own success.
func TestResolveDoctorDockerDaemonSelectsTheDaemonHoldingBuildImages(t *testing.T) {
	cases := []struct {
		name          string
		envType       EnvironmentType
		wantKind      DoctorDockerDaemonKind
		wantContainer string
		wantLabel     string
		wantReason    string
	}{
		{
			name:          "a local-agent environment builds in the dind sidecar",
			envType:       EnvironmentTypeLocalAgent,
			wantKind:      DoctorDockerDaemonDindSidecar,
			wantContainer: runtimeDindContainerName,
			wantLabel:     "the erun-dind sidecar",
		},
		{
			name:          "a remote-agent environment builds in the dind sidecar",
			envType:       EnvironmentTypeRemoteAgent,
			wantKind:      DoctorDockerDaemonDindSidecar,
			wantContainer: runtimeDindContainerName,
			wantLabel:     "the erun-dind sidecar",
		},
		{
			name:      "a host environment builds against this machine's daemon",
			envType:   EnvironmentTypeHost,
			wantKind:  DoctorDockerDaemonHost,
			wantLabel: "this machine's docker daemon",
		},
		{
			name:       "a runtime environment has no daemon holding build images",
			envType:    EnvironmentTypeRuntime,
			wantKind:   DoctorDockerDaemonNone,
			wantLabel:  "no build daemon",
			wantReason: "never builds",
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
			req := ShellLaunchParams{Tenant: "team", Environment: "dev", Namespace: "team-dev", Type: tc.envType}
			assertDoctorDaemonDecision(t, ResolveDoctorDockerDaemon(req), tc.wantKind, tc.wantContainer, tc.wantLabel, tc.wantReason)
		})
	}
}

// assertDoctorDaemonDecision checks one resolved daemon against every property
// the report depends on.
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
	// A resolved daemon must be reachable through a transport: a container to
	// exec into, or this machine.
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

// TestDoctorDockerStorageRefusesAnEnvironmentWithNoBuildDaemon reproduces the
// reported failure's shape: a prune that the environment cannot host. Before
// the daemon was resolved from the environment, the prune ran against
// erun-dind whatever the target was, so an environment whose pod carries no
// sidecar got a prune of a daemon that holds none of its build images -- and
// with it a docker-reported success that reads as space reclaimed. The
// requested prune must now fail naming why, and nothing may be dispatched.
func TestDoctorDockerStorageRefusesAnEnvironmentWithNoBuildDaemon(t *testing.T) {
	for _, envType := range []EnvironmentType{EnvironmentTypeRuntime, ""} {
		t.Run("type="+string(envType), func(t *testing.T) {
			runnerCalls := 0
			runner := func(ShellLaunchParams, string, string) (RemoteCommandResult, error) {
				runnerCalls++
				return RemoteCommandResult{}, nil
			}
			req := ShellLaunchParams{Tenant: "frs", Environment: "build", Namespace: "frs-build", Type: envType}
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

// TestDoctorDockerStorageRunsAgainstTheResolvedDaemon locks the other half of
// the seam: when the environment does carry a sidecar, the exec goes to that
// container and the store readings taken around the prune come back parsed, so
// the caller can say what the prune actually did rather than only repeating
// docker's own text.
func TestDoctorDockerStorageRunsAgainstTheResolvedDaemon(t *testing.T) {
	req := ShellLaunchParams{Tenant: "frs", Environment: "build", Namespace: "frs-build", Type: EnvironmentTypeRemoteAgent}
	daemon := ResolveDoctorDockerDaemon(req)
	if daemon.Kind != DoctorDockerDaemonDindSidecar {
		t.Fatalf("remote-agent daemon = %+v, want the dind sidecar", daemon)
	}

	var gotContainer, gotScript string
	runner := func(_ ShellLaunchParams, container, script string) (RemoteCommandResult, error) {
		gotContainer, gotScript = container, script
		return RemoteCommandResult{Stdout: strings.Join([]string{
			doctorDockerReadMarker + "before",
			"Images|11.32GB|11.32GB (100%)",
			"Build Cache|0B|0B",
			doctorDockerReadMarker + "before:end",
			"Deleted Images:",
			"Total reclaimed space: 4.105MB",
			doctorDockerReadMarker + "after",
			"Images|0B|0B",
			"Build Cache|0B|0B",
			doctorDockerReadMarker + "after:end",
			"== Docker system df ==",
			"Images          0         0         0B        0B",
		}, "\n")}, nil
	}

	steps, err := doctorActionSteps(DoctorActionPruneImages)
	if err != nil {
		t.Fatalf("action steps: %v", err)
	}
	result, err := runDoctorDockerSteps(doctorTestContext(), runner, req, daemon, "doctor-prune_images", steps)
	if err != nil {
		t.Fatalf("run steps: %v", err)
	}
	if gotContainer != runtimeDindContainerName {
		t.Fatalf("exec container = %q, want %q", gotContainer, runtimeDindContainerName)
	}
	if !strings.Contains(gotScript, "docker image prune -a -f") {
		t.Fatalf("script does not prune images:\n%s", gotScript)
	}
	if strings.Contains(result.Stdout, doctorDockerReadMarker) {
		t.Fatalf("the machine-readable readings must not reach the report:\n%s", result.Stdout)
	}
	before, ok := result.Readings["before"]
	if !ok {
		t.Fatalf("the before reading was not parsed: %+v", result.Readings)
	}
	if want := uint64(11_320_000_000); before.Reclaimable != want {
		t.Fatalf("before reclaimable = %d, want %d", before.Reclaimable, want)
	}
	if after := result.Readings["after"]; after.Reclaimable != 0 {
		t.Fatalf("after reclaimable = %d, want 0", after.Reclaimable)
	}
}

// TestDoctorHostDaemonRunsLocallyWithoutAShell pins the host transport: a host
// environment has no pod, so its daemon is reached by running docker on this
// machine directly. The steps carry the store readings back, and a step that
// cannot run (df over a daemon root that is not a path on this machine, as on
// Docker Desktop) is reported without taking the readings that did succeed
// down with it.
func TestDoctorHostDaemonRunsLocallyWithoutAShell(t *testing.T) {
	binDir := t.TempDir()
	writeDockerStub(t, binDir, localDockerStubScript("Images|11.32GB|11.32GB (100%)", "Build Cache|0B|0B"))
	t.Setenv("PATH", binDir)

	req := ShellLaunchParams{Tenant: "team", Environment: "local", Type: EnvironmentTypeHost}
	daemon := ResolveDoctorDockerDaemon(req)
	if daemon.Kind != DoctorDockerDaemonHost || daemon.Container != "" {
		t.Fatalf("host daemon = %+v, want a local daemon with no container", daemon)
	}

	steps, err := doctorActionSteps(DoctorActionPruneImages)
	if err != nil {
		t.Fatalf("action steps: %v", err)
	}
	// Every step is answered by the stub, so this exercises the local transport
	// rather than a real docker install.
	result, err := runDoctorDockerSteps(doctorTestContext(), nil, req, daemon, "doctor-prune_images", steps)
	if err != nil {
		t.Fatalf("run steps locally: %v", err)
	}
	for _, key := range []string{"before", "after"} {
		if store := result.Readings[key]; store.Reclaimable != 11_320_000_000 {
			t.Fatalf("%s reading = %+v, want the stub's 11.32GB", key, store)
		}
	}
	assertDoctorReportOnly(t, result.Stdout, "== Docker system df ==")
}

// TestDoctorHostDaemonToleratesAReadItCannotMake covers the other half of the
// same transport: reading the daemon's own root can fail on a machine that
// keeps that root inside the daemon's VM (Docker Desktop), and that failure
// must not take the daemon's own readings -- the figures an operator can act
// on -- down with it.
func TestDoctorHostDaemonToleratesAReadItCannotMake(t *testing.T) {
	binDir := t.TempDir()
	writeDockerStub(t, binDir, localDockerStubScript("Images  21  0  11.32GB  11.32GB (100%)"))
	t.Setenv("PATH", binDir)

	req := ShellLaunchParams{Tenant: "team", Environment: "local", Type: EnvironmentTypeHost}
	daemon := ResolveDoctorDockerDaemon(req)
	result, err := runDoctorDockerSteps(doctorTestContext(), nil, req, daemon, "doctor-inspect", doctorInspectionSteps())
	if err != nil {
		t.Fatalf("a df this machine cannot reach must not fail the daemon's own read: %v", err)
	}
	if !strings.Contains(result.Stdout, "Images  21") {
		t.Fatalf("the daemon's table must reach the report:\n%s", result.Stdout)
	}
}

// localDockerStubScript is a docker CLI stub that answers the machine-readable
// store reading and, when given it, the human table; every other docker
// invocation fails, which is how the df steps behave on a machine whose daemon
// keeps its root inside a VM.
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

// assertDoctorReportOnly checks that the one report body carries its expected
// content and none of the machine-readable readings it was parsed from.
func assertDoctorReportOnly(t *testing.T, report string, want ...string) {
	t.Helper()
	for _, expected := range want {
		if !strings.Contains(report, expected) {
			t.Fatalf("the report does not contain %q:\n%s", expected, report)
		}
	}
	if strings.Contains(report, doctorDockerReadMarker) {
		t.Fatalf("the machine-readable readings must not reach the report:\n%s", report)
	}
}

// TestDoctorHostDaemonReadFailureIsReportedNotInvented covers the other end: a
// machine with no reachable docker at all returns an error naming the daemon
// rather than an empty report that reads like a daemon with nothing in it.
func TestDoctorHostDaemonReadFailureIsReportedNotInvented(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	req := ShellLaunchParams{Tenant: "team", Environment: "local", Type: EnvironmentTypeHost}
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

// TestDoctorPruneSummaryTellsAPruneThatFreedNothingFromOneThatReclaimed is the
// reported failure's own case: the operator followed the documented remedy,
// read a success, and reclaimed nothing. docker's own "Total reclaimed space"
// line cannot distinguish that from a real reclaim, so the summary states what
// the daemon reported before and after, and says plainly when nothing moved.
func TestDoctorPruneSummaryTellsAPruneThatFreedNothingFromOneThatReclaimed(t *testing.T) {
	sidecar := DoctorDockerDaemon{Kind: DoctorDockerDaemonDindSidecar, Label: "the erun-dind sidecar"}
	cases := []struct {
		name     string
		readings map[string]DoctorDockerStore
		want     []string
	}{
		{
			name: "a prune that reclaimed space says how much",
			readings: map[string]DoctorDockerStore{
				"before": {Reclaimable: 11_320_000_000, Images: 11_320_000_000},
				"after":  {},
			},
			want: []string{"the erun-dind sidecar", "reclaimed 11.32GB"},
		},
		{
			name: "a prune that freed nothing says so",
			readings: map[string]DoctorDockerStore{
				"before": {Reclaimable: 11_320_000_000, Images: 11_320_000_000},
				"after":  {Reclaimable: 11_320_000_000, Images: 11_320_000_000},
			},
			want: []string{"the erun-dind sidecar", "freed nothing", "not reclaimed here"},
		},
		{
			name: "a prune of a daemon holding nothing says so",
			readings: map[string]DoctorDockerStore{
				"before": {},
				"after":  {},
			},
			want: []string{"the erun-dind sidecar", "nothing was reclaimable before the prune", "elsewhere"},
		},
		{
			name:     "no readings means no claim",
			readings: nil,
			want:     nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			summary := DoctorPruneSummary(DoctorDockerResult{Daemon: sidecar, Readings: tc.readings})
			if len(tc.want) == 0 {
				if summary != "" {
					t.Fatalf("summary = %q, want no claim without readings", summary)
				}
				return
			}
			for _, want := range tc.want {
				if !strings.Contains(summary, want) {
					t.Fatalf("summary %q does not contain %q", summary, want)
				}
			}
		})
	}
}

// TestDoctorDockerDaemonLineNamesTheDaemon keeps the attribution the report
// owes every figure below it: the same table from two different daemons has to
// be readable as two different daemons.
func TestDoctorDockerDaemonLineNamesTheDaemon(t *testing.T) {
	req := ShellLaunchParams{Tenant: "frs", Environment: "build", Namespace: "frs-build", Type: EnvironmentTypeRemoteAgent}
	line := DoctorDockerDaemonLine(ResolveDoctorDockerDaemon(req))
	for _, want := range []string{"erun-dind", "frs-build", "frs-devops"} {
		if !strings.Contains(line, want) {
			t.Fatalf("daemon line %q does not name %q", line, want)
		}
	}

	unavailable := DoctorDockerDaemonLine(ResolveDoctorDockerDaemon(ShellLaunchParams{Type: EnvironmentTypeRuntime}))
	if !strings.Contains(unavailable, "no build daemon") || !strings.Contains(unavailable, "runtime") {
		t.Fatalf("an environment with no build daemon must say so and why: %q", unavailable)
	}
}
