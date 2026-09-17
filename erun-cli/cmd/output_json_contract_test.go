package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// newOutputContractCmd wires a command the way the root command wires its
// children: the global --output flag, plus --json where a command still carries
// the pre-global alias.
func newOutputContractCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "probe",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE:          func(*cobra.Command, []string) error { return nil },
	}
	addOutputFlag(cmd)
	addJSONAliasFlag(cmd)
	return cmd
}

// TestOutputJSONHonouredThroughGlobalFlagAndAlias is the regression guard for
// the global --output json and the legacy per-command --json must
// resolve to the same mode, so both reach the one shared renderer instead of a
// command-local format.
func TestOutputJSONHonouredThroughGlobalFlagAndAlias(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantJSON bool
	}{
		{"default is text", nil, false},
		{"global flag", []string{"--output", "json"}, true},
		{"legacy alias", []string{"--json"}, true},
		{"explicit text", []string{"--output", "text"}, false},
		{"unparsable output falls back to text", []string{"--output", "yaml"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newOutputContractCmd()
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("execute %v: %v", tc.args, err)
			}
			if got := jsonRequested(cmd); got != tc.wantJSON {
				t.Fatalf("jsonRequested(%v) = %v, want %v", tc.args, got, tc.wantJSON)
			}
			if tc.wantJSON && commandOutputMode(cmd) != common.OutputJSON {
				t.Fatalf("commandOutputMode(%v) = %q, want %q", tc.args, commandOutputMode(cmd), common.OutputJSON)
			}
		})
	}
}

// TestJSONAliasIsHidden keeps --json an alias rather than a documented rival:
// it must not resurface in a command's help as its own output format.
func TestJSONAliasIsHidden(t *testing.T) {
	cmd := newOutputContractCmd()
	flag := cmd.Flags().Lookup(jsonAliasFlagName)
	if flag == nil {
		t.Fatalf("--%s is not registered", jsonAliasFlagName)
	}
	if !flag.Hidden {
		t.Fatalf("--%s is not hidden; it would read as a rival of the global --output", jsonAliasFlagName)
	}
}

// TestIdleRegistersNoRivalOutputFormat pins the specific defect: idle
// used to carry its own --json document. It must now offer the inherited
// --output plus the hidden alias, and no third output switch.
func TestIdleRegistersNoRivalOutputFormat(t *testing.T) {
	cmd := newIdleCmd(nil, nil)
	alias := cmd.Flags().Lookup(jsonAliasFlagName)
	if alias == nil || !alias.Hidden {
		t.Fatalf("idle's --json is missing or visible; want a hidden alias of the global flag")
	}
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if flag.Name != jsonAliasFlagName && strings.Contains(strings.ToLower(flag.Usage), "json") {
			t.Fatalf("idle declares a second JSON switch: --%s", flag.Name)
		}
	})
}

// objectPayload and slicePayload are the payloads the fixed commands hand the
// shared writer, with the keys an orchestrator reads off the document. Keys are
// asserted as a subset so adding a field to the shared type cannot fail here,
// while dropping one from the type would.
type objectPayload struct {
	command string
	payload any
	want    []string
}

func objectPayloads() []objectPayload {
	return []objectPayload{
		{
			command: "idle",
			payload: common.EnvironmentIdleStatus{
				SecondsUntilStop: 300,
				StopEligible:     true,
				Markers:          []common.EnvironmentIdleMarker{{Name: "build", Idle: true}},
			},
			want: []string{"policy", "markers", "secondsUntilStop", "stopEligible"},
		},
		{command: "list", payload: common.ListResult{}, want: []string{"defaults", "currentDirectory"}},
		{command: "whip", payload: common.WhipReport{DryRun: true}},
		{
			command: "activity lease take",
			payload: common.EnvironmentActivityLease{ID: "build", Name: "build"},
			want:    []string{"id", "name"},
		},
		{command: "activity sample", payload: common.ResidentActivityResult{}, want: []string{"busy"}},
		{command: "exec diff", payload: common.DiffResult{RawDiff: "diff --git a/x b/x"}, want: []string{"rawDiff"}},
	}
}

type slicePayload struct {
	command string
	payload any
	want    []string
}

func slicePayloads() []slicePayload {
	return []slicePayload{
		{
			command: "context list",
			payload: []common.CloudContextStatus{{
				CloudContextConfig: common.CloudContextConfig{Name: "erun-001"},
				Status:             "running",
			}},
			want: []string{"name", "status"},
		},
		{
			command: "activity lease list",
			payload: []common.EnvironmentActivityLease{{ID: "build", Name: "build"}},
			want:    []string{"id", "name"},
		},
		{
			command: "activity ai-session",
			payload: []common.AISessionStatus{{SessionID: "session-1"}},
			want:    []string{"sessionId", "state", "reason"},
		},
	}
}

// jsonTaggedKeys walks a payload type's json tags, promoting embedded structs,
// so the set of legal keys is derived from the type the shared writer marshals
// rather than restated here.
func jsonTaggedKeys(rt reflect.Type) map[string]bool {
	for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Array {
		rt = rt.Elem()
	}
	if rt.Kind() != reflect.Struct {
		return nil
	}
	keys := map[string]bool{}
	for i := 0; i < rt.NumField(); i++ {
		collectJSONKeys(rt.Field(i), keys)
	}
	return keys
}

func collectJSONKeys(field reflect.StructField, keys map[string]bool) {
	if field.PkgPath != "" && !field.Anonymous {
		return
	}
	tag := strings.Split(field.Tag.Get("json"), ",")[0]
	if tag == "-" {
		return
	}
	if tag == "" {
		if !field.Anonymous {
			keys[field.Name] = true
			return
		}
		for key := range jsonTaggedKeys(field.Type) {
			keys[key] = true
		}
		return
	}
	keys[tag] = true
}

func assertDeclaredKeys(t *testing.T, decoded map[string]json.RawMessage, payload any) {
	t.Helper()
	allowed := jsonTaggedKeys(reflect.TypeOf(payload))
	if len(allowed) == 0 {
		t.Fatalf("%T declares no json fields; the shape has no definition to come from", payload)
	}
	for key := range decoded {
		if !allowed[key] {
			t.Fatalf("emitted key %q is not declared by %T; the shape has drifted from its definition", key, payload)
		}
	}
}

// TestSharedResultWriterEmitsObjectShape asserts the property an orchestrator
// depends on: in JSON mode each fixed command's object payload reaches stdout as
// valid JSON, carrying its expected fields, with no key outside the type's own
// declaration, and without also running the human renderer.
func TestSharedResultWriterEmitsObjectShape(t *testing.T) {
	for _, tc := range objectPayloads() {
		t.Run(tc.command, func(t *testing.T) {
			var buf bytes.Buffer
			ctx := common.Context{Output: common.OutputJSON, Stdout: &buf}
			rendered := false
			if err := writeCommandResult(ctx, tc.payload, func() error {
				rendered = true
				return nil
			}); err != nil {
				t.Fatalf("writeCommandResult: %v", err)
			}
			if rendered {
				t.Fatal("JSON mode also ran the text renderer; the two formats would interleave")
			}
			if !json.Valid(buf.Bytes()) {
				t.Fatalf("stdout is not valid JSON:\n%s", buf.String())
			}
			var decoded map[string]json.RawMessage
			if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
				t.Fatalf("payload did not decode as a JSON object: %v\n%s", err, buf.String())
			}
			if len(decoded) == 0 {
				t.Fatalf("JSON mode emitted an empty object:\n%s", buf.String())
			}
			assertDeclaredKeys(t, decoded, tc.payload)
			for _, key := range tc.want {
				if _, ok := decoded[key]; !ok {
					t.Fatalf("expected field %q missing from %T\n%s", key, tc.payload, buf.String())
				}
			}
		})
	}
}

// TestSharedResultWriterEmitsSliceShape covers the list-shaped payloads, where
// an empty slice must still serialise as [] rather than null so a consumer can
// iterate without a nil check.
func TestSharedResultWriterEmitsSliceShape(t *testing.T) {
	for _, tc := range slicePayloads() {
		t.Run(tc.command, func(t *testing.T) {
			var buf bytes.Buffer
			ctx := common.Context{Output: common.OutputJSON, Stdout: &buf}
			if err := writeCommandResult(ctx, tc.payload, func() error { return nil }); err != nil {
				t.Fatalf("writeCommandResult: %v", err)
			}
			if !json.Valid(buf.Bytes()) {
				t.Fatalf("stdout is not valid JSON:\n%s", buf.String())
			}
			var decoded []map[string]json.RawMessage
			if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
				t.Fatalf("payload did not decode as a JSON array: %v\n%s", err, buf.String())
			}
			if len(decoded) != 1 {
				t.Fatalf("expected one element, got %d\n%s", len(decoded), buf.String())
			}
			assertDeclaredKeys(t, decoded[0], reflect.New(reflect.TypeOf(tc.payload).Elem()).Elem().Interface())
			for _, key := range tc.want {
				if _, ok := decoded[0][key]; !ok {
					t.Fatalf("expected field %q missing from element of %T\n%s", key, tc.payload, buf.String())
				}
			}
		})
	}
}

// TestTextModeNeverWritesJSON is the other half of the contract: the shared
// writer stays silent in text mode so a command's human report is not followed
// by a stray document.
func TestTextModeNeverWritesJSON(t *testing.T) {
	var buf bytes.Buffer
	ctx := common.Context{Output: common.OutputText, Stdout: &buf}
	rendered := false
	if err := writeCommandResult(ctx, common.EnvironmentIdleStatus{SecondsUntilStop: 300}, func() error {
		rendered = true
		return nil
	}); err != nil {
		t.Fatalf("writeCommandResult: %v", err)
	}
	if !rendered {
		t.Fatal("text mode did not run the command's own renderer")
	}
	if buf.Len() != 0 {
		t.Fatalf("text mode wrote a JSON document to stdout: %q", buf.String())
	}
}

// TestNoCommandWritesItsOwnStdoutJSON fails when a command grows a JSON document
// of its own instead of routing through the shared writer: a second format can
// only reappear where a second encoder does, which is how the divergence began.
// The one allow-listed site writes the in-pod monitor's persisted stop-decision
// handoff (a distinct payload record-stop reads back), not a command result.
func TestNoCommandWritesItsOwnStdoutJSON(t *testing.T) {
	allowed := map[string]string{
		"activity.go": "emitStopReadyJSON is the in-pod monitor's persisted stop-decision handoff, not a rendering of a command result",
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	scanned := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		scanned++
		if !bytes.Contains(source, []byte("json.NewEncoder(")) {
			continue
		}
		if _, ok := allowed[filepath.Base(path)]; !ok {
			t.Errorf("%s encodes JSON itself; route it through Context.WriteResult so --output json has one shape", path)
		}
	}
	if scanned == 0 {
		t.Fatal("scanned no source files; the guard would pass vacuously")
	}
}
