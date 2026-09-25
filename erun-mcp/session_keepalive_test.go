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

	eruncommon "github.com/sophium/erun/erun-common"
)

// sessionTestTimeout is small enough that a test outlives it in seconds. Every
// wait below is however on an observable condition the edge produces, never on
// a duration: the renewals it has issued, and the SDK's own disconnection log.
const sessionTestTimeout = 2 * time.Second

// sessionTestEdge starts the production edge — the same newHTTPHandler a
// deployed pod runs, with the same middleware chain — over a timeout small
// enough to observe.
func sessionTestEdge(t *testing.T) *httptest.Server {
	t.Helper()
	for _, key := range []string{envMCPTrustedIssuers, envMCPTrustedIssuer, envMCPAudience, envTenant} {
		t.Setenv(key, "")
	}
	previousTimeout := mcpSessionTimeout
	mcpSessionTimeout = sessionTestTimeout
	t.Cleanup(func() { mcpSessionTimeout = previousTimeout })

	server := httptest.NewServer(newHTTPHandler(eruncommon.BuildInfo{Version: "1.2.3"}, HTTPConfig{Path: "/mcp"}, RuntimeConfig{}, nil))
	t.Cleanup(server.Close)
	return server
}

// captureServerLogs routes the SDK's own server-side logging into a buffer for
// the duration of a test, which is also how the reaping case below observes a
// session actually ending.
func captureServerLogs(t *testing.T) *lockedBuffer {
	t.Helper()
	previous := mcpServerLogger
	logs := &lockedBuffer{}
	mcpServerLogger = slog.New(slog.NewTextHandler(logs, nil))
	t.Cleanup(func() { mcpServerLogger = previous })
	return logs
}

// postMCPJSONRPC sends one JSON-RPC message the way a caller does, with the
// session header only when there is a session to name.
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

// claimSession performs the handshake and returns the session id the edge
// handed out, which is the id every later request pins.
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

// getMCPStream opens the session's standalone SSE stream and hands it back
// still open, which is the state a connected client is in between its calls.
func getMCPStream(t *testing.T, server *httptest.Server, sessionID string) *http.Response {
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
	return resp
}

// waitFor polls an observable condition to a deadline. The condition is always
// something the edge itself produced; the interval only bounds how often it is
// re-read.
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

// TestASessionSurvivesItsIdleTimeoutWhileItsStreamIsHeld is the reported
// failure: a caller that is connected and streaming still lost its session,
// because the SDK's idle timer counts POSTs and the GET stream is not one.
//
// The wait is on the edge's own renewal count, not on a duration. The ticker
// cannot fire early, so by the time it reports five renewals at a quarter of
// the timeout, more than the timeout has elapsed since the last POST — and if
// those renewals did not rearm the timer, the session would have been closed
// before the tool call below is sent.
func TestASessionSurvivesItsIdleTimeoutWhileItsStreamIsHeld(t *testing.T) {
	logs := captureServerLogs(t)
	server := sessionTestEdge(t)
	sessionID := claimSession(t, server)
	getMCPStream(t, server, sessionID)

	// Either outcome is observable without sleeping: the edge renews past the
	// timeout, or the SDK reaps the session for being idle. Which one happened
	// is what the call below then decides.
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

// TestASessionWithNoHeldStreamIsStillReapedWhenIdle is the other half of the
// trade: renewing a session must not amount to never reaping one. With nothing
// attached to it, a session is still closed on the SDK's own schedule — which
// is what keeps a caller that abandons a session without terminating it (every
// one-shot CLI call does) from accumulating for the lifetime of the pod.
//
// The SDK's own disconnection log is the observable: the session is known to
// be gone before the call below is made, rather than assumed gone because a
// duration passed.
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
