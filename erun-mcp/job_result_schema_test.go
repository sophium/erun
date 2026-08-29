package erunmcp

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// A task job's Result is "whatever the task returned, captured verbatim as
// JSON" -- an object for release, an array or scalar for others, absent for a
// job still running or with no typed result. exec_job_status's own output
// schema must accept every one of those shapes, not just the empty case: the
// SDK derives that schema from EnvironmentJob.Result's Go type
// (json.RawMessage, i.e. []byte), which the reflector renders as "array of
// bytes" -- a shape no task's actual result value can ever satisfy.

// execJobStatusOutputSchema fetches the schema the server actually publishes
// for exec_job_status over the wire (via tools/list) and resolves it, so the
// assertions below exercise the schema the tool really emits rather than a
// hand-written expectation of what it should be.
func execJobStatusOutputSchema(t *testing.T) *jsonschema.Resolved {
	t.Helper()
	session := connectWithCapabilities(t, string(eruncommon.MCPCapabilityRead))
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != "exec_job_status" {
			continue
		}
		if tool.OutputSchema == nil {
			t.Fatal("exec_job_status has no output schema")
		}
		raw, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatalf("marshal published output schema: %v", err)
		}
		var schema jsonschema.Schema
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("unmarshal published output schema: %v", err)
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			t.Fatalf("resolve published output schema: %v", err)
		}
		return resolved
	}
	t.Fatal("exec_job_status is not in tools/list")
	return nil
}

// jobInstanceWithResult marshals a real EnvironmentJob carrying result, then
// decodes it back into a plain any so it can be embedded in a hand-assembled
// JobStatusResult instance -- the job shape itself comes from the production
// type, only its result varies per case.
func jobInstanceWithResult(t *testing.T, result json.RawMessage) any {
	t.Helper()
	job := finishedJobFixture("release", []string{"release"})
	job.Result = result
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal job fixture: %v", err)
	}
	var instance any
	if err := json.Unmarshal(data, &instance); err != nil {
		t.Fatalf("unmarshal job fixture: %v", err)
	}
	return instance
}

func TestExecJobStatusOutputSchemaAcceptsAnyResultShape(t *testing.T) {
	resolved := execJobStatusOutputSchema(t)

	cases := map[string]json.RawMessage{
		"object result": json.RawMessage(`{"executed":true,"spec":{"images":3}}`),
		"array result":  json.RawMessage(`[1,2,3]`),
		"string result": json.RawMessage(`"done"`),
		"number result": json.RawMessage(`42`),
		"bool result":   json.RawMessage(`true`),
		"null result":   json.RawMessage(`null`),
	}

	for name, result := range cases {
		t.Run(name, func(t *testing.T) {
			instance := map[string]any{
				"tenant":      "tenant-a",
				"environment": "dev",
				"jobs":        []any{},
				"job":         jobInstanceWithResult(t, result),
			}
			if err := resolved.Validate(instance); err != nil {
				t.Fatalf("emitted output schema rejects a %s: %v", name, err)
			}
		})
	}
}

func TestExecJobStatusOutputSchemaAcceptsAJobWithNoResult(t *testing.T) {
	resolved := execJobStatusOutputSchema(t)

	job := finishedJobFixture("release", []string{"release"})
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal job fixture: %v", err)
	}
	var jobInstance any
	if err := json.Unmarshal(data, &jobInstance); err != nil {
		t.Fatalf("unmarshal job fixture: %v", err)
	}

	instance := map[string]any{
		"tenant":      "tenant-a",
		"environment": "dev",
		"jobs":        []any{},
		"job":         jobInstance,
	}
	if err := resolved.Validate(instance); err != nil {
		t.Fatalf("emitted output schema rejects a job with no result: %v", err)
	}
}

// execJobStatus calls the real tool through a real MCP session, so it
// exercises the SDK's own output-schema validation on the way out -- the
// exact step that failed for a finished release job.
func execJobStatus(t *testing.T, session *mcp.ClientSession, id string) JobStatusResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "exec_job_status",
		Arguments: map[string]any{"tenant": "tenant-a", "environment": "dev", "id": id},
	})
	if err != nil {
		t.Fatalf("exec_job_status: %v", err)
	}
	if res.IsError {
		t.Fatalf("exec_job_status reported a tool error: %+v", res.Content)
	}
	structured, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var out JobStatusResult
	if err := json.Unmarshal(structured, &out); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	if out.Job == nil {
		t.Fatalf("no job returned for id %q", id)
	}
	return out
}

func assertResultRoundTrips(t *testing.T, out JobStatusResult, want json.RawMessage) {
	t.Helper()
	var wantAny, gotAny any
	if err := json.Unmarshal(want, &wantAny); err != nil {
		t.Fatalf("unmarshal expected result: %v", err)
	}
	if err := json.Unmarshal(out.Job.Result, &gotAny); err != nil {
		t.Fatalf("unmarshal returned result: %v", err)
	}
	if !reflect.DeepEqual(wantAny, gotAny) {
		t.Fatalf("result did not round-trip: got %#v, want %#v", gotAny, wantAny)
	}
}

func TestExecJobStatusRoundTripsAJobResult(t *testing.T) {
	isolateLeaseCache(t)
	session := connectWithCapabilities(t, string(eruncommon.MCPCapabilityRead))

	cases := []struct {
		name   string
		id     string
		result json.RawMessage
	}{
		{"object result", "release-object", json.RawMessage(`{"executed":true,"spec":{"images":3}}`)},
		{"array result", "release-array", json.RawMessage(`[1,2,3]`)},
		{"scalar result", "release-scalar", json.RawMessage(`"done"`)},
	}

	for _, tc := range cases {
		job := finishedJobFixture(tc.id, []string{"release"})
		job.Result = tc.result
		writeJobFixture(t, "tenant-a", "dev", job)
	}
	writeJobFixture(t, "tenant-a", "dev", finishedJobFixture("no-result", []string{"raw", "echo", "hi"}))

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := execJobStatus(t, session, tc.id)
			assertResultRoundTrips(t, out, tc.result)
		})
	}

	t.Run("no result", func(t *testing.T) {
		out := execJobStatus(t, session, "no-result")
		if len(out.Job.Result) != 0 {
			t.Fatalf("expected no result, got %s", out.Job.Result)
		}
	})
}
