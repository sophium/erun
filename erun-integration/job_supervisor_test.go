package integration

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/harnessexec"
)

// awaitJobSupervisorExit blocks until the job's own supervisor process is no
// longer alive, rather than trusting the instant its record first reads as
// finished. finishEnvironmentJob (job_supervisor.go) settles a job's terminal
// state and its clone-reclaim decision together, but the supervisor process
// itself only exits afterward -- stopping its heartbeat, closing its log,
// unwinding the rest of its own deferred teardown -- so a caller that returns
// the moment a job reads as finished can still race a supervisor that is
// momentarily still alive and still touching files under the scenario's
// TempDir. That race is exactly the leaked supervisors and
// "directory not empty" TempDir cleanup failures. A job whose supervisor
// never registered a pid, or is already gone, is treated as done immediately;
// this only waits out a supervisor provably still alive.
//
// "Alive" is erun-common's own answer (ProcessAlive), not a bare signal 0. A
// supervisor that has exited but has not been reaped answers signal 0, so the
// bare probe cannot tell it from one still running -- and in an agent pod
// nothing reaps it, because a process the pod's supervisor adopts as a child
// subreaper and never waits on stays a zombie for the pod's lifetime. Every
// scenario's wait then expires on a supervisor that is already gone, and the
// package burns its whole deadline as a timeout that reads like a hang in
// whichever change was being gated.
func awaitJobSupervisorExit(t *testing.T, setup env.Setup, envVars []string, id string, timeout time.Duration) {
	t.Helper()
	awaitJobSupervisorExitVia(t, setup, envVars, id, timeout, inEnvironmentJobStatus)
}

// channelUnreachableExitCode is the CLI's own contract for a host-side command
// whose environment channel stayed unreachable even after one transparent
// reattach (erun-cli/cmd/mcp_call.go's mcpChannelUnreachableExitCode). It
// exists for exactly one purpose, quoted from that constant: "distinct from a
// normal error so a caller looping on this command cannot misread 'the channel
// is down' as 'the job finished' or 'the tool failed'". Reading the code
// rather than matching on the transport error's prose is the point -- the
// words are ours to change and the code is not.
const channelUnreachableExitCode = 126

// jobSupervisorState is what one status read established about a job's
// supervisor. Three answers, not two: the wait must never act on the third as
// though it were either of the others.
type jobSupervisorState int

const (
	// jobSupervisorRecorded: the record was read, and it names a supervisor
	// pid whose liveness decides the wait.
	jobSupervisorRecorded jobSupervisorState = iota
	// jobSupervisorAbsent: there is no supervisor to wait out. The record was
	// read and names no pid, or the environment answered that it has no such
	// job -- either way the wait is over.
	jobSupervisorAbsent
	// jobSupervisorUnreadable: the read could not be performed at all, so what
	// the record says is unknown. This is not evidence of absence.
	jobSupervisorUnreadable
)

// jobSupervisorReading is one status read's answer: the pid the record carries
// and which of the three states the read established.
type jobSupervisorReading struct {
	pid   int
	state jobSupervisorState
}

// readJobSupervisor classifies one `job status` result; see
// awaitJobSupervisorExitVia for why the third state exists and what the
// absent/error boundary below deliberately still is.
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
// command, so the same wait covers jobs read through either namespace.
//
// The two namespaces do not fail the same way, which is why a failed read is
// not simply read as "no supervisor" here (readJobSupervisor above). An
// in-environment status touches a file; an off-environment one goes over the
// target's MCP edge, so it can come back having read nothing at all. Folding
// that in with "the record names no supervisor" returned immediately --
// reporting a teardown that had waited for nothing -- whenever the edge was
// down, which is main's behaviour rather than this wait's. Measured, by
// registering the emcp teardown ahead of the job teardowns in
// TestJobOffEnvironmentExclusiveClaimIsEnforced so LIFO killed the edge first:
// both status reads then exited 126 with empty stdout, and the scenario still
// passed. It fails now, naming the channel.
//
// What this deliberately does not do: a non-zero read that is not the
// unreachable contract above still reads as absence, and that is load-bearing
// rather than an oversight. `erun job status --id <id>` answers a job that was
// never started with exit 1 and empty stdout -- measured for the in-environment
// `baddir` and the off-environment `intruder`, and identical in shape to a
// transport failure by every field the harness sees. Two callers depend on
// that answer meaning "nothing to wait out": startJob registers its teardown
// before it knows whether the start succeeded (a failed start is not proof
// that nothing started, which is the leak window this wait exists to close),
// and the exclusive scenario above tears down a job whose start it has just
// asserted was refused. Telling "no such record" from "the read failed" needs
// a distinct answer the CLI does not yet give, so it stays a documented gap
// instead of a guessed one; TestReadJobSupervisor pins both sides of the line
// as they are.
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

// TestReadJobSupervisor pins how one status read is classified, including the
// two answers that must not be confused for each other.
//
// The case with teeth is the unreachable channel: reading a failed status as
// "no supervisor recorded" would make the off-environment teardown return
// without waiting whenever the edge is down. Every result shape below is the
// one the real binary produces, measured rather than invented -- a job the
// environment never started answers exit 1 with empty stdout and `no job ...`
// on stderr, and a channel that stayed unreachable answers 126 with empty
// stdout and the transport error on stderr. Removing the unreachable branch
// from readJobSupervisor makes the last case fail, which is the regression
// this test exists to catch.
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

// awaitJobSupervisorGone is the wait itself, over an observer that reports the
// supervisor pid the job currently records. It is split out from the CLI read
// that feeds it so the loop the whole teardown discipline rests on can be
// driven against a pid a test controls rather than one it has to wait for --
// see TestAwaitJobSupervisorGoneWaitsOutALivePid.
func awaitJobSupervisorGone(t *testing.T, id string, timeout time.Duration, observe func() (int, bool)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		pid, recorded := observe()
		if !recorded || !activity.ProcessAlive(pid) {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("job %q supervisor (pid %d) is still alive %s after being cancelled", id, pid, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// parkedProcessEnv re-executes this test binary as a plain parked process; see
// TestAwaitJobSupervisorGoneWaitsOutALivePid.
const parkedProcessEnv = "ERUN_TEST_PARKED_SUPERVISOR"

// TestAwaitJobSupervisorGoneWaitsOutALivePid pins the invariant every
// real-job teardown in this package depends on: the wait returns only once
// the pid the job records is no longer alive.
//
// A teardown that returns on the cancel while the supervisor still writes into
// t.TempDir() needs a lease renewal to lose a race by luck, so the state is
// forced instead. The supervisor is a real
// process this test starts and kills itself, and the observer feeding the loop
// is the same shape as production's (awaitJobSupervisorExitVia): it reports
// the pid the record carries and makes no liveness judgement of its own, so
// the loop's own ProcessAlive is the only thing that can end the wait.
//
// That is what gives this test its teeth against the shapes that would let a
// teardown return early. A wait that reads the record once and returns
// observes exactly once, which the assertion below rejects. A wait that drops
// the liveness check altogether never returns at all -- its observer always
// reports a pid, so it runs out the loop's deadline instead, which is why the
// deadline here is far below the scenario waits' own: that failure stays
// bounded and names the loop rather than reading as a hung suite.
//
// This cannot pass on Windows: os.FindProcess succeeds for an exited process
// while a handle is open and Signal(0) is unsupported, so ProcessAlive is
// constant-true there. No skip, deliberately: the same answer arms the 30s
// wait in stopJob and stopOffEnvironmentJob, so Windows needs a real liveness
// answer (base/core/process_state_windows.go), not a hidden signal.
func TestAwaitJobSupervisorGoneWaitsOutALivePid(t *testing.T) {
	t.Parallel()
	if os.Getenv(parkedProcessEnv) == "1" {
		// The re-executed copy: the stand-in supervisor, alive until the test
		// below kills it. It exits on its own eventually only so a test that
		// dies before its killer runs cannot strand it.
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
	// The killer is released once the observer has reported the pid twice.
	// Gating it on the count, rather than letting observe judge liveness as an
	// earlier version of this test did, is what puts the loop's own check under
	// test: the first read is then guaranteed to be of a live process -- the
	// killer cannot have run yet -- so a wait that returned on that read failed
	// to look, rather than catching a corpse.
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

	// The deadline bounds a wait that never checks liveness, not the kill: the
	// wait driven here concludes as soon as the killer's signal is observable,
	// a few tens of milliseconds after the second read.
	awaitJobSupervisorGone(t, "parked", 10*time.Second, observe)

	mu.Lock()
	defer mu.Unlock()
	if observations < 2 {
		t.Fatalf("the wait observed the supervisor %d time(s) before returning; a teardown that returns while the recorded pid is still alive hands a live heartbeat back to t.TempDir()'s cleanup", observations)
	}
}
