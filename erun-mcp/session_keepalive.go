package erunmcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// A session is only reaped for being idle, and the SDK's definition of idle
// counts POSTs and nothing else. startPOST/endPOST — the pair that pauses and
// rearms the session timer — are reached from the one place the streamable
// handler branches on req.Method == http.MethodPost, so a client that holds
// the standalone GET stream open is, to that timer, indistinguishable from a
// client that has gone away. Its session is closed on schedule five minutes
// after its last POST, its next tools/call lands on a session the edge no
// longer knows, and the SDK's initialization guard answers with a JSON-RPC
// error in place of the tool result.
//
// The SDK says as much itself: the TODO above startPOST records that pausing
// the timer for a resumed non-standalone SSE stream is not implemented and
// that clients should send keepalive pings if they want their session to
// live. No exported hook reaches that timer — sessionInfo and its timer are
// unexported and the sessions map is not exposed — so the edge sends those
// pings itself, for exactly as long as one of its clients is attached to a
// stream.
//
// This is not an unbounded session. The renewal runs only while a GET stream
// the edge is serving is open, so a session nobody is attached to is still
// reaped on the SDK's own schedule, and the one-shot callers this edge also
// serves (every `erun mcp call`) leak nothing they did not already leak.
type sessionKeepAlive struct {
	// inner is the streamable handler itself, deliberately without any of the
	// edge's own middleware around it: a renewal ping is the edge talking to
	// itself, so it must not re-authenticate, must not be metered as caller
	// traffic, and must not be recorded as env activity.
	inner http.Handler
	// path is the one MCP path the edge serves, used verbatim on the synthetic
	// request so the SDK's own cross-origin guard sees the same path a real
	// caller uses.
	path     string
	interval time.Duration
}

// withSessionKeepAlive renews a session for as long as a caller holds its
// stream. It is a no-op when the session timeout is disabled, because then
// there is no timer to keep ahead of.
func withSessionKeepAlive(inner http.Handler, path string, timeout time.Duration) http.Handler {
	if timeout <= 0 {
		return inner
	}
	keepAlive := &sessionKeepAlive{inner: inner, path: path, interval: sessionKeepAliveInterval(timeout)}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		sessionID := strings.TrimSpace(req.Header.Get(mcpSessionHeader))
		if req.Method != http.MethodGet || sessionID == "" {
			inner.ServeHTTP(w, req)
			return
		}
		// The GET stream is served synchronously for as long as the caller
		// holds it, so this call's own lifetime is the span to renew across.
		done := make(chan struct{})
		stop := make(chan struct{})
		go func() {
			defer close(done)
			renewSessionWhileHeld(req.Context(), keepAlive, sessionID, stop)
		}()
		defer func() {
			close(stop)
			<-done
		}()
		inner.ServeHTTP(w, req)
	})
}

// renewSessionWhileHeld pings until the caller lets go of the stream. It stops
// on the stream's own end, on the request context being cancelled (the caller
// disconnected), and on stop being closed, so nothing outlives the stream it
// was renewing for.
func renewSessionWhileHeld(ctx context.Context, keepAlive *sessionKeepAlive, sessionID string, stop <-chan struct{}) {
	ticker := time.NewTicker(keepAlive.interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			keepAlive.ping(sessionID)
		}
	}
}

// sessionKeepAliveInterval is a fraction of the timeout, so losing a single
// renewal to a stalled scheduler is not the difference between a live session
// and a dead one.
func sessionKeepAliveInterval(timeout time.Duration) time.Duration {
	interval := timeout / 4
	if interval <= 0 {
		return time.Millisecond
	}
	return interval
}

// mcpSessionKeepAlivePings counts the renewals this edge has issued. It is the
// keepalive's own progress made observable, so a test can wait for the edge to
// have renewed past its session timeout instead of sleeping for a duration and
// hoping.
var mcpSessionKeepAlivePings atomic.Int64

// ping sends the SDK's own liveness message for a session the edge is holding
// a stream for. `ping` is the one method the SDK answers before initialization
// as well as after, so it is renewed here without the session having to be
// anything other than alive.
//
// The request is handed straight to the streamable handler rather than
// dialled back through the edge: the connection, the port, and the auth the
// caller needed were all settled when the stream was established, and a
// self-dial would add a failure mode (a listener that is bound but not
// answering) with nothing to show for it.
func (k *sessionKeepAlive) ping(sessionID string) {
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"ping"}`, mcpSessionKeepAlivePings.Load()+1)
	ctx, cancel := context.WithTimeout(context.Background(), k.interval)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, k.path, strings.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(mcpSessionHeader, sessionID)
	k.inner.ServeHTTP(&discardResponseWriter{}, req)
	mcpSessionKeepAlivePings.Add(1)
}

// mcpSessionHeader is the wire name of the session id, spelled once here
// because the keepalive and the transport that hands the id out must agree.
const mcpSessionHeader = "Mcp-Session-Id"

// discardResponseWriter absorbs the reply to a renewal ping. The SDK answers a
// call asynchronously, so a write can land after ServeHTTP has returned; each
// ping gets its own writer, so the lock only has to keep the SDK's own write
// ordered against the header the SDK sets on it first.
type discardResponseWriter struct {
	mu     sync.Mutex
	header http.Header
}

func (w *discardResponseWriter) Header() http.Header {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

// Write reports the bytes as written, because a transport told its payload
// was dropped may retry or error; the reply is simply not kept.
func (w *discardResponseWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(p), nil
}

func (w *discardResponseWriter) WriteHeader(int) {}

func (w *discardResponseWriter) Flush() {}
