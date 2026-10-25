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
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/infra/mcptools"
	"github.com/sophium/erun/erun-common/primitive/open/appsession"
	"github.com/sophium/erun/erun-mcp/tools/attach"
	"github.com/sophium/erun/erun-mcp/tools/shared/edgeauth"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

// newAttachTestRuntime uses a unique tenant/environment and creates the socket directory
// the pod's session-start script would.
func newAttachTestRuntime(t *testing.T) runtimecfg.RuntimeConfig {
	t.Helper()
	if err := os.MkdirAll(activity.RemoteAppSessionSocketDir, 0o700); err != nil {
		t.Fatalf("create session socket dir: %v", err)
	}
	return runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "attach-test", Environment: "env-" + t.Name()}}
}

// newAuthedAttachServer serves the production mux with a trusted issuer.
func newAuthedAttachServer(t *testing.T, runtime runtimecfg.RuntimeConfig, issuer, tenant string) *httptest.Server {
	t.Helper()
	trusted, err := json.Marshal(map[string]string{issuer: tenant})
	if err != nil {
		t.Fatalf("marshal trusted issuers: %v", err)
	}
	t.Setenv(edgeauth.EnvMCPTrustedIssuers, string(trusted))
	t.Setenv(edgeauth.EnvMCPTrustedIssuer, "")
	t.Setenv(edgeauth.EnvMCPAudience, "erun-mcp")
	t.Setenv(edgeauth.EnvTenant, tenant)

	server := httptest.NewServer(newHTTPHandler(model.BuildInfo{Version: "1.2.3"}, HTTPConfig{Path: "/mcp"}, withStubRuntime(runtime), nil))
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

// dialAttachViaSubprotocol authenticates only via Sec-WebSocket-Protocol, as a browser must.
func dialAttachViaSubprotocol(baseURL, session, token string) (*websocket.Conn, *http.Response, error) {
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{edgeauth.AttachAuthSubprotocol, token}
	return dialer.Dial(attachWSURL(baseURL, session), nil)
}

func writeBinary(t *testing.T, conn *websocket.Conn, s string) {
	t.Helper()
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte(s)); err != nil {
		t.Fatalf("write binary: %v", err)
	}
}

func writeControl(t *testing.T, conn *websocket.Conn, msg attach.AttachControlMessage) {
	t.Helper()
	payload, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal control message: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		t.Fatalf("write control message: %v", err)
	}
}

// attachReadDeadline tolerates a contended host (23s observed for real dtach+PTY) while
// still failing a wedged attach.
const attachReadDeadline = 60 * time.Second

// waitForAnyBinary proves the PTY produced output, so the owner file is in place.
func waitForAnyBinary(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(attachReadDeadline))
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

func readOutcomeMessage(t *testing.T, conn *websocket.Conn) attach.AttachOutcomeMessage {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(attachReadDeadline))
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for outcome message: %v", err)
		}
		if kind != websocket.TextMessage {
			continue
		}
		var msg attach.AttachOutcomeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("unmarshal outcome message %q: %v", data, err)
		}
		return msg
	}
}

// The refusal is a 403 before any subprocess starts, with no dtach socket created.
func TestAttachRefusesWithoutAttachCapabilityBeforeUpgrade(t *testing.T) {
	runtime := newAttachTestRuntime(t)
	issuer, token := identityWithScopedToken(t, string(mcptools.MCPCapabilityRead))
	server := newAuthedAttachServer(t, runtime, issuer, "acme")

	conn, resp, err := dialAttach(server.URL, "denied", token)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
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

	socket := appsession.RemoteAppSessionSocketPath(runtime.Context.Tenant, runtime.Context.Environment, "denied")
	if _, statErr := os.Stat(socket); !os.IsNotExist(statErr) {
		t.Fatalf("attach subprocess ran despite the refusal: socket %q exists (stat err %v)", socket, statErr)
	}
}

func TestAttachRefusesUnauthenticatedConnectionAtUpgrade(t *testing.T) {
	runtime := newAttachTestRuntime(t)
	issuer, _ := identityWithScopedToken(t, string(mcptools.MCPCapabilityAttach))
	server := newAuthedAttachServer(t, runtime, issuer, "acme")

	conn, resp, err := dialAttach(server.URL, "denied", "")
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
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

func TestAttachBridgesPTYAndReportsEndedOutcome(t *testing.T) {
	runtime := newAttachTestRuntime(t)
	issuer, token := identityWithScopedToken(t, string(mcptools.MCPCapabilityAttach))
	server := newAuthedAttachServer(t, runtime, issuer, "acme")

	conn, resp, err := dialAttach(server.URL, "golden", token)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("dial: %v (response %+v)", err, resp)
	}
	defer func() { _ = conn.Close() }()

	writeBinary(t, conn, "echo ATTACH_MARKER_1\n")
	waitForBinaryContaining(t, conn, "ATTACH_MARKER_1")

	writeBinary(t, conn, "exit\n")
	outcome := readOutcomeMessage(t, conn)
	if outcome.Outcome != model.AISessionAttachOutcomeEnded {
		t.Fatalf("outcome = %q, want %q", outcome.Outcome, model.AISessionAttachOutcomeEnded)
	}
}

// Nothing in the image boot path creates the session directory; the handler must.
func TestAttachCreatesSessionDirectoryOnAFreshPod(t *testing.T) {
	runtime := newAttachTestRuntime(t)
	if err := os.RemoveAll(activity.RemoteAppSessionSocketDir); err != nil {
		t.Fatalf("simulating a pod that has never run a session: %v", err)
	}
	issuer, token := identityWithScopedToken(t, string(mcptools.MCPCapabilityAttach))
	server := newAuthedAttachServer(t, runtime, issuer, "acme")

	conn, resp, err := dialAttach(server.URL, "fresh-pod", token)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("dial: %v (response %+v)", err, resp)
	}
	defer func() { _ = conn.Close() }()

	writeBinary(t, conn, "echo ATTACH_MARKER_FRESH\n")
	waitForBinaryContaining(t, conn, "ATTACH_MARKER_FRESH")

	writeBinary(t, conn, "exit\n")
	outcome := readOutcomeMessage(t, conn)
	if outcome.Outcome != model.AISessionAttachOutcomeEnded {
		t.Fatalf("outcome = %q, want %q (a fresh pod's first attach must behave identically to a warm one)",
			outcome.Outcome, model.AISessionAttachOutcomeEnded)
	}
}

func TestAttachResizeControlMessageResizesThePTY(t *testing.T) {
	runtime := newAttachTestRuntime(t)
	issuer, token := identityWithScopedToken(t, string(mcptools.MCPCapabilityAttach))
	server := newAuthedAttachServer(t, runtime, issuer, "acme")

	conn, resp, err := dialAttach(server.URL, "resize", token)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("dial: %v (response %+v)", err, resp)
	}
	defer func() { _ = conn.Close() }()

	writeControl(t, conn, attach.AttachControlMessage{Type: "resize", Cols: 100, Rows: 42})
	writeBinary(t, conn, "stty size\n")
	waitForBinaryContaining(t, conn, "42 100")

	writeBinary(t, conn, "exit\n")
	_ = readOutcomeMessage(t, conn)
}

// The server echoes the negotiated scheme, never the token (RFC 6455).
func TestAttachAuthenticatesViaSubprotocolForBrowserCallers(t *testing.T) {
	runtime := newAttachTestRuntime(t)
	issuer, token := identityWithScopedToken(t, string(mcptools.MCPCapabilityAttach))
	server := newAuthedAttachServer(t, runtime, issuer, "acme")

	conn, resp, err := dialAttachViaSubprotocol(server.URL, "browser", token)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("dial: %v (response %+v)", err, resp)
	}
	defer func() { _ = conn.Close() }()

	if got := resp.Header.Get("Sec-WebSocket-Protocol"); got != edgeauth.AttachAuthSubprotocol {
		t.Fatalf("negotiated subprotocol = %q, want %q (must never echo the token)", got, edgeauth.AttachAuthSubprotocol)
	}

	writeBinary(t, conn, "echo SUBPROTOCOL_MARKER\n")
	waitForBinaryContaining(t, conn, "SUBPROTOCOL_MARKER")

	writeBinary(t, conn, "exit\n")
	_ = readOutcomeMessage(t, conn)
}

// The evicted client is told it was taken over, and the session survives.
func TestAttachEvictionReportsTakenOverAndPreservesTheSession(t *testing.T) {
	// Eviction scans /proc; without /proc (macOS) it evicts no one by design.
	if _, err := os.Stat("/proc"); err != nil {
		t.Skip("takeover resolves the session master through /proc, which this platform does not have")
	}
	runtime := newAttachTestRuntime(t)
	issuer, token := identityWithScopedToken(t, string(mcptools.MCPCapabilityAttach))
	server := newAuthedAttachServer(t, runtime, issuer, "acme")

	connA, respA, err := dialAttach(server.URL, "shared", token)
	if respA != nil {
		defer func() { _ = respA.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("dial A: %v (response %+v)", err, respA)
	}
	defer func() { _ = connA.Close() }()
	// The owner file must exist before B attaches.
	waitForAnyBinary(t, connA)

	connB, respB, err := dialAttach(server.URL, "shared", token)
	if respB != nil {
		defer func() { _ = respB.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("dial B: %v (response %+v)", err, respB)
	}
	defer func() { _ = connB.Close() }()

	outcomeA := readOutcomeMessage(t, connA)
	if outcomeA.Outcome != model.AISessionAttachOutcomeTakenOver {
		t.Fatalf("evicted outcome = %q, want %q", outcomeA.Outcome, model.AISessionAttachOutcomeTakenOver)
	}

	writeBinary(t, connB, "echo STILL_ALIVE\n")
	waitForBinaryContaining(t, connB, "STILL_ALIVE")
	writeBinary(t, connB, "exit\n")
	_ = readOutcomeMessage(t, connB)
}
