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

// sessionKeepAlive pings a session while a client holds its GET stream: the
// SDK's idle timer only counts POSTs and exposes no hook, so a client that only
// listens would otherwise be reaped mid-stream.
type sessionKeepAlive struct {
	// inner is the bare streamable handler: a renewal ping must not
	// re-authenticate, be metered, or count as activity.
	inner http.Handler
	// path is sent verbatim so the SDK's cross-origin guard sees a real path.
	path     string
	interval time.Duration
}

// withSessionKeepAlive is a no-op when the session timeout is disabled.
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

// renewSessionWhileHeld pings until the stream ends, ctx is cancelled, or stop closes.
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

// sessionKeepAliveInterval is a fraction of the timeout so one missed renewal is harmless.
func sessionKeepAliveInterval(timeout time.Duration) time.Duration {
	interval := timeout / 4
	if interval <= 0 {
		return time.Millisecond
	}
	return interval
}

// mcpSessionKeepAlivePings counts renewals so tests can wait on them.
var mcpSessionKeepAlivePings atomic.Int64

// ping sends the SDK's `ping`, valid before and after initialization, straight
// to the handler rather than dialling back through the edge.
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

const mcpSessionHeader = "Mcp-Session-Id"

// discardResponseWriter is locked because the SDK may write after ServeHTTP returns.
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

// Write reports success, since a transport told its payload was dropped may retry.
func (w *discardResponseWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(p), nil
}

func (w *discardResponseWriter) WriteHeader(int) {}

func (w *discardResponseWriter) Flush() {}
