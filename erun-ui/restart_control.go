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

// restartControlClaim is the outcome of trying to record the control marker:
// whether this process now owns the record, and — when it does not — the live
// process that does.
type restartControlClaim struct {
	// Recorded is true when the record now names this process.
	Recorded bool
	// HolderPID is the live process that already owned the record. Set only
	// when Recorded is false, and it is what the operator-visible log names
	// when it says why this instance could not take over the endpoint.
	HolderPID int
}

// claimDesktopControlMarker records marker at path, refusing to take the record
// away from a DIFFERENT process that is still alive. Ownership is what keeps a
// running desktop's control endpoint from being stranded: the record names
// whoever wrote it last, so an instance that cannot own the endpoint has to
// leave it alone rather than overwrite it — otherwise a transient second
// instance advertises itself while the running desktop is still serving, and
// every reader that resolves the desktop from this file (erun-common's
// ReadDesktopControlMarker, `erun app restart`) concludes none is running.
//
// A marker already naming this process is rewritten. A marker naming a pid that
// is no longer alive is stale — precisely what a crash leaves behind, which is
// what makes a stale record detectable at all — and is adopted.
//
// This lives with the desktop, not in erun-common, because the desktop is the
// only thing that writes a control record: the shared package owns the
// contract and the read, and one owner of the record keeps the rules for
// taking it over in one place.
func claimDesktopControlMarker(path string, marker eruncommon.DesktopControlMarker, processAlive func(int) bool) (restartControlClaim, error) {
	if path == "" {
		return restartControlClaim{}, fmt.Errorf("desktop control marker path is unset")
	}
	if marker.PID <= 0 || marker.ControlPort <= 0 {
		return restartControlClaim{}, fmt.Errorf("desktop control marker names no live target")
	}
	// A record that cannot be read as a marker at all (absent, truncated,
	// corrupt) cannot be shown to belong to a live process, so it is not one
	// this claim has to respect.
	if existing, err := eruncommon.ReadDesktopControlMarker(path); err == nil && existing.PID != marker.PID && processAlive(existing.PID) {
		return restartControlClaim{HolderPID: existing.PID}, nil
	}
	if err := writeDesktopControlMarker(path, marker); err != nil {
		return restartControlClaim{}, err
	}
	return restartControlClaim{Recorded: true}, nil
}

// writeDesktopControlMarker replaces path in one rename. The claim above reads
// the record to decide ownership, so a concurrent reader must never catch a
// half-written one and misjudge whose it is.
func writeDesktopControlMarker(path string, marker eruncommon.DesktopControlMarker) error {
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

// desktopControlMarkerTempPattern names the staging file an atomic record
// replacement uses. It is distinct from the record's own name so a reader
// resolving the record never mistakes a half-written staging file for one.
const desktopControlMarkerTempPattern = "desktop-control.json.tmp"

// removeDesktopControlMarker deletes path only while it still names pid. A
// record naming a different process belongs to whoever wrote it last: a
// predecessor quitting through a restart hand-off leaves the record to the
// successor that has already claimed it, and a transient instance that never
// owned the endpoint leaves the running desktop's record alone. Deleting one
// that is not ours would strand a desktop that is still running behind no
// record at all — the same false "nothing is running" the claim refuses to
// create.
//
// A missing file is not an error: shutdown may run twice, or the record may
// never have been written (a build with no network access to bind the control
// listener). A record that cannot be read as a marker cannot be shown to be
// ours either, so it is left where it is.
func removeDesktopControlMarker(path string, pid int) error {
	if path == "" {
		return nil
	}
	existing, err := eruncommon.ReadDesktopControlMarker(path)
	if err != nil {
		return nil
	}
	if existing.PID != pid {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// startRestartControl binds the loopback restart-trigger server and records
// how to reach it, so a CLI-triggered restart can find and verify this exact
// process before asking it to restart. A bind failure is logged and left
// without a marker (see startRestartControlServer): a desktop that cannot
// expose a restart trigger this launch still works for everything else, and an
// absent marker is exactly what an external trigger correctly reads as "no
// running desktop to restart".
func (a *App) startRestartControl() {
	server, port := startRestartControlServer(a)
	if server == nil {
		return
	}
	a.restartControl = server
	marker := eruncommon.DesktopControlMarker{PID: os.Getpid(), ControlPort: port, StartedAtUnix: time.Now().Unix()}
	claim, err := a.claimRestartControlMarker(marker)
	if err != nil {
		log.Printf("erun-app: write restart control marker: %v", err)
		return
	}
	if claim.Recorded {
		return
	}
	// A different, still-live desktop owns the endpoint. Wait for it to exit
	// instead of overwriting it: a restart spawns its successor before the
	// predecessor quits, so this process may legitimately be starting while the
	// process it is replacing is briefly still alive, and the record it leaves
	// has to end up naming the instance that outlives the hand-off.
	go a.adoptRestartControlRecord(marker, claim.HolderPID)
}

// adoptRestartControlRecord records marker once the desktop holding the record
// exits, waiting out the hand-off window first and then re-checking for as long
// as this process lives. It never gives up for good: a holder that outlives the
// window may still exit at any later time, and an adoption that stops at that
// point strands the record on a pid that has since exited — leaving it naming a
// process that is gone while the desktop actually running owns no endpoint, so
// every reader (`erun app restart`, above all) concludes no desktop is running
// and the operator has no path back to a rebuild. Re-checking costs one file
// read and one liveness probe per interval, and those are worth paying to keep
// the record pointing at the desktop that outlives the hand-off.
//
// The wait ends only on shutdown, which is the one thing that makes this
// process's endpoint unserviceable and so makes publishing it wrong.
func (a *App) adoptRestartControlRecord(marker eruncommon.DesktopControlMarker, holder int) {
	log.Printf("erun-app: restart control record is held by pid %d; waiting for it to exit", holder)
	claim := func() (restartControlClaim, error) {
		return a.claimRestartControlMarker(marker)
	}
	remaining, err := adoptRestartControl(claim, time.Sleep)
	if err == nil && remaining > 0 {
		// The tight window is over and a live process still holds the record,
		// which is the reported give-up point: from here the wait slows to a
		// steady re-check rather than ending, because the holder may exit at
		// any later time and nothing else revisits the record.
		log.Printf(
			"erun-app: restart control record still names pid %d, which is running; this desktop is not the recorded one, and keeps re-checking every %s until it can take it over",
			remaining, restartControlRetryInterval)
		err = adoptRestartControlSteadily(claim, time.Sleep, restartControlRetryInterval)
	}
	if errors.Is(err, errRestartControlReleased) {
		// This process shut down before the record was free. It never owned
		// the endpoint, so there is no takeover to report and nothing to
		// publish: the record stays with the process that wrote it.
		log.Printf("erun-app: shut down while the restart control record was held by pid %d; it stays that process's", holder)
		return
	}
	if err != nil {
		log.Printf("erun-app: adopt restart control record: %v", err)
		return
	}
	log.Printf("erun-app: took over the restart control record from pid %d", holder)
}

// errRestartControlReleased reports that this process gave up its control
// record on the way out, so an adoption still waiting on a previous owner must
// stop rather than publish again.
var errRestartControlReleased = errors.New("restart control record released")

// claimRestartControlMarker records marker as this process's control endpoint
// unless a live process already owns the record. The shutdown guard and the
// write are one critical section so an adoption waiting on a previous owner
// cannot slip an endpoint in after shutdown removed the record.
func (a *App) claimRestartControlMarker(marker eruncommon.DesktopControlMarker) (restartControlClaim, error) {
	a.restartControlMarkerMu.Lock()
	defer a.restartControlMarkerMu.Unlock()
	if a.restartControlMarkerReleased {
		return restartControlClaim{}, errRestartControlReleased
	}
	return claimDesktopControlMarker(a.deps.desktopControlMarkerPath, marker, eruncommon.DesktopProcessAlive)
}

// releaseRestartControlMarker stops an in-flight adoption and removes this
// process's record, leaving a record that names another process alone: it
// belongs to whoever wrote it last.
func (a *App) releaseRestartControlMarker() {
	a.restartControlMarkerMu.Lock()
	defer a.restartControlMarkerMu.Unlock()
	a.restartControlMarkerReleased = true
	if err := removeDesktopControlMarker(a.deps.desktopControlMarkerPath, os.Getpid()); err != nil {
		log.Printf("erun-app: remove restart control marker: %v", err)
	}
}

// A starting instance waits this many times, spaced this far apart, for a
// different live process that already owns the control record to exit before
// it gives up on recording its own endpoint. The wait is what makes a restart
// land: App.RestartApp spawns the successor before the predecessor quits, so
// the successor can start while the predecessor — a different, still-live
// process — owns the record, and may only record itself once that process is
// gone. It is bounded so two genuinely coexisting instances do not wait on
// each other forever, and it is abandoned outright on shutdown: a claim that
// landed after shutdown removed the record would republish the endpoint of a
// process that has already gone, which is the stale record this whole
// mechanism exists to keep out of the way.
const (
	restartControlClaimAttempts = 400
	restartControlClaimInterval = 50 * time.Millisecond
)

// restartControlRetryInterval is how often a desktop that has already waited
// out the hand-off window re-checks a control record another live process still
// holds. It is deliberately slower than the hand-off's own interval: that one
// is short because a restart's predecessor is normally gone within it, while
// this one covers the case where the holder outlives the window and exits at
// some unknown later time — so its job is to be cheap enough to run for the
// rest of the process's life, not to be quick.
const restartControlRetryInterval = 15 * time.Second

// adoptRestartControl keeps trying to record this process's control endpoint
// while a different live process holds the record, and returns the pid of a
// holder that outlived the wait — or 0 once the record is this process's. A
// claim refused because this process is shutting down returns
// errRestartControlReleased rather than 0: the caller must be able to tell an
// abandoned wait from a takeover, or it reports an endpoint it never owned.
//
// claim and sleep are supplied by the caller so the wait can be driven without
// a real second process or a real clock.
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

// adoptRestartControlSteadily keeps re-checking a record another live process
// holds, at a fixed interval, until this process records itself or the claim
// fails — which is how a holdout that exits long after the hand-off window is
// still taken over rather than stranding the record on a pid that has since
// exited. It returns nil only once the record is this process's, and the claim's
// own error otherwise (errRestartControlReleased on shutdown), so its caller
// reports a takeover it really made and never one it only waited for.
//
// The loop is bounded by the process's life, not by an attempt count: there is
// no later reconcile that would revisit a record this gave up on, so stopping
// short of adoption is the permanent stranding this exists to prevent. sleep
// and the claim are supplied by the caller so the wait can be driven without a
// real second process or a real clock.
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
