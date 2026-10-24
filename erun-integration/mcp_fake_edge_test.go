package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sophium/erun/erun-common/infra/mcp"
)

// The MCP edge scenarios pin the env's local port range to 26100 (same rule as
// the real-run open scenarios) so the fake edge never collides with a
// developer's live erun session on the default 17000 range.
const mcpEdgeLocalPort = 26100

// startSilentPortForward binds port and holds every connection open without
// ever answering it — a stale kubectl port-forward, as opposed to nothing
// listening at all (dial refused) or an edge that answers with an error. Every
// held connection is closed together on cleanup, once the listener itself
// closes and the accept loop's deferred closes run.
func startSilentPortForward(t *testing.T, port int) {
	t.Helper()
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen on 127.0.0.1:%d: %v", port, err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			defer func() { _ = conn.Close() }()
		}
	}()
}

// startDroppingPortForward binds port and closes every connection it accepts
// without writing a reply — a forward that is up whose target never took the
// call, which is what a pod mid-roll behind a working forward looks like from
// the local side. It is the shape that must not be reported as a stale
// forward: nothing about the tunnel is wrong.
func startDroppingPortForward(t *testing.T, port int) {
	t.Helper()
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen on 127.0.0.1:%d: %v", port, err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()
}

// fakeMCPEdge stands in for the per-env erun-mcp server: it speaks the
// streamable-HTTP handshake (initialize, then a 202 for the initialized
// notification, then the call) and records what each request carried, so a
// scenario can assert the client minted a bearer and propagated the session id.
type fakeMCPEdge struct {
	// SSE frames replies as an event stream instead of a plain JSON body; the
	// edge does one or the other depending on how it was configured, so both
	// framings must round-trip.
	SSE bool
	// Status, when set, answers every POST with that status instead of a reply.
	Status int
	// AcceptEverything answers even a request carrying an id with a bodyless 202,
	// the protocol anomaly that leaves a client waiting on a reply that is never
	// coming.
	AcceptEverything bool
	// Results maps a JSON-RPC method to the raw JSON result it answers with.
	Results map[string]string
	// ToolResults maps a tools/call tool name to the raw JSON result it answers
	// with, taking precedence over Results["tools/call"] for that tool. Use it
	// when one scenario's command makes more than one tools/call round trip to
	// different tools and each needs its own canned answer (e.g. starting a
	// background job, then reading its status back).
	ToolResults map[string]string
	// RPCErrors maps a JSON-RPC method to a raw JSON-RPC error object, the
	// protocol-level failure a tool-level isError does not cover.
	RPCErrors map[string]string
	// ForgetSessionOnce answers 404 for the first request carrying a session id,
	// the way an edge that restarted inside a still-running pod does; the next
	// handshake hands out a live session again.
	ForgetSessionOnce bool
	// ForgetSessionAlways never accepts a session id, so a client that keeps
	// re-handshaking would loop forever.
	ForgetSessionAlways bool
	// DropFirstRequest, when set, closes the raw connection for the very first
	// request without answering it at all — a connection that was accepted and
	// then cut, the shape a flapping local port-forward leaves an in-flight
	// request in (as opposed to AcceptEverything, which answers but with an
	// empty envelope).
	DropFirstRequest bool

	mu         sync.Mutex
	requests   []fakeMCPRequest
	handshakes int
	forgot     bool
	dropped    bool
}

type fakeMCPRequest struct {
	Method     string
	Authbearer string
	SessionID  string
	// Tool is the tool a tools/call named, which is what proves a caller reached
	// the intended surface rather than some other one.
	Tool string
	// Arguments is the tools/call arguments object the caller actually sent, so
	// a scenario can assert what a host-side command forwarded into the payload
	// (e.g. that it restated the tenant/environment it already resolved) rather
	// than only that a call reached the edge at all.
	Arguments map[string]any
	// IdleProbe reports whether the request carried the header that exempts it
	// from the environment's activity accounting — set by a diagnostic read, and
	// never by a call that can mutate the environment.
	IdleProbe bool
}

func (e *fakeMCPEdge) start(t *testing.T, port int) {
	t.Helper()
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen on 127.0.0.1:%d: %v", port, err)
	}
	server := httptest.NewUnstartedServer(e)
	_ = server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
}

// fakeMCPRPCRequest is the slice of a JSON-RPC request the fake edge reads.
type fakeMCPRPCRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"params"`
}

func (e *fakeMCPEdge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if e.DropFirstRequest && e.dropOnce() {
		dropConnection(w)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var request fakeMCPRPCRequest
	_ = json.Unmarshal(body, &request)
	e.record(r, request)

	if e.Status != 0 {
		http.Error(w, "edge refused the request", e.Status)
		return
	}
	if r.Header.Get("Mcp-Session-Id") != "" && e.forgetSession() {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if request.Method == "initialize" {
		e.newSession()
	}
	w.Header().Set("Mcp-Session-Id", e.sessionID())
	if len(request.ID) == 0 || e.AcceptEverything {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	e.writeReply(w, e.reply(request))
}

func dropConnection(w http.ResponseWriter) {
	if hijacker, ok := w.(http.Hijacker); ok {
		if conn, _, err := hijacker.Hijack(); err == nil {
			_ = conn.Close()
		}
	}
}

func (e *fakeMCPEdge) record(r *http.Request, request fakeMCPRPCRequest) {
	e.mu.Lock()
	e.requests = append(e.requests, fakeMCPRequest{
		Method:     request.Method,
		Authbearer: strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "),
		SessionID:  r.Header.Get("Mcp-Session-Id"),
		Tool:       request.Params.Name,
		Arguments:  request.Params.Arguments,
		IdleProbe:  r.Header.Get(mcp.MCPIdleProbeHeader) == "true",
	})
	e.mu.Unlock()
}

func (e *fakeMCPEdge) reply(request fakeMCPRPCRequest) string {
	if rpcError, ok := e.RPCErrors[request.Method]; ok {
		return fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":%s}`, request.ID, rpcError)
	}
	result, ok := "", false
	if request.Method == "tools/call" {
		result, ok = e.ToolResults[request.Params.Name]
	}
	if !ok {
		result, ok = e.Results[request.Method]
	}
	if !ok {
		result = "{}"
	}
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, request.ID, result)
}

func (e *fakeMCPEdge) writeReply(w http.ResponseWriter, reply string) {
	if e.SSE {
		w.Header().Set("Content-Type", "text/event-stream")
		// A progress notification ahead of the reply proves the client skips
		// non-reply events instead of failing on the first data line.
		_, _ = fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\n")
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", reply)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintln(w, reply)
}

func (e *fakeMCPEdge) dropOnce() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dropped {
		return false
	}
	e.dropped = true
	return true
}

func (e *fakeMCPEdge) forgetSession() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ForgetSessionAlways {
		return true
	}
	if e.ForgetSessionOnce && !e.forgot {
		e.forgot = true
		return true
	}
	return false
}

func (e *fakeMCPEdge) newSession() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handshakes++
}

// The first handshake keeps the historical id so the scenarios that only ever
// shake hands once stay readable; a later one hands out a distinct id, which is
// how a replay can be told apart from a request on the dead session.
func (e *fakeMCPEdge) sessionID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.handshakes > 1 {
		return fmt.Sprintf("test-session-%d", e.handshakes)
	}
	return "test-session"
}

// requestsFor returns every recorded request for a method in arrival order, so a
// scenario can prove a recovery cost exactly one extra handshake and that the
// replay went out on the new session.
func (e *fakeMCPEdge) requestsFor(method string) []fakeMCPRequest {
	matched := make([]fakeMCPRequest, 0, 2)
	for _, request := range e.recorded() {
		if request.Method == method {
			matched = append(matched, request)
		}
	}
	return matched
}

func (e *fakeMCPEdge) recorded() []fakeMCPRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]fakeMCPRequest(nil), e.requests...)
}

// requestFor returns the recorded request for a method, failing the scenario when
// the client never sent it.
func (e *fakeMCPEdge) requestFor(t *testing.T, method string) fakeMCPRequest {
	t.Helper()
	for _, request := range e.recorded() {
		if request.Method == method {
			return request
		}
	}
	t.Fatalf("edge never received %s; got %+v", method, e.recorded())
	return fakeMCPRequest{}
}

// assertToolArgumentsNameTarget asserts a tools/call request restated the
// tenant/environment the host already resolved to reach this edge, rather
// than leaving the tool to infer them from the server's own bound context
// (erun#1709). Every off-environment idle/job/activity-lease call must carry
// its own tenant/environment on the wire.
func assertToolArgumentsNameTarget(t *testing.T, request fakeMCPRequest, tenant, environment string) {
	t.Helper()
	if got, _ := request.Arguments["tenant"].(string); got != tenant {
		t.Errorf("%s call: got tenant argument %q, want %q -- arguments: %+v", request.Tool, got, tenant, request.Arguments)
	}
	if got, _ := request.Arguments["environment"].(string); got != environment {
		t.Errorf("%s call: got environment argument %q, want %q -- arguments: %+v", request.Tool, got, environment, request.Arguments)
	}
}
