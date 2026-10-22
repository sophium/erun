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

// The two sides of a published tool definition, named so an assertion failure
// says which one it swept.
const (
	schemaSideInput  = "input"
	schemaSideOutput = "output"
)

// publishedSchemaWire is the part of one published schema the assertions below
// read, decoded from the generic JSON a client actually receives rather than
// from jsonschema-go's Go model -- which is exactly the gap the defect fell
// through: the Go tree looked fine and the bytes on the wire did not.
type publishedSchemaWire struct {
	// Properties keeps each `properties.<name>` value verbatim, so a value
	// that is the boolean `true` stays a boolean here instead of being
	// normalized away by a Go type that cannot represent it.
	Properties map[string]json.RawMessage `json:"properties"`
	// AdditionalProperties keeps `additionalProperties` verbatim. A boolean
	// is legal -- and common -- in this position, so nothing below flags it.
	AdditionalProperties json.RawMessage `json:"additionalProperties"`
}

// publishedSchema reads one tool's published input or output schema as the wire
// shape, returning a zero value for a tool that publishes no schema on that
// side.
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

// propertyIsJSONObject reports whether a published property's schema is a JSON
// object, which is the shape every client has to be able to read: the empty
// schema's other spelling, the boolean `true`, is refused outright by a client
// that validates the tool list.
func propertyIsJSONObject(t *testing.T, property json.RawMessage) bool {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(property, &decoded); err != nil {
		t.Fatalf("published property schema is not JSON: %v", err)
	}
	_, ok := decoded.(map[string]any)
	return ok
}

// booleanSchemaValue reports a published subschema's boolean value and whether
// it is a boolean at all, so the input-side assertion can show it saw the legal
// booleans it must leave alone: `additionalProperties: false` is how a tool
// rejects unknown arguments, and a blanket "no boolean in a schema" rule would
// flag every one of them.
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

// assertSchemaPropertiesAreJSONObjects fails for every top-level
// `properties.<name>` value in one tool's schema whose own schema is not a JSON
// object.
//
// The invariant is deliberately narrow. Only `properties.<name>` VALUES are
// examined: `additionalProperties` is not, because a boolean there is legal and
// `false` is how a tool rejects unknown arguments. Only the top level is swept,
// matching the output-side assertion this generalizes -- nested `properties`
// and `items` subschemas are not read. That boundary is stated rather than
// silent: widening it means teaching the sweep to tell a nested
// `properties.<name>` value apart from a nested `additionalProperties` key at
// every depth, and it is a change to both sides at once.
func assertSchemaPropertiesAreJSONObjects(t *testing.T, toolName, side string, properties map[string]json.RawMessage) {
	t.Helper()
	for name, property := range properties {
		if !propertyIsJSONObject(t, property) {
			t.Errorf("%s.%sSchema.properties.%s is %s, not a JSON object: a boolean subschema makes a client that validates the tool list refuse the whole tools/list, taking every erun tool with it",
				toolName, side, name, property)
		}
	}
}

// TestPublishedOutputSchemaPropertiesAreObjectsNotBooleans pins that every
// property of every published output schema is a JSON object.
//
// A property whose schema must accept any JSON value -- a json.RawMessage
// field, widened by rawJSONSchemaOverrides -- used to be emitted as the
// boolean `true`, because an empty jsonschema.Schema marshals to exactly that.
// `true` is a legal JSON Schema, so nothing here noticed; a client's tool-list
// validator that does not accept boolean subschemas refused the whole
// tools/list over it. build_profile carries one such field (Record, a raw
// TimingRecord), and one refusal of that list is the entire erun surface gone
// for the session's callers -- the in-pod agent included.
//
// The surface is swept, and `record` is then asserted by name so the case the
// report described is the one that has to pass, not just some property that
// happened to be checked.
func TestPublishedOutputSchemaPropertiesAreObjectsNotBooleans(t *testing.T) {
	session := connectWithCapabilities(t, string(eruncommon.MCPCapabilityAdmin))
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
				// Named explicitly below, so the property the report
				// described is one this test cannot pass without.
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

// TestPublishedInputSchemaPropertiesAreObjectsNotBooleans is the input-side
// half of that same invariant, and the half that was missing.
//
// The sweep above guards the shape the report described, but it reads
// `OutputSchema` only and skips every tool that publishes none; `InputSchema`
// was not examined anywhere. The identical collapse on the input side --
// `"properties": {"record": true}` -- would have landed with the whole suite
// green, and it is the same shape that took every erun tool down for the
// session's callers, because a client that validates the tool list refuses the
// entire list over one boolean subschema.
//
// The invariant is the narrow one: no `properties.<name>` VALUE may be a
// boolean. A blanket "no boolean in an input schema" rule would be wrong and is
// not what this asserts -- a boolean `additionalProperties` is legal, and most
// of these schemas publish `additionalProperties: false` on purpose to reject
// unknown arguments. The logged counts are what keeps that arm visible: they
// have to show the legal booleans were seen and left alone, not that the sweep
// simply never met one.
func TestPublishedInputSchemaPropertiesAreObjectsNotBooleans(t *testing.T) {
	session := connectWithCapabilities(t, string(eruncommon.MCPCapabilityAdmin))
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
