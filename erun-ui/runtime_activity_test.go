package main

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestRuntimeActivityGroupsResourceHoldingProcesses(t *testing.T) {
	probe := strings.Join([]string{
		"erun-session\tai\t900\tclaude",
		"erun-process\t2097152\tjava\t/usr/bin/java -Xmx2g org.gradle.launcher.daemon.bootstrap.GradleDaemon 8.7",
		"erun-process\t1048576\tjava\t/usr/bin/java -Xmx1g org.gradle.launcher.daemon.bootstrap.GradleDaemon 8.7",
		"erun-process\t524288\tjava\t/usr/bin/java -jar testcontainers.jar",
		"erun-process\t262144\tbuildkitd\t/usr/bin/buildkitd",
		"erun-process\t4096\tsh\t/bin/sh -lc true",
		"erun-process\tnot-a-number\tjava\tjava",
	}, "\n")

	activity := runtimeActivityFromProbe(uimodel.Selection{Tenant: "petios", Environment: "local"}, probe)

	want := []uiRuntimeProcessGroup{
		{ID: runtimeProcessGroupGradle, Label: "2 Gradle daemons", Count: 2, Memory: "3.0 GiB", MemoryMiB: 3072, Reclaim: runtimeReclaimGradleDaemons, ReclaimLabel: "Stop build daemons"},
		{ID: runtimeProcessGroupJava, Label: "1 Java process", Count: 1, Memory: "512 MiB", MemoryMiB: 512, Reclaim: runtimeReclaimGradleDaemons, ReclaimLabel: "Stop build daemons"},
		{ID: runtimeProcessGroupDocker, Label: "1 container build process", Count: 1, Memory: "256 MiB", MemoryMiB: 256, Reclaim: runtimeReclaimBuildCache, ReclaimLabel: "Prune build cache"},
	}
	if !reflect.DeepEqual(activity.Processes, want) {
		t.Fatalf("unexpected process groups:\n got %+v\nwant %+v", activity.Processes, want)
	}
	if activity.MemoryHeld != "3.8 GiB" {
		t.Fatalf("held memory must exclude the ignored processes, got %q", activity.MemoryHeld)
	}
	if !strings.Contains(activity.Message, "1 session running") {
		t.Fatalf("the panel line must state the observed session count, got %q", activity.Message)
	}
}

func TestRuntimeActivityGroupsAreReadOnly(t *testing.T) {
	probe := "erun-process\t1048576\tclaude.exe\tclaude.exe --continue --model opus"
	activity := runtimeActivityFromProbe(uimodel.Selection{Tenant: "petios", Environment: "local"}, probe)
	if len(activity.Processes) != 1 {
		t.Fatalf("expected the agent process to be shown, got %+v", activity.Processes)
	}
	if activity.Processes[0].Reclaim != "" {
		t.Fatalf("an agent process is the operator's work, not a leftover: %+v", activity.Processes[0])
	}
}

func TestLoadRuntimeActivityFailsSoft(t *testing.T) {
	app := NewApp(erunUIDeps{
		execRuntimePod: func(context.Context, uimodel.Selection, string) (string, error) {
			return "", context.DeadlineExceeded
		},
	})
	activity, err := app.LoadRuntimeActivity(uimodel.Selection{Tenant: "petios", Environment: "local"})
	if err != nil {
		t.Fatalf("LoadRuntimeActivity must not surface a pod read failure as an error: %v", err)
	}
	if activity.Available {
		t.Fatalf("an unreadable pod must not be reported as an available reading: %+v", activity)
	}
	if !strings.Contains(activity.Message, "Cannot read what the runtime is running") {
		t.Fatalf("the reason must be visible, got %q", activity.Message)
	}
}

// An already-expired parent ctx must be reported as our timeout, not the mocked "signal: killed".
func TestLoadRuntimeActivityReportsOwnTimeoutNotSignalKilled(t *testing.T) {
	app := NewApp(erunUIDeps{
		execRuntimePod: func(context.Context, uimodel.Selection, string) (string, error) {
			return "", errors.New("signal: killed")
		},
	})
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer cancel()
	app.ctx = ctx

	activity, err := app.LoadRuntimeActivity(uimodel.Selection{Tenant: "petios", Environment: "local"})
	if err != nil {
		t.Fatalf("LoadRuntimeActivity must not surface a probe failure as an error: %v", err)
	}
	if activity.Available {
		t.Fatalf("a timed-out probe must not be reported as an available reading: %+v", activity)
	}
	if !strings.Contains(activity.Message, "timed out") {
		t.Fatalf("expected the panel to name its own timeout, got %q", activity.Message)
	}
	if strings.Contains(activity.Message, "signal:") {
		t.Fatalf("the panel must never repeat the raw kill signal on a timeout, got %q", activity.Message)
	}
}

func TestLoadRuntimeActivityReportsExternalKillDistinctFromTimeout(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("signal-terminated exec is a POSIX concept")
	}
	app := NewApp(erunUIDeps{
		execRuntimePod: func(context.Context, uimodel.Selection, string) (string, error) {
			cmd := exec.Command("/bin/sh", "-c", "kill -9 $$")
			return "", cmd.Run()
		},
	})
	app.ctx = context.Background()

	activity, err := app.LoadRuntimeActivity(uimodel.Selection{Tenant: "petios", Environment: "local"})
	if err != nil {
		t.Fatalf("LoadRuntimeActivity must not surface a probe failure as an error: %v", err)
	}
	if strings.Contains(activity.Message, "timed out") {
		t.Fatalf("a kill this probe did not cause must not be read as its own timeout, got %q", activity.Message)
	}
	if strings.Contains(activity.Message, "signal:") {
		t.Fatalf("the panel must never repeat the raw kill signal, got %q", activity.Message)
	}
	if !strings.Contains(activity.Message, "killed") {
		t.Fatalf("expected the panel to name an external kill, got %q", activity.Message)
	}
}

func TestRuntimeReclaimScriptsAreScopedToBuildLeftovers(t *testing.T) {
	cache, err := runtimeReclaimScript(runtimeReclaimBuildCache)
	if err != nil {
		t.Fatalf("build-cache reclaim: %v", err)
	}
	if !strings.Contains(cache, "prune") {
		t.Fatalf("build-cache reclaim must prune, got %q", cache)
	}
	for _, script := range []string{cache, mustReclaimScript(t, runtimeReclaimGradleDaemons)} {
		for _, forbidden := range []string{"rm -rf", "dtach", "/home/erun/git"} {
			if strings.Contains(script, forbidden) {
				t.Fatalf("reclaim script must not contain %q: %q", forbidden, script)
			}
		}
	}
}

// Executed for real: the self-kill via `pgrep -f` only shows up when the script runs.
// The build-cache reclaim is not run because it would prune the host's real docker cache.
func TestGradleReclaimSurvivesItsOwnProcessScan(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("reclaim scripts are sh scripts that run inside the runtime pod")
	}
	script := mustReclaimScript(t, runtimeReclaimGradleDaemons)
	cmd := exec.Command("/bin/sh", "-c", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the daemon reclaim did not survive its own run: %v\n%s", err, output)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("the daemon reclaim exited %d; a pod with nothing to reclaim is a clean no-op", code)
	}
	if strings.Contains(script, "pgrep -f GradleDaemon") {
		t.Fatalf("an unbracketed pattern makes pgrep -f match this script's own command line: %q", script)
	}
}

func mustReclaimScript(t *testing.T, action string) string {
	t.Helper()
	script, err := runtimeReclaimScript(action)
	if err != nil {
		t.Fatalf("%s reclaim: %v", action, err)
	}
	return script
}
