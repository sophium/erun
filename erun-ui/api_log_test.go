package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeRawInput struct {
	Command []string `json:"command"`
}

type fakeRawOutput struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

// TestLoadAPILogFromMCPTargetsAPIDeployment pins the MCP `raw` fallback (used
// when the desktop has no local Kubernetes context) to the erun-backend-api
// chart's own Deployment/Service name. Before #1197's fix this shelled out to
// "deployment/${ERUN_TENANT:-erun}-devops" -- the runtime Deployment, which
// never runs an erun-backend-api container -- so the API log tab could never
// load through this path either.
func TestLoadAPILogFromMCPTargetsAPIDeployment(t *testing.T) {
	var capturedCommand []string
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		server := mcp.NewServer(&mcp.Implementation{Name: "fake-runtime", Version: "v0.0.1"}, nil)
		mcp.AddTool(server, &mcp.Tool{Name: "raw"}, func(_ context.Context, _ *mcp.CallToolRequest, input fakeRawInput) (*mcp.CallToolResult, fakeRawOutput, error) {
			capturedCommand = input.Command
			return nil, fakeRawOutput{Stdout: "api log from pod\n"}, nil
		})
		return server
	}, &mcp.StreamableHTTPOptions{JSONResponse: true})

	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	log, err := loadAPILogFromMCP(context.Background(), httpServer.URL, "")
	if err != nil {
		t.Fatalf("loadAPILogFromMCP failed: %v", err)
	}
	if log != "api log from pod" {
		t.Fatalf("unexpected log: %q", log)
	}

	if len(capturedCommand) != 3 || capturedCommand[0] != "sh" || capturedCommand[1] != "-lc" {
		t.Fatalf("unexpected raw command: %+v", capturedCommand)
	}
	script := capturedCommand[2]
	if !strings.Contains(script, `-l app="${ERUN_TENANT:-erun}-api"`) {
		t.Fatalf("script does not select the API chart's deployment: %s", script)
	}
	if strings.Contains(script, "-devops") {
		t.Fatalf("script still targets the runtime deployment: %s", script)
	}
}
