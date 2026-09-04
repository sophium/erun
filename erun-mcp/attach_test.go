package erunmcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	eruncommon "github.com/sophium/erun/erun-common"
)

// attachTestIOTimeout bounds how long these tests wait for a PTY byte or an
// outcome message. This suite forks a real shell under a real PTY (dtach,
// pgrep, several /proc reads) for every scenario, and every one of those
// forks queues behind whatever else the host scheduler is running -- an
// agent pod is frequently sharing its node with other pods' CPU-heavy work
// (release builds, other suites) that this suite has no way to see or wait
// out. Measured on a contended pod (~2x CPU oversubscription from unrelated
// load, several repeated runs), the slowest of these scenarios still
// completed within 23s; a shorter deadline turns ordinary scheduler
// contention into a spurious failure indistinguishable from a real hang,
// which is what happened here before this constant existed. A genuine hang
// (the bridge never closing, the shell never producing output) still fails
// the test, just later.
const attachTestIOTimeout = 45 * time.Second

// newAttachTestRuntime scopes a test to a unique tenant/environment pair, so
// RemoteAppSessionSocketPath resolves to a socket path no other test (or real
// environment) can collide with. It also ensures the socket directory itself
// exists: in a deployed pod, the session-start script's own "mkdir -p" (see
// open.go) has always run before anything attaches, but these tests attach
// straight away without ever running that step, so they must establish the
// same precondition instead of depending on it already being present on the
// host running the suite.
func newAttachTestRuntime(t *testing.T) RuntimeConfig {
	t.Helper()
	if err := os.MkdirAll(eruncommon.RemoteAppSessionSocketDir, 0o700); err != nil {
		t.Fatalf("create session socket dir: %v", err)
	}
	return RuntimeConfig{Context: RuntimeContext{Tenant: "attach-test", Environment: "env-" + t.Name()}}
}

// newAuthedAttachServer serves the real production mux (newHTTPHandler,
// including registerAttachHandler) with a trusted issuer configured, so these
// tests exercise the actual wiring a deployed edge runs rather than a
// hand-built test-only mux.
func newAuthedAttachServer(t *testing.T, runtime RuntimeConfig, issuer, tenant string) *httptest.Server {
	t.Helper()
	trusted, err := json.Marshal(map[string]string{issuer: tenant})
	if err != nil {
		t.Fatalf("marshal trusted issuers: %v", err)
	}
	t.Setenv(envMCPTrustedIssuers, string(trusted))
	t.Setenv(envMCPTrustedIssuer, "")
	t.Setenv(envMCPAudience, "erun-mcp")
	t.Setenv(envTenant, tenant)

	server := httptest.NewServer(newHTTPHandler(eruncommon.BuildInfo{Version: "1.2.3"}, HTTPConfig{Path: "/mcp"}, runtime, nil))
	t.Cleanup(server.Close)
	return server
}

func attachWSURL(baseURL, session string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		panic(err)
	}
	u.Scheme = "ws"
	u.Path = "/mcp/attach/" + session
	return u.String()
}

func dialAttach(baseURL, session, token string) (*websocket.Conn, *http.Response, error) {
	header := http.Header{}
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}
	return websocket.DefaultDialer.Dial(attachWSURL(baseURL, session), header)
}

// dialAttachViaSubprotocol authenticates with no Authorization header at all --
// only the Sec-WebSocket-Protocol offer -- the one credential channel a real
// browser WebSocket client has (see auth.go's attachAuthSubprotocol).
func dialAttachViaSubprotocol(baseURL, session, token string) (*websocket.Conn, *http.Response, error) {
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{attachAuthSubprotocol, token}
	return dialer.Dial(attachWSURL(baseURL, session), nil)
}

func writeBinary(t *testing.T, conn *websocket.Conn, s string) {
	t.Helper()
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte(s)); err != nil {
		t.Fatalf("write binary: %v", err)
	}
}

func writeControl(t *testing.T, conn *websocket.Conn, msg attachControlMessage) {
	t.Helper()
	payload, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal control message: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		t.Fatalf("write control message: %v", err)
	}
}

// waitForAnyBinary blocks until the first binary frame arrives, proving the
// PTY has actually produced output (so the owner file -- written before dtach
// ever runs -- is guaranteed to already be in place).
func waitForAnyBinary(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(attachTestIOTimeout))
	for {
		kind, _, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for initial output: %v", err)
		}
		if kind == websocket.BinaryMessage {
			return
		}
	}
}

// waitForBinaryContaining reads frames, accumulating binary payloads, until
// want appears or the deadline lapses.
func waitForBinaryContaining(t *testing.T, conn *websocket.Conn, want string) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(attachTestIOTimeout))
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

func readOutcomeMessage(t *testing.T, conn *websocket.Conn) attachOutcomeMessage {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(attachTestIOTimeout))
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for outcome message: %v", err)
		}
		if kind != websocket.TextMessage {
			continue
		}
		var msg attachOutcomeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("unmarshal outcome message %q: %v", data, err)
		}
		return msg
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
	if delay >= attachTestIOTimeout {
		t.Fatalf("test setup: delay %s must stay below attachTestIOTimeout %s", delay, attachTestIOTimeout)
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

// TestAttachRefusesWithoutAttachCapabilityBeforeUpgrade is the mandatory proof
// that a caller without erun:attach is refused server-side before any
// subprocess starts: a plain HTTP 403, not merely an endpoint that "isn't
// offered", and no dtach socket ever created for the denied session.
func TestAttachRefusesWithoutAttachCapabilityBeforeUpgrade(t *testing.T) {
	runtime := newAttachTestRuntime(t)
	issuer, token := identityWithScopedToken(t, string(eruncommon.MCPCapabilityRead))
	server := newAuthedAttachServer(t, runtime, issuer, "acme")

	conn, resp, err := dialAttach(server.URL, "denied", token)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("expected the handshake to be refused, it succeeded")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		status := -1
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("status = %d, want %d", status, http.StatusForbidden)
	}

	socket := eruncommon.RemoteAppSessionSocketPath(runtime.Context.Tenant, runtime.Context.Environment, "denied")
	if _, statErr := os.Stat(socket); !os.IsNotExist(statErr) {
		t.Fatalf("attach subprocess ran despite the refusal: socket %q exists (stat err %v)", socket, statErr)
	}
}

// TestAttachRefusesUnauthenticatedConnectionAtUpgrade proves the same
// upgrade-time gate for a caller carrying no bearer token at all.
func TestAttachRefusesUnauthenticatedConnectionAtUpgrade(t *testing.T) {
	runtime := newAttachTestRuntime(t)
	issuer, _ := identityWithScopedToken(t, string(eruncommon.MCPCapabilityAttach))
	server := newAuthedAttachServer(t, runtime, issuer, "acme")

	conn, resp, err := dialAttach(server.URL, "denied", "")
	if err == nil {
		_ = conn.Close()
		t.Fatalf("expected the handshake to be refused, it succeeded")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		status := -1
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("status = %d, want %d", status, http.StatusUnauthorized)
	}
}

// TestAttachBridgesPTYAndReportsEndedOutcome is the golden path: an
// erun:attach-scoped caller can drive a real shell over the WebSocket, and
// once the shell exits on its own the server reports the "ended" outcome
// before closing.
func TestAttachBridgesPTYAndReportsEndedOutcome(t *testing.T) {
	runtime := newAttachTestRuntime(t)
	issuer, token := identityWithScopedToken(t, string(eruncommon.MCPCapabilityAttach))
	server := newAuthedAttachServer(t, runtime, issuer, "acme")

	conn, resp, err := dialAttach(server.URL, "golden", token)
	if err != nil {
		t.Fatalf("dial: %v (response %+v)", err, resp)
	}
	defer func() { _ = conn.Close() }()

	writeBinary(t, conn, "echo ATTACH_MARKER_1\n")
	waitForBinaryContaining(t, conn, "ATTACH_MARKER_1")

	writeBinary(t, conn, "exit\n")
	outcome := readOutcomeMessage(t, conn)
	if outcome.Outcome != eruncommon.AISessionAttachOutcomeEnded {
		t.Fatalf("outcome = %q, want %q", outcome.Outcome, eruncommon.AISessionAttachOutcomeEnded)
	}
}

// TestAttachCreatesSessionDirectoryOnAFreshPod is the regression test for the
// defect erun-console/playwright/tests/mcp-attach-session.spec.ts found by
// actually driving a real browser against a real emcp instance with no prior
// CLI session: session-prune.sh's own boot-time reconciliation explicitly
// no-ops when eruncommon.RemoteAppSessionSocketDir is absent ("nothing
// created yet: no sessions have ever run in this container"), and nothing
// else in the runtime image's boot path creates it either -- so a freshly
// deployed or restarted pod that has never had a CLI-driven session
// (`erun open --ai`, a linked orchestrator) has no session directory at all
// until this handler creates one itself. Before the fix this failed with
// `dtach: ...: No such file or directory` surfaced as raw shell stderr piped
// into the client's own byte stream, and a misdiagnosed "taken-over" outcome
// (the owner-file write failing open reads identically to a real rival
// claiming the socket) -- exactly the "unknown must not render as a definite
// value" property this file's own doc comment calls out.
func TestAttachCreatesSessionDirectoryOnAFreshPod(t *testing.T) {
	runtime := newAttachTestRuntime(t)
	if err := os.RemoveAll(eruncommon.RemoteAppSessionSocketDir); err != nil {
		t.Fatalf("simulating a pod that has never run a session: %v", err)
	}
	issuer, token := identityWithScopedToken(t, string(eruncommon.MCPCapabilityAttach))
	server := newAuthedAttachServer(t, runtime, issuer, "acme")

	conn, resp, err := dialAttach(server.URL, "fresh-pod", token)
	if err != nil {
		t.Fatalf("dial: %v (response %+v)", err, resp)
	}
	defer func() { _ = conn.Close() }()

	writeBinary(t, conn, "echo ATTACH_MARKER_FRESH\n")
	waitForBinaryContaining(t, conn, "ATTACH_MARKER_FRESH")

	writeBinary(t, conn, "exit\n")
	outcome := readOutcomeMessage(t, conn)
	if outcome.Outcome != eruncommon.AISessionAttachOutcomeEnded {
		t.Fatalf("outcome = %q, want %q (a fresh pod's first attach must behave identically to a warm one)",
			outcome.Outcome, eruncommon.AISessionAttachOutcomeEnded)
	}
}

// TestAttachResizeControlMessageResizesThePTY proves the resize wire message
// actually reaches the PTY: `stty size` reports the shell's own view of the
// terminal dimensions, so a mismatch here means the control message was
// dropped rather than applied.
func TestAttachResizeControlMessageResizesThePTY(t *testing.T) {
	runtime := newAttachTestRuntime(t)
	issuer, token := identityWithScopedToken(t, string(eruncommon.MCPCapabilityAttach))
	server := newAuthedAttachServer(t, runtime, issuer, "acme")

	conn, resp, err := dialAttach(server.URL, "resize", token)
	if err != nil {
		t.Fatalf("dial: %v (response %+v)", err, resp)
	}
	defer func() { _ = conn.Close() }()

	writeControl(t, conn, attachControlMessage{Type: "resize", Cols: 100, Rows: 42})
	writeBinary(t, conn, "stty size\n")
	waitForBinaryContaining(t, conn, "42 100")

	writeBinary(t, conn, "exit\n")
	_ = readOutcomeMessage(t, conn)
}

// TestAttachAuthenticatesViaSubprotocolForBrowserCallers is the golden path
// for a browser client: no Authorization header at all, only the
// Sec-WebSocket-Protocol offer, and the server must echo back the negotiated
// scheme (never the token) per RFC 6455.
func TestAttachAuthenticatesViaSubprotocolForBrowserCallers(t *testing.T) {
	runtime := newAttachTestRuntime(t)
	issuer, token := identityWithScopedToken(t, string(eruncommon.MCPCapabilityAttach))
	server := newAuthedAttachServer(t, runtime, issuer, "acme")

	conn, resp, err := dialAttachViaSubprotocol(server.URL, "browser", token)
	if err != nil {
		t.Fatalf("dial: %v (response %+v)", err, resp)
	}
	defer func() { _ = conn.Close() }()

	if got := resp.Header.Get("Sec-WebSocket-Protocol"); got != attachAuthSubprotocol {
		t.Fatalf("negotiated subprotocol = %q, want %q (must never echo the token)", got, attachAuthSubprotocol)
	}

	writeBinary(t, conn, "echo SUBPROTOCOL_MARKER\n")
	waitForBinaryContaining(t, conn, "SUBPROTOCOL_MARKER")

	writeBinary(t, conn, "exit\n")
	_ = readOutcomeMessage(t, conn)
}

// TestAttachEvictionReportsTakenOverAndPreservesTheSession is the other
// mandatory proof: a second attach to the same session id evicts the first,
// the evicted client is told plainly that it was taken over (never a silent
// disconnect indistinguishable from a network stall), and the session itself
// survives for the new attach to keep driving.
func TestAttachEvictionReportsTakenOverAndPreservesTheSession(t *testing.T) {
	runtime := newAttachTestRuntime(t)
	issuer, token := identityWithScopedToken(t, string(eruncommon.MCPCapabilityAttach))
	server := newAuthedAttachServer(t, runtime, issuer, "acme")

	connA, respA, err := dialAttach(server.URL, "shared", token)
	if err != nil {
		t.Fatalf("dial A: %v (response %+v)", err, respA)
	}
	defer func() { _ = connA.Close() }()
	// Wait for A's own initial output so the owner file (written before
	// dtach ever runs) is guaranteed in place before B attaches and evicts it.
	waitForAnyBinary(t, connA)

	connB, respB, err := dialAttach(server.URL, "shared", token)
	if err != nil {
		t.Fatalf("dial B: %v (response %+v)", err, respB)
	}
	defer func() { _ = connB.Close() }()

	outcomeA := readOutcomeMessage(t, connA)
	if outcomeA.Outcome != eruncommon.AISessionAttachOutcomeTakenOver {
		t.Fatalf("evicted outcome = %q, want %q", outcomeA.Outcome, eruncommon.AISessionAttachOutcomeTakenOver)
	}

	writeBinary(t, connB, "echo STILL_ALIVE\n")
	waitForBinaryContaining(t, connB, "STILL_ALIVE")
	writeBinary(t, connB, "exit\n")
	_ = readOutcomeMessage(t, connB)
}
