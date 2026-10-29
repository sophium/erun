package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	tenantsvc "github.com/sophium/erun/erun-ui/internal/tenant"
)

type fakeRawInput struct {
	Command []string `json:"command"`
}

type fakeRawOutput struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

// The MCP fallback must target the erun-backend-api Deployment, not the runtime's.
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

func TestLoadAPILogPrefersKubernetesLogs(t *testing.T) {
	t.Setenv("PATH", fakeKubectl(t, func(args []string) (string, int) {
		got := strings.Join(args, "\n")
		// The API runs in <tenant>-api (open.APIDeploymentName), not <tenant>-devops.
		want := "--context\ncluster-dev\n--namespace\nfrs-prod\nlogs\n-l\napp=frs-api\n--prefix\n-c\nerun-backend-api\n--tail\n400"
		if got != want {
			t.Fatalf("unexpected kubectl args:\n%s", got)
		}
		return "api log from pod\n", 0
	})+string(os.PathListSeparator)+os.Getenv("PATH"))

	log, err := loadAPILog(context.Background(), tenantsvc.DashboardInput{
		Tenant:            "frs",
		Environment:       "prod",
		MCPURL:            "http://127.0.0.1:17000/mcp",
		KubernetesContext: "cluster-dev",
	})
	if err != nil {
		t.Fatalf("loadAPILog failed: %v", err)
	}
	if log != "api log from pod" {
		t.Fatalf("unexpected log: %q", log)
	}
}

func fakeKubectl(t *testing.T, handler func([]string) (string, int)) string {
	t.Helper()
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "kubectl.args")
	outputPath := filepath.Join(dir, "kubectl.output")
	exitPath := filepath.Join(dir, "kubectl.exit")
	writeFakeKubectlStub(t, dir, argsPath, outputPath, exitPath)
	output, code := handler([]string{"--context", "cluster-dev", "--namespace", "frs-prod", "logs", "-l", "app=frs-api", "--prefix", "-c", "erun-backend-api", "--tail", "400"})
	if err := os.WriteFile(outputPath, []byte(output), 0o644); err != nil {
		t.Fatalf("write fake kubectl output failed: %v", err)
	}
	if err := os.WriteFile(exitPath, []byte(fmt.Sprintf("%d", code)), 0o644); err != nil {
		t.Fatalf("write fake kubectl exit failed: %v", err)
	}
	t.Cleanup(func() {
		data, err := os.ReadFile(argsPath)
		if err != nil {
			t.Fatalf("read fake kubectl args failed: %v", err)
		}
		normalized := strings.ReplaceAll(string(data), "\r\n", "\n")
		handler(strings.Split(strings.TrimSpace(normalized), "\n"))
	})
	return dir
}

// writeFakeKubectlStub installs a recording kubectl on PATH. On Windows it must be a
// .cmd with CRLF endings (PATHEXT lookup; cmd.exe mis-parses LF labels).
func writeFakeKubectlStub(t *testing.T, dir, argsPath, outputPath, exitPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		script := "@echo off\r\n" +
			"type nul > \"" + argsPath + "\"\r\n" +
			":loop\r\n" +
			"if \"%~1\"==\"\" goto after\r\n" +
			">> \"" + argsPath + "\" echo(%~1\r\n" +
			"shift\r\n" +
			"goto loop\r\n" +
			":after\r\n" +
			"type \"" + outputPath + "\"\r\n" +
			"set /p code=< \"" + exitPath + "\"\r\n" +
			"exit /b %code%\r\n"
		if err := os.WriteFile(filepath.Join(dir, "kubectl.cmd"), []byte(script), 0o755); err != nil {
			t.Fatalf("write fake kubectl failed: %v", err)
		}
		return
	}
	script := `#!/bin/sh
printf '%s\n' "$@" >"` + argsPath + `"
cat "` + outputPath + `"
exit "$(cat "` + exitPath + `")"
`
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake kubectl failed: %v", err)
	}
}
