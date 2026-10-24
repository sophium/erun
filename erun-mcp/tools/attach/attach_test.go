package attach

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// attachReadDeadline tolerates a contended host (a real dtach and shell per
// scenario took up to 23s) while still failing a wedged attach.
const attachReadDeadline = 60 * time.Second

func waitForBinaryContaining(t *testing.T, conn *websocket.Conn, want string) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(attachReadDeadline))
	var accumulated strings.Builder
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for %q: %v (got so far: %q)", want, err, accumulated.String())
		}
		if kind == websocket.BinaryMessage {
			accumulated.Write(data)
			if strings.Contains(accumulated.String(), want) {
				return
			}
		}
	}
}

// A frame delayed past the old 10s deadline must still be read, without needing
// real host contention.
func TestAttachReadHelpersToleratePastThePriorDeadline(t *testing.T) {
	const priorDeadline = 10 * time.Second
	const delay = priorDeadline + 2*time.Second
	if delay >= attachReadDeadline {
		t.Fatalf("test setup: delay %s must stay below attachReadDeadline %s", delay, attachReadDeadline)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		time.Sleep(delay)
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte("SLOW_MARKER"))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	u.Scheme = "ws"
	u.Path = "/slow"

	conn, resp, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	waitForBinaryContaining(t, conn, "SLOW_MARKER")
}
