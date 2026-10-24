package erunmcp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
	mcppkg "github.com/sophium/erun/erun-common/infra/mcp"
	sharedpkg "github.com/sophium/erun/erun-mcp/tools/shared"
)

func TestCorsMiddlewareAnswersPreflightWithoutReachingTheWrappedHandler(t *testing.T) {
	called := false
	handler := corsMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	server := httptest.NewServer(handler)
	defer server.Close()

	req, err := http.NewRequest(http.MethodOptions, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	req.Header.Set("Origin", "https://console.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", resp.StatusCode)
	}
	if called {
		t.Fatalf("preflight must not reach the wrapped handler")
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://console.example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want the reflected origin", got)
	}
	allowed := strings.ToLower(resp.Header.Get("Access-Control-Allow-Headers"))
	for _, header := range []string{"authorization", "content-type", "mcp-session-id"} {
		if !strings.Contains(allowed, header) {
			t.Fatalf("Access-Control-Allow-Headers = %q, must permit %q", allowed, header)
		}
	}
}

// TestCorsMiddlewareExposesSessionIdOnRealRequest: fetch() hides response
// headers from the caller's script unless the server lists them in
// Access-Control-Expose-Headers, and the MCP handshake requires the client to
// read Mcp-Session-Id off the initialize response.
func TestCorsMiddlewareExposesSessionIdOnRealRequest(t *testing.T) {
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Mcp-Session-Id", "abc123")
		w.WriteHeader(http.StatusOK)
	}))
	server := httptest.NewServer(handler)
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	req.Header.Set("Origin", "https://console.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://console.example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want the reflected origin", got)
	}
	if got := resp.Header.Get("Access-Control-Expose-Headers"); !strings.Contains(got, "Mcp-Session-Id") {
		t.Fatalf("Access-Control-Expose-Headers = %q, must list Mcp-Session-Id", got)
	}
}

func TestCorsMiddlewareAddsNothingForNonBrowserCallers(t *testing.T) {
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Post(server.URL, "application/json", nil) //nolint:noctx
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty when the request carried no Origin header", got)
	}
}

// TestNewHTTPHandlerAnswersPreflightEvenWhenAuthIsConfigured: a real preflight
// never carries the Authorization header (that is the whole point of the
// preflight), so CORS must sit outside auth or every cross-origin caller
// would be rejected before its real request is ever sent. The real POST
// behind it must still require the bearer token -- CORS widens who can reach
// the server, never who is authorized.
func TestNewHTTPHandlerAnswersPreflightEvenWhenAuthIsConfigured(t *testing.T) {
	t.Setenv(sharedpkg.EnvMCPTrustedIssuer, "file:///some/key.pub")
	t.Setenv(sharedpkg.EnvTenant, "acme")

	cfg := HTTPConfig{Path: "/mcp"}
	httpServer := httptest.NewServer(newHTTPHandler(model.BuildInfo{}, cfg, sharedpkg.RuntimeConfig{}, nil))
	defer httpServer.Close()

	preflight, err := http.NewRequest(http.MethodOptions, httpServer.URL+cfg.Path, nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	preflight.Header.Set("Origin", "https://console.example.com")
	resp, err := http.DefaultClient.Do(preflight)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204 even with auth configured", resp.StatusCode)
	}

	unauthenticated, err := http.NewRequest(http.MethodPost, httpServer.URL+cfg.Path, strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	unauthenticated.Header.Set("Content-Type", "application/json")
	postResp, err := http.DefaultClient.Do(unauthenticated)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = postResp.Body.Close() }()
	if postResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated POST status = %d, want 401 (CORS must not weaken auth)", postResp.StatusCode)
	}
}

func TestActivityHTTPMiddlewareSkipsRecordingForIdleProbe(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	xdg.Reload()

	runtime := sharedpkg.RuntimeConfig{Context: sharedpkg.RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}
	handler := activityHTTPMiddleware(runtime, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server := httptest.NewServer(handler)
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	req.Header.Set(mcppkg.MCPIdleProbeHeader, "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	_ = resp.Body.Close()

	dir, err := activity.EnvironmentActivityDir("tenant-a", "dev")
	if err != nil {
		t.Fatalf("EnvironmentActivityDir failed: %v", err)
	}
	for _, name := range []string{"mcp.json", "codex.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be absent under probe header, stat err=%v", name, err)
		}
	}
}

func TestActivityHTTPMiddlewareRecordsWithoutIdleProbe(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	xdg.Reload()

	runtime := sharedpkg.RuntimeConfig{Context: sharedpkg.RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}
	handler := activityHTTPMiddleware(runtime, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	_ = resp.Body.Close()

	dir, err := activity.EnvironmentActivityDir("tenant-a", "dev")
	if err != nil {
		t.Fatalf("EnvironmentActivityDir failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mcp.json")); err != nil {
		t.Fatalf("expected mcp.json to exist without probe header: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "codex.json")); !os.IsNotExist(err) {
		t.Fatalf("expected codex.json to be absent for non-codex user agent, stat err=%v", err)
	}
}

func TestActivityHTTPMiddlewareRecordsCodexWhenUserAgentMatches(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	xdg.Reload()

	runtime := sharedpkg.RuntimeConfig{Context: sharedpkg.RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}
	handler := activityHTTPMiddleware(runtime, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server := httptest.NewServer(handler)
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	req.Header.Set("User-Agent", "codex-cli/0.1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	_ = resp.Body.Close()

	dir, err := activity.EnvironmentActivityDir("tenant-a", "dev")
	if err != nil {
		t.Fatalf("EnvironmentActivityDir failed: %v", err)
	}
	for _, name := range []string{"mcp.json", "codex.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %s to exist for codex client: %v", name, err)
		}
	}
}
