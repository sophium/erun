package integration

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/infra/activity/activitylease"
	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/harnessexec"
)

// awaitJobSupervisorExit waits until the job's supervisor process is gone, not
// just until its record reads finished: the supervisor still touches TempDir
// during teardown. Liveness uses ProcessAlive because an unreaped zombie still
// answers signal 0.
func awaitJobSupervisorExit(t *testing.T, setup env.Setup, envVars []string, id string, timeout time.Duration) {
	t.Helper()
	awaitJobSupervisorExitVia(t, setup, envVars, id, timeout, inEnvironmentJobStatus)
}

// channelUnreachableExitCode mirrors the CLI's mcpChannelUnreachableExitCode;
// match the code, not the transport error's prose.
const channelUnreachableExitCode = 126

// jobSupervisorState is what one status read established about a job's supervisor.
type jobSupervisorState int

const (
	jobSupervisorRecorded jobSupervisorState = iota
	// jobSupervisorAbsent: the record names no pid, or the job does not exist.
	jobSupervisorAbsent
	// jobSupervisorUnreadable: the read itself failed; not evidence of absence.
	jobSupervisorUnreadable
)

type jobSupervisorReading struct {
	pid   int
	state jobSupervisorState
}

func readJobSupervisor(result erun.Result) jobSupervisorReading {
	if result.ExitCode == channelUnreachableExitCode {
		return jobSupervisorReading{state: jobSupervisorUnreadable}
	}
	var payload struct {
		PID int `json:"pid"`
	}
	if json.Unmarshal([]byte(result.Stdout), &payload) != nil || payload.PID <= 0 {
		return jobSupervisorReading{state: jobSupervisorAbsent}
	}
	return jobSupervisorReading{pid: payload.PID, state: jobSupervisorRecorded}
}

// awaitJobSupervisorExitVia is awaitJobSupervisorExit over an explicit status
// command. An unreachable edge fails rather than reading as "no supervisor"; any
// other non-zero read means absence, since the CLI answers a never-started job
// the same way and startJob's teardown relies on that.
func awaitJobSupervisorExitVia(t *testing.T, setup env.Setup, envVars []string, id string, timeout time.Duration, status func(string) []string) {
	t.Helper()
	awaitJobSupervisorGone(t, id, timeout, func() (int, bool) {
		result := erun.Run(t, status(id), erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		reading := readJobSupervisor(result)
		if reading.state == jobSupervisorUnreadable {
			t.Fatalf("job %q: `erun %s` could not reach the environment's MCP channel, so whether the supervisor is still alive is unknown. A teardown that never got its read must not report success; the transport error below names the remedy:\n%s",
				id, strings.Join(status(id), " "), result.Combined)
		}
		return reading.pid, reading.state == jobSupervisorRecorded
	})
}

func TestReadJobSupervisor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		result erun.Result
		want   jobSupervisorReading
	}{
		{
			name:   "a_recorded_supervisor_reports_its_pid",
			result: erun.Result{ExitCode: 0, Stdout: "{\n  \"id\": \"gate\",\n  \"state\": \"exited\",\n  \"pid\": 2035\n}\n"},
			want:   jobSupervisorReading{pid: 2035, state: jobSupervisorRecorded},
		},
		{
			name:   "a_record_that_names_no_pid_is_absent",
			result: erun.Result{ExitCode: 0, Stdout: "{\n  \"id\": \"gate\",\n  \"state\": \"exited\"\n}\n"},
			want:   jobSupervisorReading{state: jobSupervisorAbsent},
		},
		{
			name:   "a_job_the_environment_never_started_is_absent",
			result: erun.Result{ExitCode: 1, Stdout: "", Stderr: "no job \"intruder\" in team/dev\n"},
			want:   jobSupervisorReading{state: jobSupervisorAbsent},
		},
		{
			name:   "an_unreachable_channel_is_not_an_absent_record",
			result: erun.Result{ExitCode: channelUnreachableExitCode, Stdout: "", Stderr: "MCP endpoint is not reachable: http://127.0.0.1:26700/mcp (connect: connection refused)\n"},
			want:   jobSupervisorReading{state: jobSupervisorUnreadable},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := readJobSupervisor(tc.result); got != tc.want {
				t.Fatalf("readJobSupervisor(exit %d, stdout %q) = %+v, want %+v", tc.result.ExitCode, tc.result.Stdout, got, tc.want)
			}
		})
	}
}

// awaitJobSupervisorGone is the wait loop, split out so a test can drive it
// against a pid it controls.
func awaitJobSupervisorGone(t *testing.T, id string, timeout time.Duration, observe func() (int, bool)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		pid, recorded := observe()
		if !recorded || !activitylease.ProcessAlive(pid) {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("job %q supervisor (pid %d) is still alive %s after being cancelled", id, pid, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// parkedProcessEnv re-executes this test binary as a stand-in supervisor.
const parkedProcessEnv = "ERUN_TEST_PARKED_SUPERVISOR"

// TestAwaitJobSupervisorGoneWaitsOutALivePid: the observer only reports the pid,
// so only the loop's own ProcessAlive can end the wait. Not skipped on Windows,
// where ProcessAlive must give a real answer too.
func TestAwaitJobSupervisorGoneWaitsOutALivePid(t *testing.T) {
	t.Parallel()
	if os.Getenv(parkedProcessEnv) == "1" {
		// Exits on its own eventually so a test that dies early cannot strand it.
		time.Sleep(60 * time.Second)
		os.Exit(0)
	}

	parked := harnessexec.Command(os.Args[0], "-test.run=TestAwaitJobSupervisorGoneWaitsOutALivePid")
	parked.Env = append(os.Environ(), parkedProcessEnv+"=1")
	if err := parked.Start(); err != nil {
		t.Fatalf("start the parked stand-in for a job supervisor: %v", err)
	}
	t.Cleanup(func() {
		_ = parked.Process.Kill()
		_ = parked.Wait()
	})
	pid := parked.Process.Pid

	var mu sync.Mutex
	observations := 0
	// Kill only after two reads, so the first read is guaranteed to see a live process.
	var release sync.Once
	released := make(chan struct{})
	observe := func() (int, bool) {
		mu.Lock()
		observations++
		seen := observations
		mu.Unlock()
		if seen >= 2 {
			release.Do(func() { close(released) })
		}
		return pid, true
	}

	go func() {
		<-released
		_ = parked.Process.Kill()
	}()

	// The deadline bounds a wait that never checks liveness.
	awaitJobSupervisorGone(t, "parked", 10*time.Second, observe)

	mu.Lock()
	defer mu.Unlock()
	if observations < 2 {
		t.Fatalf("the wait observed the supervisor %d time(s) before returning; a teardown that returns while the recorded pid is still alive hands a live heartbeat back to t.TempDir()'s cleanup", observations)
	}
}
