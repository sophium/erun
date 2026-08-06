package main

import (
	"context"
	"os/exec"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"
)

// TestRuntimeActivityGroupsResourceHoldingProcesses covers the leftover case
// the operator hit: Gradle runs finished but left thirteen resident JVMs
// holding memory, with nothing in the desktop showing them. The panel groups
// what an operator recognises and names the action that reclaims it — a pid
// table would be homework, not a decision.
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

	activity := runtimeActivityFromProbe(uiSelection{Tenant: "petios", Environment: "local"}, probe)

	// The pod's own supervision (the `sh` line) is not something the operator can
	// reclaim, so it is neither grouped nor counted.
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

// TestRuntimeActivityGroupsAreReadOnly pins the "show first, reclaim on an
// explicit action" rule at the level that matters: a class of process nobody
// should be killed out from under (the agent's own tool) is shown but carries
// no reclaim action.
func TestRuntimeActivityGroupsAreReadOnly(t *testing.T) {
	// `claude-real`, not `claude`: the image wraps the AI tools, so the wrapper's
	// name is what an exact-match classifier would have looked for and the real
	// memory holder is what `ps` actually reports. Observed live in petios/local.
	probe := "erun-process\t1048576\tclaude-real\tclaude-real --continue --model opus"
	activity := runtimeActivityFromProbe(uiSelection{Tenant: "petios", Environment: "local"}, probe)
	if len(activity.Processes) != 1 {
		t.Fatalf("expected the agent process to be shown, got %+v", activity.Processes)
	}
	if activity.Processes[0].Reclaim != "" {
		t.Fatalf("an agent process is the operator's work, not a leftover: %+v", activity.Processes[0])
	}
}

// TestLoadRuntimeActivityFailsSoft keeps the Runtime tab a status surface: an
// unreachable or stopped environment explains itself rather than turning the
// dialog into an error.
func TestLoadRuntimeActivityFailsSoft(t *testing.T) {
	app := NewApp(erunUIDeps{
		execRuntimePod: func(context.Context, uiSelection, string) (string, error) {
			return "", context.DeadlineExceeded
		},
	})
	activity, err := app.LoadRuntimeActivity(uiSelection{Tenant: "petios", Environment: "local"})
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

func TestRuntimeReclaimScriptsAreScopedToBuildLeftovers(t *testing.T) {
	cache, err := runtimeReclaimScript(runtimeReclaimBuildCache)
	if err != nil {
		t.Fatalf("build-cache reclaim: %v", err)
	}
	if !strings.Contains(cache, "prune") {
		t.Fatalf("build-cache reclaim must prune, got %q", cache)
	}
	// Neither action may touch the worktree or a running session — those are the
	// operator's work, and a reclaim that ate them would be a data-loss bug.
	for _, script := range []string{cache, mustReclaimScript(t, runtimeReclaimGradleDaemons)} {
		for _, forbidden := range []string{"rm -rf", "dtach", "/home/erun/git"} {
			if strings.Contains(script, forbidden) {
				t.Fatalf("reclaim script must not contain %q: %q", forbidden, script)
			}
		}
	}
}

// TestGradleReclaimSurvivesItsOwnProcessScan runs the daemon reclaim for real.
// `pgrep -f` matches whole command lines, and this script's own command line
// contains its pattern — so a literal spelling makes the script kill the shell
// running it. That is invisible in the script text; it only shows up when the
// script runs (it reached a live pod as exit 143), so the regression test has to
// execute it.
//
// Only this action is executed. The build-cache reclaim's commands act on
// whatever docker daemon the test host is pointed at, and a test that prunes a
// developer's real build cache is a worse defect than the one it guards. Its
// self-scan hazard does not exist anyway: it runs no process scan.
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
