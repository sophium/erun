package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	goruntime "runtime"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func (a *App) SendSessionInput(sessionID int, data string) error {
	if data == "" {
		return nil
	}

	a.mu.Lock()
	managed := a.sessionBySerialLocked(sessionID)
	a.mu.Unlock()
	if managed == nil || managed.session == nil {
		return nil
	}

	if _, err := io.WriteString(managed.session, data); err != nil {
		return err
	}
	a.noteSessionInput(managed)
	a.clearAwaitingPostRespawnInput(managed)
	a.recordTerminalActivity(managed.selection)
	if id, ok := orchestratorIDFromSessionKey(managed.key); ok {
		// Real operator input into the pane is the other rearm the pacing
		// nudge cap names: the operator is plainly at the keyboard, whatever
		// the last report said.
		a.rearmOrchestratorPacing(id)
	}
	return nil
}

func (a *App) clearAwaitingPostRespawnInput(managed *managedTerminal) {
	if managed == nil {
		return
	}
	a.mu.Lock()
	managed.awaitingPostRespawnInput = false
	a.mu.Unlock()
}

func (a *App) isAwaitingPostRespawnInput(managed *managedTerminal) bool {
	if managed == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return managed.awaitingPostRespawnInput
}

func (a *App) recordTerminalActivity(selection uiSelection) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" || a.deps.recordActivity == nil {
		return
	}
	_ = a.deps.recordActivity(activity.EnvironmentActivityParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		Kind:        configpkg.ActivityKindCLI,
	})
}

// SavePastedFile copies a file pasted into the desktop terminal into the env's
// runtime pod and returns its in-pod path. Any file type is accepted, not just
// images.
func (a *App) SavePastedFile(sessionID int, payload pastedFilePayload) (pastedFileResult, error) {
	data, mimeType, err := decodePastedFilePayload(payload)
	if err != nil {
		return pastedFileResult{}, err
	}

	a.mu.Lock()
	managed := a.sessionBySerialLocked(sessionID)
	a.mu.Unlock()
	if managed == nil || managed.session == nil {
		return pastedFileResult{}, fmt.Errorf("no active terminal session")
	}

	result, err := open.ResolveOpen(a.deps.store, shared.OpenParams{
		Tenant:      managed.selection.Tenant,
		Environment: managed.selection.Environment,
	})
	if err != nil {
		return pastedFileResult{}, err
	}

	path, err := a.deps.savePastedFile(pastedFileSaveParams{
		Result:   result,
		Data:     data,
		MIMEType: mimeType,
		Name:     payload.Name,
	})
	if err != nil {
		return pastedFileResult{}, err
	}
	return pastedFileResult{Path: path}, nil
}

func (a *App) ResizeSession(sessionID, cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}

	a.mu.Lock()
	managed := a.sessionBySerialLocked(sessionID)
	if managed != nil {
		managed.lastCols = cols
		managed.lastRows = rows
	}
	a.mu.Unlock()
	if managed == nil || managed.session == nil {
		return nil
	}

	return managed.session.Resize(cols, rows)
}

// RepaintSession forces the session's program to fully repaint by raising a real
// WINCH (shrink one row, then restore) on its backend pty. Switching to a tab
// replays the retained buffer locally, but an Ink/alt-screen TUI (Claude) only
// repaints on a geometry change, so a same-size switch would leave the tab blank
// until the app next emits a diff on its own. Unlike maybeNudgeAIRepaint this is
// deliberately NOT gated by repaintNudged (that guard is for the once-per-attach
// streaming path — every alt-screen switch wants a fresh repaint); the frontend
// calls it only for alt-screen sessions. The local xterm is never resized, so
// the user sees the frame appear with no visible reflow.
func (a *App) RepaintSession(sessionID int) error {
	a.mu.Lock()
	managed := a.sessionBySerialLocked(sessionID)
	var cols, rows int
	var gen uint64
	var typedRecently bool
	if managed != nil {
		cols, rows = managed.lastCols, managed.lastRows
		gen = managed.inputGen
		typedRecently = typedRecentlyLocked(managed)
	}
	a.mu.Unlock()
	if managed == nil || managed.session == nil {
		return nil
	}
	if typedRecently {
		// Switching into a pane fires this on every switch, so it is the other
		// half of #1330: a switch-and-type sequence would resize the pty under
		// a line being entered. Someone typing needs no synthetic repaint.
		return nil
	}
	// Only main-screen TUIs (claude/codex, whether in an AI tab or an
	// orchestrator pane) need the WINCH repaint: they only repaint on a real
	// geometry change, so a tab switch leaves them blank until their next diff.
	// Plain shells and alt-screen apps reconstruct from the replayed buffer, so a
	// nudge would just cause a needless reflow. The frontend fires this on every
	// switch and lets this gate decide.
	if !needsWINCHRepaint(managed) {
		return nil
	}
	// No attach delay on a switch: the program is already attached (unlike the
	// attach-marker path, which must wait for dtach to reattach first).
	go a.nudgeAIRepaint(managed, cols, rows, 0, gen)
	return nil
}

func decodePastedFilePayload(payload pastedFilePayload) ([]byte, string, error) {
	value := strings.TrimSpace(payload.Data)
	mimeType := strings.TrimSpace(payload.MIMEType)
	if strings.HasPrefix(value, "data:") {
		header, body, ok := strings.Cut(value, ",")
		if !ok {
			return nil, "", fmt.Errorf("pasted file data URL is malformed")
		}
		value = body
		if mimeType == "" {
			mediaType := strings.TrimPrefix(header, "data:")
			mediaType, _, _ = strings.Cut(mediaType, ";")
			mimeType = strings.TrimSpace(mediaType)
		}
	}
	if value == "" {
		return nil, "", fmt.Errorf("pasted file data is empty")
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, "", fmt.Errorf("decode pasted file: %w", err)
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("pasted file data is empty")
	}
	return data, mimeType, nil
}

// aiRepaintNudgeDelay waits after the attach marker so the bootstrap's
// `dtach -A` has reattached the client to Claude and Claude is listening for
// the WINCH; aiRepaintNudgeSettle spaces the shrink and restore so the two
// SIGWINCHes are not coalesced.
var (
	aiRepaintNudgeDelay  = 400 * time.Millisecond
	aiRepaintNudgeSettle = defaultAIRepaintNudgeSettle()
)

// aiRepaintInputQuiet is how recently input must have arrived for the repaint
// nudge to stand down. The nudge exists to repaint a BLANK reattached screen; a
// pane receiving keystrokes is by definition not that, and resizing it mid-line
// is what corrupted submitted prompts (#1330). Generous on purpose: skipping a
// repaint costs one keypress to fix, while a bad reflow costs the message.
const aiRepaintInputQuiet = 1500 * time.Millisecond

// noteSessionInput records that real user input just reached this pane.
func (a *App) noteSessionInput(managed *managedTerminal) {
	if managed == nil {
		return
	}
	a.mu.Lock()
	managed.inputGen++
	managed.lastInputAt = time.Now()
	a.mu.Unlock()
}

// sessionInputGen reads the pane's input counter so a nudge can detect input
// that arrived after it was scheduled.
func (a *App) sessionInputGen(managed *managedTerminal) uint64 {
	if managed == nil {
		return 0
	}
	a.mu.Lock()
	gen := managed.inputGen
	a.mu.Unlock()
	return gen
}

// typedRecentlyLocked reports whether the pane saw input inside the quiet
// window. Caller holds a.mu.
func typedRecentlyLocked(managed *managedTerminal) bool {
	return !managed.lastInputAt.IsZero() && time.Since(managed.lastInputAt) < aiRepaintInputQuiet
}

// defaultAIRepaintNudgeSettle sizes the shrink-hold to the platform's resize
// delivery. POSIX kubectl exec delivers the resize via SIGWINCH immediately, so
// a short hold suffices. On Windows there is no SIGWINCH: kubectl exec -it POLLS
// the terminal size (~250ms), so the shrink must be held past a poll interval or
// the pod TTY never sees it — and then Claude, a main-screen TUI that only
// repaints on a real geometry change, stays blank after a reattach until the
// user types (verified: a held ConPTY resize does reach the pod TTY; a 150ms
// blip does not).
func defaultAIRepaintNudgeSettle() time.Duration {
	if goruntime.GOOS == "windows" {
		return 600 * time.Millisecond
	}
	return 150 * time.Millisecond
}

// aiAttachMarker is the window-title escape (OSC 0) the open bootstrap prints
// immediately before `dtach -A` reattaches to the running program — the precise
// "about-to-reattach" signal. Nudging on the first output overall instead would
// fire too early, before the pod-side attach.
var aiAttachMarker = []byte("\x1b]0;")

func isAITabKind(managed *managedTerminal) bool {
	return managed != nil &&
		(managed.kind == sessionKindAI || managed.kind == sessionKindContributeAI)
}

// needsWINCHRepaint reports whether a session runs a main-screen TUI that only
// repaints on a real pty geometry change (a WINCH), so a same-size tab switch
// or reattach leaves it blank until one is forced. AI tabs and the orchestrator
// both run that kind of program (claude/codex). Kept separate from isAITabKind,
// which also drives AI-activity accounting (managedAITabFor, aiActivityKind)
// where an orchestrator is deliberately excluded for unrelated reasons — see
// aiActivityKind's comment.
func needsWINCHRepaint(managed *managedTerminal) bool {
	return isAITabKind(managed) || (managed != nil && managed.kind == sessionKindOrchestrator)
}

// maybeNudgeAIRepaint fires the AI repaint nudge when the attach marker first
// appears. dtach hands a reattached client a cleared screen, but Claude (an Ink
// main-screen TUI) only fully repaints on an actual geometry change — a
// same-size reattach raises no effective WINCH, so the tab would render blank.
// The nudge forces that geometry change once Claude is attached.
func (a *App) maybeNudgeAIRepaint(managed *managedTerminal, chunk []byte) {
	if !needsWINCHRepaint(managed) || !bytes.Contains(chunk, aiAttachMarker) {
		return
	}
	a.mu.Lock()
	if managed.repaintNudged || managed.closed {
		a.mu.Unlock()
		return
	}
	if typedRecentlyLocked(managed) {
		// The user is at the keyboard, so the pane is not the blank screen
		// this nudge repaints -- and their typing will force the redraw
		// anyway. Deliberately does NOT set repaintNudged: this attach has
		// not been nudged, so a later chunk may still do it once they stop.
		a.mu.Unlock()
		return
	}
	managed.repaintNudged = true
	cols, rows := managed.lastCols, managed.lastRows
	gen := managed.inputGen
	a.mu.Unlock()
	go a.nudgeAIRepaint(managed, cols, rows, aiRepaintNudgeDelay, gen)
}

// nudgeAIRepaint briefly shrinks the backend pty by one row and restores it:
// the change reaches Claude as a real WINCH and forces the full repaint a
// same-size reattach cannot. The local xterm is never resized, so the user sees
// the tab's content appear with no visible reflow.
func (a *App) nudgeAIRepaint(managed *managedTerminal, cols, rows int, initialDelay time.Duration, gen uint64) {
	if cols <= 0 || rows <= 1 {
		return
	}
	if initialDelay > 0 {
		time.Sleep(initialDelay)
	}
	// Input during the delay means the user started typing between the attach
	// marker and here. Shrink now and the restore reflows their line, so do
	// not start the cycle at all -- nothing has been resized yet, so bailing
	// costs nothing.
	if a.sessionInputGen(managed) != gen {
		return
	}
	if !a.resizeSessionIfLive(managed, cols, rows-1) {
		return
	}
	// Past this point the pty IS a row short, so it must be restored on every
	// path. awaitRepaintSettle returns early on input so the shrunken geometry
	// is held across as little typing as possible.
	a.awaitRepaintSettle(managed, gen)
	a.resizeSessionIfLive(managed, cols, rows)
}

// awaitRepaintSettle waits out the nudge settle, returning as soon as user
// input arrives so the caller can restore the pty immediately.
func (a *App) awaitRepaintSettle(managed *managedTerminal, gen uint64) {
	const slice = 10 * time.Millisecond
	for waited := time.Duration(0); waited < aiRepaintNudgeSettle; waited += slice {
		time.Sleep(slice)
		if a.sessionInputGen(managed) != gen {
			return
		}
	}
}

// resizeSessionIfLive guards the AI repaint nudge: a session that exits
// mid-nudge must not panic on a nil/closed pty.
func (a *App) resizeSessionIfLive(managed *managedTerminal, cols, rows int) bool {
	a.mu.Lock()
	session := managed.session
	closed := managed.closed
	a.mu.Unlock()
	if session == nil || closed {
		return false
	}
	_ = session.Resize(cols, rows)
	return true
}
