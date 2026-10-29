package erunmcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

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
