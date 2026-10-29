package workspace

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/mcp"
)

func workspaceSyncPlanEnv(kind config.EnvironmentType, sshd, sync bool, localPath string) config.OpenResult {
	return config.OpenResult{
		Tenant:      "acme",
		Environment: "dev",
		EnvConfig: config.EnvConfig{
			Name: "dev",
			Type: kind,
			SSHD: config.SSHDConfig{
				Enabled: sshd,
				WorkspaceSync: model.SSHDWorkspaceSyncConfig{
					Enabled:   sync,
					LocalPath: localPath,
				},
			},
		},
	}
}

func TestResolveWorkspaceSyncParamsNamesEachRefusal(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		result config.OpenResult
		want   error
	}{
		{"no local path", workspaceSyncPlanEnv(config.EnvironmentTypeRemoteAgent, true, true, ""), ErrWorkspaceSyncNoLocalPath},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ResolveWorkspaceSyncParams(testCase.result, nil)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("got %v, want %v", err, testCase.want)
			}
		})
	}
}

// Losing the edge's answer is worse than not advertising a local tool.
func TestAppendLocalToolsPassesThroughWhatItCannotParse(t *testing.T) {
	reply := []byte(`not json`)
	if got := mcp.McpAppendLocalTools(reply, []mcp.MCPLocalTool{{Name: "workspace_sync"}}); string(got) != string(reply) {
		t.Fatalf("got %q, want the reply untouched", got)
	}
}

func TestLocalToolReplyCarriesFailureAsContent(t *testing.T) {
	reply, err := mcp.McpLocalToolReply(json.RawMessage(`3`), "", errors.New("the SSH channel to the pod is not up"))
	if err != nil {
		t.Fatalf("render reply: %v", err)
	}
	var envelope struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(reply, &envelope); err != nil {
		t.Fatalf("reply is not JSON: %v", err)
	}
	if !envelope.Result.IsError {
		t.Fatal("a failed call must be marked as an error result")
	}
	if len(envelope.Result.Content) != 1 || !strings.Contains(envelope.Result.Content[0].Text, "SSH channel") {
		t.Fatalf("the reason must reach the caller: %s", reply)
	}
}
