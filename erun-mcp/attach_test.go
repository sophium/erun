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

// newAttachTestRuntime scopes a test to a unique tenant/environment pair, so
// RemoteAppSessionSocketPath resolves to a socket path no other test (or real
// environment) can collide with.
func newAttachTestRuntime(t *testing.T) RuntimeConfig {
	t.Helper()
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
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
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
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
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
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
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
