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
		// Real input rearms the orchestrator pacing nudge.
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

// SavePastedFile copies a pasted file of any type into the runtime pod and
// returns its in-pod path.
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

// RepaintSession forces a full repaint with a shrink-and-restore WINCH, since
// main-screen TUIs repaint only on a geometry change. Unlike
// maybeNudgeAIRepaint it ignores repaintNudged: every switch wants a repaint.
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
		// Resizing under a line being typed corrupts it.
		return nil
	}
	// Shells and alt-screen apps rebuild from the replayed buffer.
	if !needsWINCHRepaint(managed) {
		return nil
	}
	// No attach delay: the program is already attached.
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

// aiRepaintNudgeDelay lets `dtach -A` reattach first; aiRepaintNudgeSettle keeps
// the two SIGWINCHes from coalescing.
var (
	aiRepaintNudgeDelay  = 400 * time.Millisecond
	aiRepaintNudgeSettle = defaultAIRepaintNudgeSettle()
)

// aiRepaintInputQuiet is generous: a skipped repaint costs a keypress, a bad
// reflow costs the message.
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

// typedRecentlyLocked: caller holds a.mu.
func typedRecentlyLocked(managed *managedTerminal) bool {
	return !managed.lastInputAt.IsZero() && time.Since(managed.lastInputAt) < aiRepaintInputQuiet
}

// defaultAIRepaintNudgeSettle is longer on Windows, where kubectl exec polls the
// terminal size (~250ms) instead of receiving SIGWINCH.
func defaultAIRepaintNudgeSettle() time.Duration {
	if goruntime.GOOS == "windows" {
		return 600 * time.Millisecond
	}
	return 150 * time.Millisecond
}

// aiAttachMarker is the OSC 0 title the bootstrap prints right before `dtach -A`
// reattaches; the first output overall would be too early.
var aiAttachMarker = []byte("\x1b]0;")

func isAITabKind(managed *managedTerminal) bool {
	return managed != nil &&
		(managed.kind == sessionKindAI || managed.kind == sessionKindContributeAI)
}

// needsWINCHRepaint covers AI tabs and orchestrators. It is separate from
// isAITabKind, whose activity accounting excludes orchestrators.
func needsWINCHRepaint(managed *managedTerminal) bool {
	return isAITabKind(managed) || (managed != nil && managed.kind == sessionKindOrchestrator)
}

// maybeNudgeAIRepaint nudges once when the attach marker first appears.
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
		// Typing will redraw anyway. repaintNudged stays unset so a later chunk can
		// still nudge.
		a.mu.Unlock()
		return
	}
	managed.repaintNudged = true
	cols, rows := managed.lastCols, managed.lastRows
	gen := managed.inputGen
	a.mu.Unlock()
	go a.nudgeAIRepaint(managed, cols, rows, aiRepaintNudgeDelay, gen)
}

// nudgeAIRepaint shrinks the backend pty by one row and restores it; the local
// xterm is never resized.
func (a *App) nudgeAIRepaint(managed *managedTerminal, cols, rows int, initialDelay time.Duration, gen uint64) {
	if cols <= 0 || rows <= 1 {
		return
	}
	if initialDelay > 0 {
		time.Sleep(initialDelay)
	}
	// Nothing is resized yet, so bailing on input costs nothing.
	if a.sessionInputGen(managed) != gen {
		return
	}
	if !a.resizeSessionIfLive(managed, cols, rows-1) {
		return
	}
	// The pty is now a row short and must be restored on every path.
	a.awaitRepaintSettle(managed, gen)
	a.resizeSessionIfLive(managed, cols, rows)
}

// awaitRepaintSettle returns early on input.
func (a *App) awaitRepaintSettle(managed *managedTerminal, gen uint64) {
	const slice = 10 * time.Millisecond
	for waited := time.Duration(0); waited < aiRepaintNudgeSettle; waited += slice {
		time.Sleep(slice)
		if a.sessionInputGen(managed) != gen {
			return
		}
	}
}

// resizeSessionIfLive tolerates a session exiting mid-nudge.
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
