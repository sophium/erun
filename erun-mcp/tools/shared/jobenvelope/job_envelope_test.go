package jobenvelope

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

// The server process has no ERUN_JOB_ID, so the caller's job id must be threaded onto
// the record or its parent's child scan never finds it.
func TestJobEnvelopeRecordsTheCallersJobAsTheTasksParent(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "acme", Environment: "dev"}}

	job := startEnvelopeJob(t, runtime, "build", JobEnvelopeInput{
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

	job := startEnvelopeJob(t, runtime, "deploy", JobEnvelopeInput{Wait: boolPtr(false)}, func(bool, io.Writer) (toolio.CommandOutput, error) {
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

	job := startEnvelopeJob(t, runtime, "release", JobEnvelopeInput{
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

	job := startEnvelopeJob(t, runtime, "doctor", JobEnvelopeInput{Wait: boolPtr(false)}, func(_ bool, log io.Writer) (toolio.CommandOutput, error) {
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

// startEnvelopeJob is bounded so unsettled work fails rather than hangs.
func startEnvelopeJob(t *testing.T, runtime runtimecfg.RuntimeConfig, name string, input JobEnvelopeInput, execute func(bool, io.Writer) (toolio.CommandOutput, error)) jobstate.EnvironmentJob {
	t.Helper()
	envelope, err := RunJobEnvelope(runtime, name, input, false, execute)
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
