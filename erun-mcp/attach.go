package erunmcp

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	eruncommon "github.com/sophium/erun/erun-common"
)

// The MCP transport (erun-mcp/server.go's mcp.NewStreamableHTTPHandler) is
// JSON-RPC request/response, not a duplex byte stream, so a live PTY needs a
// second surface: a WebSocket sitting beside it in the same authenticated
// edge. This file is that surface. It execs eruncommon.RemoteAppSessionAttachLines'
// script as a local subprocess of this pod -- never a Kubernetes API call --
// because the whole point of putting this here is that `emcp` already runs
// inside the pod holding the dtach socket, so reaching it needs no
// `pods/exec` grant at all.

// attachToolName names this surface in the audit log and capability-denial
// error, the same way every registered MCP tool name does, even though it is
// not registered as an MCP tool and has no JSON-RPC schema of its own.
const attachToolName = "attach"

// attachRedraw always forces a full repaint on connect. A mobile client's
// screen starts blank on every reconnect -- unlike a desktop tab, which may
// only have briefly lost focus -- so the quieter ctrl_l trigger open.go's
// plain shell tabs use would leave stale or partial content on screen.
const attachRedraw = "winch"

// attachDefaultLaunchCommand only runs the first time a given session id's
// dtach socket is created; the overwhelmingly common case is reattaching to a
// socket a desktop tab or `erun open --ai` already started, which ignores
// this and reattaches to whatever is already running. A plain shell is the
// only sane default for the create-from-scratch case, since a WebSocket
// client carries no AITool/Claude config of its own to build a launch
// command from the way `erun open` does.
const attachDefaultLaunchCommand = "/bin/bash"

const (
	attachDefaultCols = 80
	attachDefaultRows = 24
)

// attachControlMessage is the one WebSocket text-frame shape a client may
// send. Every other client->server frame is binary, carrying raw keystroke
// bytes written straight to the PTY -- JSON control and terminal data never
// share a frame type, so a client can never accidentally inject a resize by
// typing JSON-looking text at the shell.
type attachControlMessage struct {
	Type string `json:"type"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

// attachOutcomeMessage is the one text-frame shape the server sends, exactly
// once, immediately before it closes the socket. It is how an evicted or
// ended client learns *why* the byte stream stopped, instead of reading a
// silent close as indistinguishable from a network stall -- the property
// eruncommon.ResolveAISessionAttachOutcome exists to compute, and root
// AGENTS.md's "an unknown must not render as a definite value" rule applies
// to this wire message exactly as it does to that function's own return
// value: AISessionAttachOutcomeUnknown is a real, sent value here, never
// silently upgraded to Ended.
type attachOutcomeMessage struct {
	Type    string                            `json:"type"`
	Outcome eruncommon.AISessionAttachOutcome `json:"outcome"`
}

// registerAttachHandler wires the WebSocket attach edge into mux at path
// (e.g. "/mcp/attach/{session}"), behind the same bearer-token auth
// middleware the JSON-RPC path uses. Auth resolves the caller's identity into
// the request context; attachHTTPHandler re-checks the erun:attach capability
// itself before ever calling Upgrade, so a caller who fails that check is
// refused with a plain 403 and no subprocess starts -- refused, not merely
// "not offered the endpoint".
func registerAttachHandler(mux *http.ServeMux, path string, authCfg mcpAuthConfig, runtime RuntimeConfig) {
	mux.Handle("GET "+path, wsAttachAuthHTTPMiddleware(authCfg, attachHTTPHandler(runtime)))
}

var attachUpgrader = websocket.Upgrader{
	// The real gate is the bearer token wsAttachAuthHTTPMiddleware already
	// verified before this handler runs, not the WebSocket handshake's Origin
	// header -- a native mobile client has no browser Origin to check in the
	// first place. Accepting every origin here does not widen access: a
	// request that reaches Upgrade has already cleared the same capability
	// check every MCP tool call goes through.
	CheckOrigin: func(*http.Request) bool { return true },
	// Subprotocols declares the one scheme this edge understands, so a client
	// that offered [attachAuthSubprotocol, token] (auth.go's browser fallback)
	// gets that scheme echoed back per RFC 6455 -- never the token itself. A
	// client that authenticated via a plain Authorization header offers no
	// subprotocol at all, and this negotiates to nothing for it, exactly as
	// before this field existed.
	Subprotocols: []string{attachAuthSubprotocol},
}

// attachHTTPHandler bridges one WebSocket connection to this environment's
// own dtach takeover contract for the lifetime of that connection.
func attachHTTPHandler(runtime RuntimeConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := authIdentityFrom(r.Context())
		if !identity.Capabilities.Allows(eruncommon.MCPCapabilityAttach) {
			auditToolDecision(identity, attachToolName, false)
			writeForbidden(w, fmt.Sprintf("attach requires the %s capability", eruncommon.MCPCapabilityAttach))
			return
		}
		session := strings.TrimSpace(r.PathValue("session"))
		if session == "" {
			http.Error(w, "attach: session id is required", http.StatusBadRequest)
			return
		}
		cols, rows := attachInitialSize(r)

		conn, err := attachUpgrader.Upgrade(w, r, nil)
		if err != nil {
			// Upgrade already wrote its own HTTP error response.
			return
		}
		auditToolDecision(identity, attachToolName, true)
		recordRuntimeActivity(runtime, eruncommon.ActivityKindMCP, false)
		runAttachSession(conn, runtime, session, cols, rows)
	})
}

func attachInitialSize(r *http.Request) (cols, rows int) {
	cols, rows = attachDefaultCols, attachDefaultRows
	if v, err := strconv.Atoi(r.URL.Query().Get("cols")); err == nil && v > 0 {
		cols = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("rows")); err == nil && v > 0 {
		rows = v
	}
	return cols, rows
}

// runAttachSession owns one WebSocket connection end to end: start the local
// dtach-takeover subprocess under a real PTY (dtach needs a controlling
// terminal to manage; a plain pipe will not do), bridge bytes in both
// directions, and report the classified outcome before closing.
func runAttachSession(conn *websocket.Conn, runtime RuntimeConfig, session string, cols, rows int) {
	defer func() { _ = conn.Close() }()

	socket := eruncommon.RemoteAppSessionSocketPath(runtime.Context.Tenant, runtime.Context.Environment, session)
	script := strings.Join(eruncommon.RemoteAppSessionAttachLines(socket, attachRedraw, attachDefaultLaunchCommand), "\n")
	cmd := exec.Command("sh", "-c", script)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		log.Printf("erun-mcp attach: starting session %q: %v", session, err)
		sendAttachOutcome(conn, eruncommon.AISessionAttachOutcomeUnknown)
		return
	}
	defer func() { _ = ptmx.Close() }()

	ptyExited := make(chan struct{})
	go pumpPTYToWebSocket(conn, ptmx, cmd, ptyExited)

	pumpWebSocketToPTY(conn, ptmx)
	// The client side of the bridge stopped -- either the client hung up, or
	// a write to the PTY failed because the process is already gone. Kill the
	// whole process group (not just `sh`), so an orphaned `dtach -A` client
	// cannot linger holding the PTY slave open: this only ever detaches the
	// local attach client, never the session's own master, exactly like
	// RemoteAppSessionAttachLines' own eviction path detaches other viewers.
	killAttachProcessGroup(cmd)
	<-ptyExited
}

func pumpPTYToWebSocket(conn *websocket.Conn, ptmx *os.File, cmd *exec.Cmd, done chan<- struct{}) {
	defer close(done)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := ptmx.Read(buf)
		if n > 0 {
			if writeErr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); writeErr != nil {
				break
			}
		}
		if readErr != nil {
			break
		}
	}
	exitCode, known := attachExitCode(cmd)
	sendAttachOutcome(conn, eruncommon.ResolveAISessionAttachOutcome(exitCode, known))
	// A WS->PTY pump still blocked waiting for client input has nothing more
	// coming: the session ended on its own (taken over, deploy-reattach, or
	// the program exited) and the client may never send another byte, so it
	// must not wait for one forever.
	_ = conn.Close()
}

func pumpWebSocketToPTY(conn *websocket.Conn, ptmx *os.File) {
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		switch kind {
		case websocket.BinaryMessage:
			if _, writeErr := ptmx.Write(data); writeErr != nil {
				return
			}
		case websocket.TextMessage:
			applyAttachControlMessage(ptmx, data)
		}
	}
}

func applyAttachControlMessage(ptmx *os.File, raw []byte) {
	var msg attachControlMessage
	if err := json.Unmarshal(raw, &msg); err != nil || msg.Type != "resize" {
		return
	}
	if msg.Cols <= 0 || msg.Rows <= 0 {
		return
	}
	_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(msg.Cols), Rows: uint16(msg.Rows)})
}

func sendAttachOutcome(conn *websocket.Conn, outcome eruncommon.AISessionAttachOutcome) {
	payload, err := json.Marshal(attachOutcomeMessage{Type: "outcome", Outcome: outcome})
	if err != nil {
		return
	}
	_ = conn.WriteMessage(websocket.TextMessage, payload)
}

// attachExitCode calls cmd.Wait() -- the single, one-time reap for the
// attach subprocess regardless of which path (natural exit, eviction exit
// 76, or our own defensive kill) got it there -- and reports whether the
// exit status is actually known. A signal-killed or otherwise unreaped
// process must resolve to unknown, never to a guessed Ended.
func attachExitCode(cmd *exec.Cmd) (code int, known bool) {
	_ = cmd.Wait()
	if cmd.ProcessState == nil {
		return 0, false
	}
	return cmd.ProcessState.ExitCode(), cmd.ProcessState.Exited()
}
