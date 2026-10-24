package erunmcp

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/infra/mcptools"
	"github.com/sophium/erun/erun-mcp/tools/jobs"
)

// execJobStatusOutputSchema resolves the schema actually published via tools/list.
func execJobStatusOutputSchema(t *testing.T) *jsonschema.Resolved {
	t.Helper()
	session := connectWithCapabilities(t, string(mcptools.MCPCapabilityRead))
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

// The reflector renders json.RawMessage as an array of bytes, which no task result satisfies.
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

// execJobStatus goes through a real session so the SDK validates the output schema.
func execJobStatus(t *testing.T, session *mcp.ClientSession, id string) jobs.JobStatusResult {
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
	var out jobs.JobStatusResult
	if err := json.Unmarshal(structured, &out); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	if out.Job == nil {
		t.Fatalf("no job returned for id %q", id)
	}
	return out
}

func assertResultRoundTrips(t *testing.T, out jobs.JobStatusResult, want json.RawMessage) {
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
	session := connectWithCapabilities(t, string(mcptools.MCPCapabilityRead))

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

const (
	schemaSideInput  = "input"
	schemaSideOutput = "output"
)

// publishedSchemaWire is decoded from wire JSON, not jsonschema-go's model, which hid the defect.
type publishedSchemaWire struct {
	// Raw so a boolean `true` subschema stays visible.
	Properties           map[string]json.RawMessage `json:"properties"`
	AdditionalProperties json.RawMessage            `json:"additionalProperties"`
}

func publishedSchema(t *testing.T, tool *mcp.Tool, side string) publishedSchemaWire {
	t.Helper()
	schema := tool.OutputSchema
	if side == schemaSideInput {
		schema = tool.InputSchema
	}
	if schema == nil {
		return publishedSchemaWire{}
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("%s: marshal %s schema: %v", tool.Name, side, err)
	}
	var decoded publishedSchemaWire
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("%s: %s schema is not an object schema: %v", tool.Name, side, err)
	}
	return decoded
}

// propertyIsJSONObject: clients that validate the tool list refuse boolean subschemas.
func propertyIsJSONObject(t *testing.T, property json.RawMessage) bool {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(property, &decoded); err != nil {
		t.Fatalf("published property schema is not JSON: %v", err)
	}
	_, ok := decoded.(map[string]any)
	return ok
}

// booleanSchemaValue lets the input sweep show it saw legal `additionalProperties: false`.
func booleanSchemaValue(raw json.RawMessage) (value, isBoolean bool) {
	if len(raw) == 0 {
		return false, false
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false, false
	}
	boolean, ok := decoded.(bool)
	return boolean, ok
}

// assertSchemaPropertiesAreJSONObjects checks only top-level `properties.<name>` values;
// boolean additionalProperties is legal.
func assertSchemaPropertiesAreJSONObjects(t *testing.T, toolName, side string, properties map[string]json.RawMessage) {
	t.Helper()
	for name, property := range properties {
		if !propertyIsJSONObject(t, property) {
			t.Errorf("%s.%sSchema.properties.%s is %s, not a JSON object: a boolean subschema makes a client that validates the tool list refuse the whole tools/list, taking every erun tool with it",
				toolName, side, name, property)
		}
	}
}

// An empty jsonschema.Schema marshals to `true`, and one boolean subschema makes clients
// refuse the whole tools/list. build_profile's `record` is asserted by name.
func TestPublishedOutputSchemaPropertiesAreObjectsNotBooleans(t *testing.T) {
	session := connectWithCapabilities(t, string(mcptools.MCPCapabilityAdmin))
	tools := listTools(t, session)
	if len(tools) == 0 {
		t.Fatal("no tools listed; the rest of this test would pass vacuously")
	}

	properties, rawValueProperties := 0, 0
	for _, tool := range tools {
		if tool.OutputSchema == nil {
			continue
		}
		shape := publishedSchema(t, tool, schemaSideOutput)
		for name := range shape.Properties {
			properties++
			if name == "record" {
				rawValueProperties++
			}
		}
		assertSchemaPropertiesAreJSONObjects(t, tool.Name, schemaSideOutput, shape.Properties)
	}

	t.Logf("output schema properties checked: %d, json.RawMessage-backed: %d", properties, rawValueProperties)
	if properties == 0 {
		t.Fatal("no output schema property was checked, so the loop above proves nothing")
	}
	if rawValueProperties == 0 {
		t.Fatal("no json.RawMessage-backed property was found, so the case the report described was never exercised")
	}
}

// Input-side half: no `properties.<name>` value may be a boolean. The logged counts show
// legal boolean additionalProperties were seen and left alone.
func TestPublishedInputSchemaPropertiesAreObjectsNotBooleans(t *testing.T) {
	session := connectWithCapabilities(t, string(mcptools.MCPCapabilityAdmin))
	tools := listTools(t, session)
	if len(tools) == 0 {
		t.Fatal("no tools listed; the rest of this test would pass vacuously")
	}

	properties, booleanAdditional, falseAdditional := 0, 0, 0
	for _, tool := range tools {
		shape := publishedSchema(t, tool, schemaSideInput)
		properties += len(shape.Properties)
		if value, isBoolean := booleanSchemaValue(shape.AdditionalProperties); isBoolean {
			booleanAdditional++
			if !value {
				falseAdditional++
			}
		}
		assertSchemaPropertiesAreJSONObjects(t, tool.Name, schemaSideInput, shape.Properties)
	}

	t.Logf("input schema properties checked: %d, boolean additionalProperties (legal, not flagged): %d, of which false: %d",
		properties, booleanAdditional, falseAdditional)
	if properties == 0 {
		t.Fatal("no input schema property was checked, so the loop above proves nothing")
	}
	if falseAdditional == 0 {
		t.Fatal("no `additionalProperties: false` was seen, so this test does not demonstrate the legal boolean it must leave alone")
	}
}
