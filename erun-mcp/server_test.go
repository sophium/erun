package erunmcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

func TestBuildVersionOutputDefaultsVersion(t *testing.T) {
	got := buildVersionOutput(eruncommon.BuildInfo{})
	if got.Version != "dev" || got.Commit != "" || got.Date != "" {
		t.Fatalf("unexpected defaults: %+v", got)
	}
}

func TestBuildVersionOutput(t *testing.T) {
	got := buildVersionOutput(eruncommon.BuildInfo{
		Version: "1.2.3",
		Commit:  "abcdef",
		Date:    "2024-01-01",
	})
	if got.Version != "1.2.3" || got.Commit != "abcdef" || got.Date != "2024-01-01" {
		t.Fatalf("unexpected version output: %+v", got)
	}
}

func TestNormalizeHTTPConfigDefaults(t *testing.T) {
	got, err := NormalizeHTTPConfig(HTTPConfig{})
	if err != nil {
		t.Fatalf("NormalizeHTTPConfig failed: %v", err)
	}
	if got.Host != DefaultHost || got.Port != DefaultPort || got.Path != DefaultPath {
		t.Fatalf("unexpected defaults: %+v", got)
	}
}

func TestNormalizeHTTPConfigRejectsInvalidPort(t *testing.T) {
	if _, err := NormalizeHTTPConfig(HTTPConfig{Port: 70000}); err == nil {
		t.Fatal("expected invalid port error")
	}
}

func TestEndpointURL(t *testing.T) {
	got := EndpointURL(HTTPConfig{})
	if got != "http://127.0.0.1:17000/mcp" {
		t.Fatalf("unexpected endpoint URL: %q", got)
	}
}

func TestHTTPHandlerExposesVersionTool(t *testing.T) {
	cfg := HTTPConfig{Path: "/mcp"}
	info := eruncommon.BuildInfo{
		Version: "1.2.3",
		Commit:  "abcdef",
		Date:    "2024-01-01",
	}

	httpServer := httptest.NewServer(NewHTTPHandler(info, cfg))
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL + cfg.Path,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "version" {
		t.Fatalf("unexpected tools: %+v", tools.Tools)
	}

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "version"})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	version := decodeStructuredVersion(t, result.StructuredContent)
	if got := version["version"]; got != "1.2.3" {
		t.Fatalf("unexpected structured content: %+v", version)
	}
}

func decodeStructuredVersion(t *testing.T, content any) map[string]any {
	t.Helper()

	switch typed := content.(type) {
	case map[string]any:
		return typed
	case json.RawMessage:
		var decoded map[string]any
		if err := json.Unmarshal(typed, &decoded); err != nil {
			t.Fatalf("Unmarshal(structured content) failed: %v", err)
		}
		return decoded
	default:
		t.Fatalf("unexpected structured content type %T", content)
		return nil
	}
}
