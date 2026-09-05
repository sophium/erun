package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// restartControlServer is the loopback listener a CLI-triggered restart talks
// to. It exposes exactly one operation — call this process's own RestartApp —
// rather than the full method surface headlessserver exposes for the
// Playwright harness: a general RPC surface on every real desktop launch would
// be a materially bigger trust boundary than this one feature needs.
//
// It exists because RestartApp's correctness depends on state only the live
// App holds (which conversation is actually running for an orchestrator right
// now — see orchestrator.go's runningOrchestratorConversation): nothing
// outside this process can reconstruct that, so a CLI trigger cannot
// restart the desktop itself; it can only ask this server to do it. Composing
// this way — one HTTP hop into the exact method the button calls — means the
// button and the trigger share the one restart mechanism rather than growing
// a second one beside it (root AGENTS.md: "Do not build a second restart path
// beside the first").
type restartControlServer struct {
	listener net.Listener
	server   *http.Server
}

// restartControlRequest/restartControlResponse mirror
// eruncommon.desktopRestartRequest/desktopRestartResponse; kept as unexported
// local types (rather than exported shared ones) because erun-common must not
// depend on this package and the wire shape has exactly two callers, one on
// each side of the loopback call.
type restartControlRequest struct {
	OrchestratorID string `json:"orchestratorId"`
}

type restartControlResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// startRestartControlServer binds a loopback listener on an OS-assigned port
// and serves RestartApp behind it. Returns a nil server and port 0 when the
// bind fails (e.g. no loopback interface in a sandboxed test), which the
// caller treats as "no control server this launch" rather than a startup
// failure — a desktop with no programmatic restart trigger available is still
// a working desktop.
func startRestartControlServer(app *App) (*restartControlServer, int) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Printf("erun-app: restart control server disabled: %v", err)
		return nil, 0
	}
	mux := http.NewServeMux()
	mux.HandleFunc(eruncommon.DesktopControlPath, handleRestartControl(app))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("erun-app: restart control server: %v", err)
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	return &restartControlServer{listener: listener, server: srv}, port
}

// handleRestartControl calls app.RestartApp exactly once per request and
// reports the outcome plainly: root AGENTS.md's "Smooth, Seamless, No Dead
// Ends" forbids an action that either silently does nothing or is
// indistinguishable from one that did.
func handleRestartControl(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req restartControlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := restartControlResponse{OK: true}
		if err := app.RestartApp(req.OrchestratorID); err != nil {
			resp = restartControlResponse{OK: false, Error: err.Error()}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// Close shuts the listener down. Safe to call on a nil server (no control
// server this launch) or more than once.
func (s *restartControlServer) Close() {
	if s == nil || s.server == nil {
		return
	}
	_ = s.server.Close()
}
