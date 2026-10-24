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

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
	"github.com/sophium/erun/erun-mcp/tools/exectool"
	"github.com/sophium/erun/erun-mcp/tools/shared/jobenvelope"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

// The server process has no ERUN_JOB_ID, so the caller's job id must be threaded onto
// the record or its parent's child scan never finds it.
func TestJobEnvelopeRecordsTheCallersJobAsTheTasksParent(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "acme", Environment: "dev"}}

	job := startEnvelopeJob(t, runtime, "build", jobenvelope.JobEnvelopeInput{
		Wait:           boolPtr(false),
		StartedByJobID: "gate-parent",
	}, func(bool, io.Writer) (toolio.CommandOutput, error) {
		return toolio.CommandOutput{Executed: true}, nil
	})

	if job.StartedByJobID != "gate-parent" {
		t.Fatalf("startedByJobId = %q, want gate-parent", job.StartedByJobID)
	}
	if job.Handoff {
		t.Fatalf("handoff = true without the caller asking for it")
	}
}

// A caller outside any job is genuinely parentless.
func TestJobEnvelopeLeavesTheParentEmptyWhenTheCallerNamesNone(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "acme", Environment: "dev"}}

	job := startEnvelopeJob(t, runtime, "deploy", jobenvelope.JobEnvelopeInput{Wait: boolPtr(false)}, func(bool, io.Writer) (toolio.CommandOutput, error) {
		return toolio.CommandOutput{Executed: true}, nil
	})

	if job.StartedByJobID != "" {
		t.Fatalf("startedByJobId = %q, want empty for a caller that named no parent", job.StartedByJobID)
	}
}

// Handoff keeps a deliberate fire-and-forget from becoming a waited-on child.
func TestJobEnvelopeMarksAHandoffTask(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "acme", Environment: "dev"}}

	job := startEnvelopeJob(t, runtime, "release", jobenvelope.JobEnvelopeInput{
		Wait:           boolPtr(false),
		StartedByJobID: "gate-parent",
		Handoff:        true,
	}, func(bool, io.Writer) (toolio.CommandOutput, error) {
		return toolio.CommandOutput{Executed: true}, nil
	})

	if !job.Handoff {
		t.Fatalf("handoff = false, want true")
	}
}

// The work's trace and output reach the job's log as produced.
func TestJobEnvelopeMirrorsTheWorksOutputIntoTheJobsLog(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "acme", Environment: "dev"}}

	job := startEnvelopeJob(t, runtime, "doctor", jobenvelope.JobEnvelopeInput{Wait: boolPtr(false)}, func(_ bool, log io.Writer) (toolio.CommandOutput, error) {
		_, _ = io.WriteString(log, "docker build failed: no space left on device\n")
		return toolio.CommandOutput{Executed: true}, errors.New("exit status 1")
	})

	if job.Succeeded {
		t.Fatalf("job = %+v, want a failure", job)
	}
	output, err := jobs.ReadEnvironmentJobOutput(model.ReadEnvironmentJobOutputParams{
		Tenant: "acme", Environment: "dev", ID: job.ID,
	})
	if err != nil {
		t.Fatalf("ReadEnvironmentJobOutput: %v", err)
	}
	if !strings.Contains(output.Output, "no space left on device") {
		t.Fatalf("job output = %q, want the work's own output", output.Output)
	}
}

// The SDK flattens embedded structs, so the switches stay top-level properties.
func TestJobEnvelopeSwitchesArePublishedAsTopLevelProperties(t *testing.T) {
	t.Setenv("ERUN_CLAUDE_BIN", "true")
	t.Setenv("ERUN_CODEX_BIN", "true")
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "acme", Environment: "dev", RepoPath: t.TempDir()}}
	session := connectTestMCPSession(t, model.BuildInfo{Version: "1.2.3"}, runtime)

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

// startEnvelopeJob is bounded so unsettled work fails rather than hangs.
func startEnvelopeJob(t *testing.T, runtime runtimecfg.RuntimeConfig, name string, input jobenvelope.JobEnvelopeInput, execute func(bool, io.Writer) (toolio.CommandOutput, error)) jobstate.EnvironmentJob {
	t.Helper()
	envelope, err := jobenvelope.RunJobEnvelope(runtime, name, input, false, execute)
	if err != nil {
		t.Fatalf("runJobEnvelope: %v", err)
	}
	if envelope.JobID == "" || envelope.Wait {
		t.Fatalf("envelope = %+v, want a background handle", envelope)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		job, err := jobs.LoadEnvironmentJob(runtime.Context.Tenant, runtime.Context.Environment, envelope.JobID, time.Now())
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

// Preview resolves the supervisor invocation, where startedByJobId must appear.
func TestExecRawBackgroundThreadsTheCallersJobIDToTheSupervisor(t *testing.T) {
	t.Setenv("ERUN_ERUN_BIN", "erun-test-supervisor-stub")
	runtime := normalizeRuntimeConfig(runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "acme", Environment: "dev", RepoPath: t.TempDir()}})

	for _, tc := range []struct {
		name  string
		input string
		want  bool
	}{
		{"supplied", "gate-parent", true},
		{"omitted", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, envelope, err := exectool.RawTool(runtime)(context.Background(), nil, exectool.RawInput{
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
