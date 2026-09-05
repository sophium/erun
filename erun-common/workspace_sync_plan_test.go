package eruncommon

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func workspaceSyncPlanEnv(kind EnvironmentType, sshd, sync bool, localPath string) OpenResult {
	return OpenResult{
		Tenant:      "acme",
		Environment: "dev",
		EnvConfig: EnvConfig{
			Name: "dev",
			Type: kind,
			SSHD: SSHDConfig{
				Enabled: sshd,
				WorkspaceSync: SSHDWorkspaceSyncConfig{
					Enabled:   sync,
					LocalPath: localPath,
				},
			},
		},
	}
}

// Every refusal is a distinct value, because "the mirror did not change" is the
// one symptom they share and the operator's fix differs for each.
func TestResolveWorkspaceSyncParamsNamesEachRefusal(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		result OpenResult
		want   error
	}{
		{"local agent has no pod worktree", workspaceSyncPlanEnv(EnvironmentTypeLocalAgent, true, true, "/host/mirror"), ErrWorkspaceSyncNotRemoteAgent},
		{"sshd off", workspaceSyncPlanEnv(EnvironmentTypeRemoteAgent, false, true, "/host/mirror"), ErrWorkspaceSyncNotEnabled},
		{"sync off", workspaceSyncPlanEnv(EnvironmentTypeRemoteAgent, true, false, "/host/mirror"), ErrWorkspaceSyncNotEnabled},
		{"no local path", workspaceSyncPlanEnv(EnvironmentTypeRemoteAgent, true, true, ""), ErrWorkspaceSyncNoLocalPath},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ResolveWorkspaceSyncParams(testCase.result, nil)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("got %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestResolveWorkspaceSyncParamsAddressesThePodWorktree(t *testing.T) {
	params, err := ResolveWorkspaceSyncParams(workspaceSyncPlanEnv(EnvironmentTypeRemoteAgent, true, true, "/host/mirror"), nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if params.LocalPath != "/host/mirror" {
		t.Fatalf("local path = %q", params.LocalPath)
	}
	if params.HostAlias != SSHHostAlias("acme", "dev") {
		t.Fatalf("host alias = %q", params.HostAlias)
	}
	if strings.TrimSpace(params.RemotePath) == "" {
		t.Fatal("a pass needs a remote path to mirror from")
	}
}

// A tool the pod cannot serve still has to be discoverable, so it is merged into
// the edge's own listing rather than announced some other way.
func TestAppendLocalToolsExtendsTheEdgeListing(t *testing.T) {
	reply := []byte(`{"jsonrpc":"2.0","id":7,"result":{"tools":[{"name":"diff"}]}}`)
	merged := mcpAppendLocalTools(reply, []MCPLocalTool{{Name: "workspace_sync", Description: "refresh the mirror"}})

	var envelope struct {
		ID     int `json:"id"`
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(merged, &envelope); err != nil {
		t.Fatalf("merged reply is not JSON: %v (%s)", err, merged)
	}
	if envelope.ID != 7 {
		t.Fatalf("the reply must stay in its request slot, got id %d", envelope.ID)
	}
	names := []string{}
	for _, tool := range envelope.Result.Tools {
		names = append(names, tool.Name)
	}
	if len(names) != 2 || names[0] != "diff" || names[1] != "workspace_sync" {
		t.Fatalf("tools = %v, want the edge's own plus the local one", names)
	}
}

// An unparseable reply is passed through rather than dropped: losing the edge's
// answer is worse than not advertising a local tool.
func TestAppendLocalToolsPassesThroughWhatItCannotParse(t *testing.T) {
	reply := []byte(`not json`)
	if got := mcpAppendLocalTools(reply, []MCPLocalTool{{Name: "workspace_sync"}}); string(got) != string(reply) {
		t.Fatalf("got %q, want the reply untouched", got)
	}
}

func TestLocalToolForMatchesOnlyItsOwnCall(t *testing.T) {
	tools := []MCPLocalTool{{Name: "workspace_sync"}}

	call := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"workspace_sync","arguments":{"preview":true}}}`)
	tool, arguments, ok := mcpLocalToolFor(call, tools)
	if !ok || tool.Name != "workspace_sync" {
		t.Fatalf("expected the local tool to claim its own call, got %v %v", tool, ok)
	}
	if !strings.Contains(string(arguments), "preview") {
		t.Fatalf("arguments not carried through: %s", arguments)
	}

	other := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"diff"}}`)
	if _, _, claimed := mcpLocalToolFor(other, tools); claimed {
		t.Fatal("the edge's own tools must still be relayed")
	}
	if _, _, claimed := mcpLocalToolFor([]byte(`{"method":"tools/list"}`), tools); claimed {
		t.Fatal("a listing is not a call")
	}
}

// A failing tool answers in-band, so the caller reads why instead of seeing the
// session fault.
func TestLocalToolReplyCarriesFailureAsContent(t *testing.T) {
	reply, err := mcpLocalToolReply(json.RawMessage(`3`), "", errors.New("the SSH channel to the pod is not up"))
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
