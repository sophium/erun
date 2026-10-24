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

// attachReadDeadline bounds each read from the attach WebSocket. Every one of
// these reads waits for a real dtach + shell process to produce something, so
// the bound has to tolerate a busy host rather than measure it: at 10s the
// golden-path test failed inside the in-build gate, where six packages
// including this one run their own shells side by side, while the same test
// passes standalone in seconds. The same suite forks a real shell under a real
// PTY (dtach, pgrep, several /proc reads) per scenario, and each fork queues
// behind whatever else the node runs -- measured on a contended pod (~2x CPU
// oversubscription), the slowest scenario still completed within 23s, so a
// tighter bound turns ordinary scheduler contention into a spurious failure
// indistinguishable from a real hang. It stays finite so a genuinely wedged
// attach still fails its own test instead of hanging the suite.
const attachReadDeadline = 60 * time.Second

// waitForBinaryContaining reads frames, accumulating binary payloads, until
// want appears or the deadline lapses.
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

// TestAttachReadHelpersToleratePastThePriorDeadline is the regression test
// for the specific failure mode this suite hit under a contended host: every
// subprocess-spawning scenario missed the outcome message by roughly 100ms
// past a hardcoded 10s deadline -- the signature of a deadline too tight for
// the host, not of a broken bridge (confirmed separately: the same scenarios
// passed reliably once given a longer deadline under the same load). This
// test proves the fix without depending on inducing real host contention: a
// minimal websocket server with no PTY, no dtach, and no shell delays its one
// binary frame past that old boundary, and the read helper these tests
// actually use must still see it.
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

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	waitForBinaryContaining(t, conn, "SLOW_MARKER")
}
