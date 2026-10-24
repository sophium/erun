package attach

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/mcptools"
	"github.com/sophium/erun/erun-common/primitive/open/appsession"
	"github.com/sophium/erun/erun-mcp/tools/shared/activityrec"
	"github.com/sophium/erun/erun-mcp/tools/shared/callerid"
	"github.com/sophium/erun/erun-mcp/tools/shared/edgeauth"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
)

// MCP is request/response, so a live PTY gets a WebSocket beside it. The
// attach script runs as a local subprocess: emcp is in the pod holding the
// dtach socket, so no pods/exec grant is needed.

// attachToolName names this edge in audit logs and capability errors.
const attachToolName = "attach"

// attachRedraw forces a full repaint, since a mobile client's screen starts blank on reconnect.
const attachRedraw = "winch"

// attachDefaultLaunchCommand only runs when the dtach socket does not exist yet.
const attachDefaultLaunchCommand = "/bin/bash"

const (
	attachDefaultCols = 80
	attachDefaultRows = 24
)

// AttachControlMessage is the only text frame a client sends; keystrokes are
// binary frames, so typed text can never be read as a control message.
type AttachControlMessage struct {
	Type string `json:"type"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

// AttachOutcomeMessage is sent once before close so the client learns why the
// stream ended; an unknown outcome is sent as unknown, never as ended.
type AttachOutcomeMessage struct {
	Type    string                       `json:"type"`
	Outcome model.AISessionAttachOutcome `json:"outcome"`
}

// RegisterAttachHandler serves the WebSocket attach edge at path behind bearer
// auth; the erun:attach capability is checked before Upgrade.
func RegisterAttachHandler(mux *http.ServeMux, path string, authCfg edgeauth.McpAuthConfig, runtime runtimecfg.RuntimeConfig) {
	mux.Handle("GET "+path, edgeauth.WsAttachAuthHTTPMiddleware(authCfg, attachHTTPHandler(runtime)))
}

var attachUpgrader = websocket.Upgrader{
	// The bearer token is the gate; native clients have no Origin to check.
	CheckOrigin: func(*http.Request) bool { return true },
	// Echo the auth subprotocol scheme, never the token (RFC 6455).
	Subprotocols: []string{edgeauth.AttachAuthSubprotocol},
}

func attachHTTPHandler(runtime runtimecfg.RuntimeConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := callerid.AuthIdentityFrom(r.Context())
		if !identity.Capabilities.Allows(mcptools.MCPCapabilityAttach) {
			callerid.AuditToolDecision(identity, attachToolName, false)
			edgeauth.WriteForbidden(w, fmt.Sprintf("attach requires the %s capability", mcptools.MCPCapabilityAttach))
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
		callerid.AuditToolDecision(identity, attachToolName, true)
		activityrec.RecordRuntimeActivity(runtime, config.ActivityKindMCP, false)
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

// runAttachSession runs dtach under a real PTY (dtach needs a controlling
// terminal), bridges bytes, and reports the outcome before closing.
func runAttachSession(conn *websocket.Conn, runtime runtimecfg.RuntimeConfig, session string, cols, rows int) {
	defer func() { _ = conn.Close() }()

	socket := appsession.RemoteAppSessionSocketPath(runtime.Context.Tenant, runtime.Context.Environment, session)
	// Nothing in the image creates the session directory, and this edge can be a
	// fresh pod's first attach.
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		log.Printf("erun-mcp attach: creating session directory for %q: %v", session, err)
		sendAttachOutcome(conn, model.AISessionAttachOutcomeUnknown)
		return
	}
	script := strings.Join(appsession.RemoteAppSessionAttachLines(socket, attachRedraw, attachDefaultLaunchCommand), "\n")
	cmd := exec.Command("sh", "-c", script)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		log.Printf("erun-mcp attach: starting session %q: %v", session, err)
		sendAttachOutcome(conn, model.AISessionAttachOutcomeUnknown)
		return
	}
	defer func() { _ = ptmx.Close() }()

	ptyExited := make(chan struct{})
	go pumpPTYToWebSocket(conn, ptmx, cmd, ptyExited)

	pumpWebSocketToPTY(conn, ptmx)
	// Kill the whole process group so an orphaned `dtach -A` client cannot hold the
	// PTY open; this detaches only the local client.
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
	sendAttachOutcome(conn, appsession.ResolveAISessionAttachOutcome(exitCode, known))
	// The session ended on its own; unblock the WS->PTY pump.
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
	var msg AttachControlMessage
	if err := json.Unmarshal(raw, &msg); err != nil || msg.Type != "resize" {
		return
	}
	if msg.Cols <= 0 || msg.Rows <= 0 {
		return
	}
	_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(msg.Cols), Rows: uint16(msg.Rows)})
}

func sendAttachOutcome(conn *websocket.Conn, outcome model.AISessionAttachOutcome) {
	payload, err := json.Marshal(AttachOutcomeMessage{Type: "outcome", Outcome: outcome})
	if err != nil {
		return
	}
	_ = conn.WriteMessage(websocket.TextMessage, payload)
}

// attachExitCode reaps cmd once; a signal-killed process resolves to unknown,
// never Ended.
func attachExitCode(cmd *exec.Cmd) (code int, known bool) {
	_ = cmd.Wait()
	if cmd.ProcessState == nil {
		return 0, false
	}
	return cmd.ProcessState.ExitCode(), cmd.ProcessState.Exited()
}
