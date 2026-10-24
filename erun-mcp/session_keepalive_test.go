package erunmcp

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-mcp/tools/shared/edgeauth"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
)

// sessionTestTimeout is short; waits below are on observable conditions, never durations.
const sessionTestTimeout = 2 * time.Second

// sessionTestEdge runs the production handler and middleware chain.
func sessionTestEdge(t *testing.T) *httptest.Server {
	t.Helper()
	for _, key := range []string{edgeauth.EnvMCPTrustedIssuers, edgeauth.EnvMCPTrustedIssuer, edgeauth.EnvMCPAudience, edgeauth.EnvTenant} {
		t.Setenv(key, "")
	}
	previousTimeout := mcpSessionTimeout
	mcpSessionTimeout = sessionTestTimeout
	t.Cleanup(func() { mcpSessionTimeout = previousTimeout })

	server := httptest.NewServer(newHTTPHandler(model.BuildInfo{Version: "1.2.3"}, HTTPConfig{Path: "/mcp"}, runtimecfg.RuntimeConfig{}, nil))
	t.Cleanup(server.Close)
	return server
}

func captureServerLogs(t *testing.T) *lockedBuffer {
	t.Helper()
	previous := mcpServerLogger
	logs := &lockedBuffer{}
	mcpServerLogger = slog.New(slog.NewTextHandler(logs, nil))
	t.Cleanup(func() { mcpServerLogger = previous })
	return logs
}

func postMCPJSONRPC(t *testing.T, server *httptest.Server, sessionID, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, server.URL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set(mcpSessionHeader, sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", body, err)
	}
	defer func() { _ = resp.Body.Close() }()
	reply, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	return resp.StatusCode, string(reply)
}

func claimSession(t *testing.T, server *httptest.Server) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, server.URL+"/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"session-test","version":"v0.0.1"}}}`))
	if err != nil {
		t.Fatalf("build initialize: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("read initialize reply: %v", err)
	}
	sessionID := strings.TrimSpace(resp.Header.Get(mcpSessionHeader))
	if sessionID == "" {
		t.Fatalf("edge handed out no %s header on initialize (HTTP %d)", mcpSessionHeader, resp.StatusCode)
	}
	if status, reply := postMCPJSONRPC(t, server, sessionID, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); status >= 300 {
		t.Fatalf("notifications/initialized: HTTP %d %s", status, reply)
	}
	return sessionID
}

// getMCPStream holds the standalone SSE stream open until the test ends.
func getMCPStream(t *testing.T, server *httptest.Server, sessionID string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/mcp", nil)
	if err != nil {
		t.Fatalf("build GET stream request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(mcpSessionHeader, sessionID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open the session stream: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("opening the session stream returned HTTP %d, want 200", resp.StatusCode)
	}
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if condition() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The ticker cannot fire early, so five renewals at a quarter of the timeout
// prove the timeout passed since the last POST.
func TestASessionSurvivesItsIdleTimeoutWhileItsStreamIsHeld(t *testing.T) {
	logs := captureServerLogs(t)
	server := sessionTestEdge(t)
	sessionID := claimSession(t, server)
	getMCPStream(t, server, sessionID)

	renewals := int64(5)
	baseline := mcpSessionKeepAlivePings.Load()
	waitFor(t, "the streamed session to be renewed or reaped", func() bool {
		return mcpSessionKeepAlivePings.Load()-baseline >= renewals ||
			strings.Contains(logs.String(), "server session disconnected")
	})

	status, reply := postMCPJSONRPC(t, server, sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if status != http.StatusOK {
		t.Fatalf("a session whose own stream is held was reaped anyway; tools/list returned HTTP %d %s", status, reply)
	}
	if !strings.Contains(reply, `"tools"`) {
		t.Fatalf("tools/list on a session held open by its own stream returned no tools: %s", reply)
	}
}

// Renewal must not mean never reaping: an abandoned session is still closed,
// observed through the SDK's disconnection log.
func TestASessionWithNoHeldStreamIsStillReapedWhenIdle(t *testing.T) {
	logs := captureServerLogs(t)
	server := sessionTestEdge(t)
	sessionID := claimSession(t, server)

	waitFor(t, "the edge to reap the idle session", func() bool {
		return strings.Contains(logs.String(), "server session disconnected")
	})

	status, reply := postMCPJSONRPC(t, server, sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if status != http.StatusNotFound {
		t.Fatalf("an idle session with no stream attached should be reaped; tools/list returned HTTP %d %s", status, reply)
	}
}
