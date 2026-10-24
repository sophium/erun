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

// Off the default 17000 range so the fake edge never collides with a live session.
const mcpEdgeLocalPort = 26100

// startSilentPortForward accepts connections and never answers, like a stale
// kubectl port-forward.
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

// startDroppingPortForward closes every connection unanswered, like a working
// forward to a pod mid-roll.
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

// fakeMCPEdge stands in for the per-env erun-mcp server and records each request.
type fakeMCPEdge struct {
	// SSE frames replies as an event stream instead of a JSON body.
	SSE    bool
	Status int
	// AcceptEverything answers even requests with an id with a bodyless 202.
	AcceptEverything bool
	Results          map[string]string
	// ToolResults overrides Results["tools/call"] per tool name.
	ToolResults map[string]string
	RPCErrors   map[string]string
	// ForgetSessionOnce 404s the first request with a session id, like a restarted edge.
	ForgetSessionOnce   bool
	ForgetSessionAlways bool
	// DropFirstRequest closes the first connection without answering.
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
	Tool       string
	Arguments  map[string]any
	// IdleProbe reports the activity-exempt header, which only diagnostic reads
	// may set, never a call that can mutate the environment.
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
		// A progress event first proves the client skips non-reply events.
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

// Later handshakes get distinct ids so a replay is distinguishable from the dead session.
func (e *fakeMCPEdge) sessionID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.handshakes > 1 {
		return fmt.Sprintf("test-session-%d", e.handshakes)
	}
	return "test-session"
}

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

// assertToolArgumentsNameTarget asserts the call restated tenant/environment
// rather than relying on the server's bound context.
func assertToolArgumentsNameTarget(t *testing.T, request fakeMCPRequest, tenant, environment string) {
	t.Helper()
	if got, _ := request.Arguments["tenant"].(string); got != tenant {
		t.Errorf("%s call: got tenant argument %q, want %q -- arguments: %+v", request.Tool, got, tenant, request.Arguments)
	}
	if got, _ := request.Arguments["environment"].(string); got != environment {
		t.Errorf("%s call: got environment argument %q, want %q -- arguments: %+v", request.Tool, got, environment, request.Arguments)
	}
}
