package erunmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// A tool started with wait:false runs its work inside this server's own
// long-lived process, which was never itself started as anyone's job, so
// nothing here inherits ERUN_JOB_ID the way a nested subprocess does. Without
// the caller's own job id threaded onto the record, the job that started this
// work can never find it on its own finish path -- its child scan matches on
// StartedByJobID and nothing else -- so an agent that used a background build
// as its gate got a clean exit and no gate-incomplete no matter what the build
// then did.
func TestJobEnvelopeRecordsTheCallersJobAsTheTasksParent(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "acme", Environment: "dev"}}

	job := startEnvelopeJob(t, runtime, "build", JobEnvelopeInput{
		Wait:           boolPtr(false),
		StartedByJobID: "gate-parent",
	}, func(bool, io.Writer) (CommandOutput, error) {
		return CommandOutput{Executed: true}, nil
	})

	if job.StartedByJobID != "gate-parent" {
		t.Fatalf("startedByJobId = %q, want gate-parent", job.StartedByJobID)
	}
	if job.Handoff {
		t.Fatalf("handoff = true without the caller asking for it")
	}
}

// Nothing is guessed when the caller supplies nothing: a caller driving this
// environment from outside any job at all is genuinely parentless, and
// attributing its work to whichever job happens to be running here would be a
// definite answer to an unknown question.
func TestJobEnvelopeLeavesTheParentEmptyWhenTheCallerNamesNone(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "acme", Environment: "dev"}}

	job := startEnvelopeJob(t, runtime, "deploy", JobEnvelopeInput{Wait: boolPtr(false)}, func(bool, io.Writer) (CommandOutput, error) {
		return CommandOutput{Executed: true}, nil
	})

	if job.StartedByJobID != "" {
		t.Fatalf("startedByJobId = %q, want empty for a caller that named no parent", job.StartedByJobID)
	}
}

// Handoff is what keeps the linkage from turning a deliberate "leave this
// running past my turn" into a parent that waits for it.
func TestJobEnvelopeMarksAHandoffTask(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "acme", Environment: "dev"}}

	job := startEnvelopeJob(t, runtime, "release", JobEnvelopeInput{
		Wait:           boolPtr(false),
		StartedByJobID: "gate-parent",
		Handoff:        true,
	}, func(bool, io.Writer) (CommandOutput, error) {
		return CommandOutput{Executed: true}, nil
	})

	if !job.Handoff {
		t.Fatalf("handoff = false, want true")
	}
}

// The diagnosability half: a failed background tool used to leave an exit code
// and a bare error string behind -- no argv (a task job has none to record)
// and no log at all -- which is indistinguishable from any other failure. The
// work's own trace and output now reach the job's log as it produces them.
func TestJobEnvelopeMirrorsTheWorksOutputIntoTheJobsLog(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "acme", Environment: "dev"}}

	job := startEnvelopeJob(t, runtime, "doctor", JobEnvelopeInput{Wait: boolPtr(false)}, func(_ bool, log io.Writer) (CommandOutput, error) {
		_, _ = io.WriteString(log, "docker build failed: no space left on device\n")
		return CommandOutput{Executed: true}, errors.New("exit status 1")
	})

	if job.Succeeded {
		t.Fatalf("job = %+v, want a failure", job)
	}
	output, err := eruncommon.ReadEnvironmentJobOutput(eruncommon.ReadEnvironmentJobOutputParams{
		Tenant: "acme", Environment: "dev", ID: job.ID,
	})
	if err != nil {
		t.Fatalf("ReadEnvironmentJobOutput: %v", err)
	}
	if !strings.Contains(output.Output, "no space left on device") {
		t.Fatalf("job output = %q, want the work's own output", output.Output)
	}
}

// A synchronous call has no job and no log, and already returns everything
// inline, so it must not be handed a nil writer the work would panic on.
func TestJobEnvelopeGivesASynchronousCallADiscardLog(t *testing.T) {
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "acme", Environment: "dev"}}

	envelope, err := runJobEnvelope(runtime, "build", JobEnvelopeInput{}, false, func(_ bool, log io.Writer) (CommandOutput, error) {
		if log == nil {
			return CommandOutput{}, errors.New("synchronous execute was handed a nil log writer")
		}
		_, writeErr := io.WriteString(log, "ignored")
		return CommandOutput{Executed: true}, writeErr
	})
	if err != nil {
		t.Fatalf("runJobEnvelope: %v", err)
	}
	if !envelope.Wait || envelope.JobID != "" {
		t.Fatalf("envelope = %+v, want the synchronous shape", envelope)
	}
}

// Embedding JobEnvelopeInput must stay invisible on the wire: the SDK's schema
// reflection flattens an embedded struct, so wait keeps its own top-level
// property exactly as it had when every input declared it itself, and the two
// new switches arrive beside it rather than nested under a wrapper object no
// caller knows to send.
func TestJobEnvelopeSwitchesArePublishedAsTopLevelProperties(t *testing.T) {
	t.Setenv("ERUN_CLAUDE_BIN", "true")
	t.Setenv("ERUN_CODEX_BIN", "true")
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "acme", Environment: "dev", RepoPath: t.TempDir()}}
	session := connectTestMCPSession(t, eruncommon.BuildInfo{Version: "1.2.3"}, runtime)

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	checked := 0
	for _, tool := range tools.Tools {
		properties := publishedInputProperties(t, tool.Name, tool.InputSchema)
		if _, ok := properties["wait"]; !ok {
			continue
		}
		checked++
		for _, property := range []string{"handoff", "startedByJobId"} {
			if _, ok := properties[property]; !ok {
				t.Errorf("%s publishes wait but no top-level %q property: the embedded envelope input did not flatten", tool.Name, property)
			}
		}
		if _, ok := properties["JobEnvelopeInput"]; ok {
			t.Errorf("%s publishes a nested JobEnvelopeInput object; the wire shape changed", tool.Name)
		}
	}
	if checked == 0 {
		t.Fatal("no tool published a wait property; this audit checked nothing")
	}
}

// publishedInputProperties reads a tool's top-level input properties off the
// schema the server actually publishes, so the assertions above exercise the
// wire shape rather than a hand-written expectation of it.
func publishedInputProperties(t *testing.T, name string, inputSchema any) map[string]json.RawMessage {
	t.Helper()
	if inputSchema == nil {
		return nil
	}
	raw, err := json.Marshal(inputSchema)
	if err != nil {
		t.Fatalf("marshal %s input schema: %v", name, err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("unmarshal %s input schema: %v", name, err)
	}
	return schema.Properties
}

// startEnvelopeJob starts a background task through the envelope and returns
// its finished record, bounded so work that never settles fails the test
// rather than hanging the suite.
func startEnvelopeJob(t *testing.T, runtime RuntimeConfig, name string, input JobEnvelopeInput, execute func(bool, io.Writer) (CommandOutput, error)) eruncommon.EnvironmentJob {
	t.Helper()
	envelope, err := runJobEnvelope(runtime, name, input, false, execute)
	if err != nil {
		t.Fatalf("runJobEnvelope: %v", err)
	}
	if envelope.JobID == "" || envelope.Wait {
		t.Fatalf("envelope = %+v, want a background handle", envelope)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		job, err := eruncommon.LoadEnvironmentJob(runtime.Context.Tenant, runtime.Context.Environment, envelope.JobID, time.Now())
		if err != nil {
			t.Fatalf("LoadEnvironmentJob: %v", err)
		}
		if job.Finished() {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %q did not finish within the deadline", envelope.JobID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// exec_raw grew a startedByJobId input, documented in the tool's own schema
// and in erun-docs, and the CLI's off-environment job start was taught to fill
// it in -- but it was never passed through to the job it starts, so every
// off-environment `erun exec job start -- <command>` recorded no parent at
// all. Preview resolves the supervisor invocation without spawning it, and
// that invocation is where the value has to appear.
func TestExecRawBackgroundThreadsTheCallersJobIDToTheSupervisor(t *testing.T) {
	t.Setenv("ERUN_ERUN_BIN", "erun-test-supervisor-stub")
	runtime := normalizeRuntimeConfig(RuntimeConfig{Context: RuntimeContext{Tenant: "acme", Environment: "dev", RepoPath: t.TempDir()}})

	for _, tc := range []struct {
		name  string
		input string
		want  bool
	}{
		{"supplied", "gate-parent", true},
		{"omitted", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, envelope, err := rawTool(runtime)(context.Background(), nil, RawInput{
				Command:        []string{"true"},
				Wait:           boolPtr(false),
				Preview:        true,
				StartedByJobID: tc.input,
			})
			if err != nil {
				t.Fatalf("exec_raw: %v", err)
			}
			trace := strings.Join(envelope.Trace, "\n")
			got := strings.Contains(trace, fmt.Sprintf("--started-by-job-id %s", tc.input)) && tc.input != ""
			if got != tc.want {
				t.Fatalf("supervisor invocation carried the parent job id = %v, want %v; trace:\n%s", got, tc.want, trace)
			}
			if tc.input == "" && strings.Contains(trace, "--started-by-job-id") {
				t.Fatalf("supervisor invocation named a parent nobody supplied; trace:\n%s", trace)
			}
		})
	}
}
