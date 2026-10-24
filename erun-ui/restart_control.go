package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

// restartControlServer is the loopback listener a CLI-triggered restart talks
// to. It exposes only RestartApp, keeping the trust boundary minimal; only the
// live App knows which conversations to reopen, so the CLI must ask it.
type restartControlServer struct {
	listener net.Listener
	server   *http.Server
}

// startRestartControlServer serves RestartApp on a loopback port. A bind
// failure returns nil and port 0: the desktop still works without the trigger.
func startRestartControlServer(app *App) (*restartControlServer, int) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Printf("erun-app: restart control server disabled: %v", err)
		return nil, 0
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		log.Printf("erun-app: restart control server disabled: listener address %v is not TCP", listener.Addr())
		return nil, 0
	}
	mux := http.NewServeMux()
	mux.HandleFunc(core.DesktopControlPath, handleRestartControl(app))
	mux.HandleFunc(core.DesktopControlPlanPath, handleRestartPlanControl(app))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("erun-app: restart control server: %v", err)
		}
	}()
	return &restartControlServer{listener: listener, server: srv}, addr.Port
}

// handleRestartControl calls app.RestartApp once per request and reports the outcome.
func handleRestartControl(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeRestartControlRequest(w, r)
		if !ok {
			return
		}
		resp := core.DesktopRestartResponse{OK: true}
		if err := app.RestartApp(req.OrchestratorID); err != nil {
			resp = core.DesktopRestartResponse{OK: false, Error: err.Error()}
		}
		writeRestartControlResponse(w, resp)
	}
}

// handleRestartPlanControl reports what a restart would reopen without doing it.
// It has its own path so an older desktop cannot read the question as a command.
func handleRestartPlanControl(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeRestartControlRequest(w, r)
		if !ok {
			return
		}
		writeRestartControlResponse(w, core.DesktopRestartResponse{
			OK:      true,
			Preview: app.orchestratorRestartPreview(req.OrchestratorID),
		})
	}
}

// decodeRestartControlRequest answers the client itself when it cannot decode.
func decodeRestartControlRequest(w http.ResponseWriter, r *http.Request) (core.DesktopRestartRequest, bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return core.DesktopRestartRequest{}, false
	}
	var req core.DesktopRestartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return core.DesktopRestartRequest{}, false
	}
	return req, true
}

func writeRestartControlResponse(w http.ResponseWriter, resp core.DesktopRestartResponse) {
	w.Header().Set("Content-Type", "application/json")
	// A failed write means the caller already hung up: nobody is left to tell.
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return
	}
}

// Close shuts the listener down. Safe on a nil server and when called twice.
func (s *restartControlServer) Close() {
	if s == nil || s.server == nil {
		return
	}
	_ = s.server.Close()
}

// restartControlClaim is the outcome of trying to record the control marker.
type restartControlClaim struct {
	// Recorded is true when the record now names this process.
	Recorded bool
	// HolderPID is the live owner; set only when Recorded is false.
	HolderPID int
}

// claimDesktopControlMarker records marker at path unless a different live
// process owns it; overwriting would make readers think the running desktop is
// gone. A marker naming a dead pid is stale (a crash) and is adopted.
func claimDesktopControlMarker(path string, marker core.DesktopControlMarker, processAlive func(int) bool) (restartControlClaim, error) {
	if path == "" {
		return restartControlClaim{}, fmt.Errorf("desktop control marker path is unset")
	}
	if marker.PID <= 0 || marker.ControlPort <= 0 {
		return restartControlClaim{}, fmt.Errorf("desktop control marker names no live target")
	}
	// An unreadable record cannot be shown to belong to a live process.
	if existing, err := core.ReadDesktopControlMarker(path); err == nil && existing.PID != marker.PID && processAlive(existing.PID) {
		return restartControlClaim{HolderPID: existing.PID}, nil
	}
	if err := writeDesktopControlMarker(path, marker); err != nil {
		return restartControlClaim{}, err
	}
	return restartControlClaim{Recorded: true}, nil
}

// writeDesktopControlMarker replaces path atomically so a concurrent claim
// never reads a half-written record.
func writeDesktopControlMarker(path string, marker core.DesktopControlMarker) error {
	data, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, desktopControlMarkerTempPattern)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

const desktopControlMarkerTempPattern = "desktop-control.json.tmp"

// removeDesktopControlMarker deletes path only while it still names pid: a
// record naming another process belongs to it, and deleting it would strand
// a running desktop. Missing or unreadable records are left alone.
func removeDesktopControlMarker(path string, pid int) error {
	if path == "" {
		return nil
	}
	if !desktopControlMarkerOwnedBy(path, pid) {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func desktopControlMarkerOwnedBy(path string, pid int) bool {
	existing, err := core.ReadDesktopControlMarker(path)
	return err == nil && existing.PID == pid
}

// startRestartControl binds the restart trigger and records how to reach it.
// On bind failure no marker is written, which readers take as "not running".
func (a *App) startRestartControl() {
	server, port := startRestartControlServer(a)
	if server == nil {
		return
	}
	a.restartControl = server
	marker := core.DesktopControlMarker{PID: os.Getpid(), ControlPort: port, StartedAtUnix: time.Now().Unix()}
	claim, err := a.claimRestartControlMarker(marker)
	if err != nil {
		log.Printf("erun-app: write restart control marker: %v", err)
		return
	}
	if claim.Recorded {
		return
	}
	// A restart spawns its successor before the predecessor quits, so wait for
	// the holder to exit instead of overwriting its record.
	go a.adoptRestartControlRecord(marker, claim.HolderPID)
}

// adoptRestartControlRecord records marker once the holder exits, re-checking
// until shutdown: giving up would strand the record on a dead pid.
func (a *App) adoptRestartControlRecord(marker core.DesktopControlMarker, holder int) {
	log.Printf("erun-app: restart control record is held by pid %d; waiting for it to exit", holder)
	claim := func() (restartControlClaim, error) {
		return a.claimRestartControlMarker(marker)
	}
	remaining, err := adoptRestartControl(claim, time.Sleep)
	if err == nil && remaining > 0 {
		log.Printf(
			"erun-app: restart control record still names pid %d, which is running; this desktop is not the recorded one, and keeps re-checking every %s until it can take it over",
			remaining, restartControlRetryInterval)
		err = adoptRestartControlSteadily(claim, time.Sleep, restartControlRetryInterval)
	}
	if errors.Is(err, errRestartControlReleased) {
		// Shut down before the record was free; it never owned the endpoint.
		log.Printf("erun-app: shut down while the restart control record was held by pid %d; it stays that process's", holder)
		return
	}
	if err != nil {
		log.Printf("erun-app: adopt restart control record: %v", err)
		return
	}
	log.Printf("erun-app: took over the restart control record from pid %d", holder)
}

// errRestartControlReleased reports that this process released its record on shutdown.
var errRestartControlReleased = errors.New("restart control record released")

// claimRestartControlMarker holds the lock across the shutdown check and the
// write so an adoption cannot republish after shutdown removed the record.
func (a *App) claimRestartControlMarker(marker core.DesktopControlMarker) (restartControlClaim, error) {
	a.restartControlMarkerMu.Lock()
	defer a.restartControlMarkerMu.Unlock()
	if a.restartControlMarkerReleased {
		return restartControlClaim{}, errRestartControlReleased
	}
	return claimDesktopControlMarker(a.deps.desktopControlMarkerPath, marker, core.DesktopProcessAlive)
}

// releaseRestartControlMarker stops any in-flight adoption and removes this
// process's record.
func (a *App) releaseRestartControlMarker() {
	a.restartControlMarkerMu.Lock()
	defer a.restartControlMarkerMu.Unlock()
	a.restartControlMarkerReleased = true
	if err := removeDesktopControlMarker(a.deps.desktopControlMarkerPath, os.Getpid()); err != nil {
		log.Printf("erun-app: remove restart control marker: %v", err)
	}
}

// Bounds the wait for a predecessor to exit during a restart hand-off.
const (
	restartControlClaimAttempts = 400
	restartControlClaimInterval = 50 * time.Millisecond
)

// restartControlRetryInterval paces the cheap lifelong re-check once the
// hand-off window has passed.
const restartControlRetryInterval = 15 * time.Second

// adoptRestartControl returns the pid of a holder that outlived the wait, or 0
// once the record is ours; errRestartControlReleased distinguishes shutdown.
func adoptRestartControl(claim func() (restartControlClaim, error), sleep func(time.Duration)) (int, error) {
	var result restartControlClaim
	for attempt := 0; attempt < restartControlClaimAttempts; attempt++ {
		if attempt > 0 {
			sleep(restartControlClaimInterval)
		}
		claimResult, err := claim()
		if err != nil {
			return 0, err
		}
		result = claimResult
		if result.Recorded {
			return 0, nil
		}
	}
	return result.HolderPID, nil
}

// adoptRestartControlSteadily re-checks at interval until the record is ours
// or the claim fails. It never gives up: nothing else revisits the record.
func adoptRestartControlSteadily(claim func() (restartControlClaim, error), sleep func(time.Duration), interval time.Duration) error {
	for {
		sleep(interval)
		result, err := claim()
		if err != nil {
			return err
		}
		if result.Recorded {
			return nil
		}
	}
}
