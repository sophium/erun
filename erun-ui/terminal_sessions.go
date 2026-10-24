package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func (a *App) OpenIDE(selection uiSelection, ide string) error {
	selection = normalizeSelection(selection)
	ide = strings.TrimSpace(ide)
	if err := errMissingTenantOrEnvironment("open IDE", selection.Tenant, selection.Environment); err != nil {
		return err
	}
	if ide != "vscode" && ide != "intellij" {
		return fmt.Errorf("unsupported IDE %q", ide)
	}
	params, err := a.resolveOpenIDEParams(selection, ide)
	if err != nil {
		return err
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	output, err := a.deps.runIDECommand(ctx, params)
	if err == nil {
		return nil
	}
	return formatOpenIDEError(ide, output, err)
}

func (a *App) resolveOpenIDEParams(selection uiSelection, ide string) (startTerminalSessionParams, error) {
	result, err := open.ResolveOpen(a.deps.store, shared.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return startTerminalSessionParams{}, err
	}

	params := startTerminalSessionParams{
		Dir:        resolveDeployStartDir(a.deps.findProjectRoot, result),
		Executable: a.deps.resolveCLIPath(),
		Args:       buildOpenIDEArgs(selection, ide),
		Env:        []string{appSessionEnvVar + "=1"},
	}
	if !result.RemoteRepo() {
		localParams, err := buildLocalOpenIDEParams(result, ide)
		if err != nil {
			return startTerminalSessionParams{}, err
		}
		params = localParams
		params.Env = []string{appSessionEnvVar + "=1"}
	} else if !result.EnvConfig.SSHD.Enabled {
		return startTerminalSessionParams{}, fmt.Errorf("open %s requires sshd-enabled remote environment; run `erun sshd init %s %s` first", ide, selection.Tenant, selection.Environment)
	}
	return params, nil
}

func formatOpenIDEError(ide, output string, err error) error {
	if detail := strings.TrimSpace(output); detail != "" {
		return fmt.Errorf("open %s: %w: %s", ide, err, detail)
	}
	return fmt.Errorf("open %s: %w", ide, err)
}

func (a *App) LoadDiff(selection uiSelection, options uiDiffOptions) (lifecycle.DiffResult, error) {
	selection = normalizeSelection(selection)
	options.Scope = strings.TrimSpace(options.Scope)
	options.SelectedCommit = strings.TrimSpace(options.SelectedCommit)
	if err := errMissingTenantOrEnvironment("load diff", selection.Tenant, selection.Environment); err != nil {
		return lifecycle.DiffResult{}, err
	}
	result, err := open.ResolveOpen(a.deps.store, shared.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return lifecycle.DiffResult{}, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	mcpPort := configpkg.MCPPortForResult(result)
	if a.deps.canReachMCPEndpoint != nil && !a.deps.canReachMCPEndpoint(mcpPort) {
		return lifecycle.DiffResult{}, wrapMCPUnreachableErrorWithKind(
			a.classifyMCPUnreachable(mcpPort),
			errors.New(mcp.DescribeLocalMCPUnreachable(result.Tenant, result.EnvConfig.Name, mcpPort)),
		)
	}
	endpoint := mcpEndpointForOpenResult(result)
	bearer := a.mcpBearer(result.Tenant, result.EnvConfig.Name)
	diff, err := a.deps.loadDiff(ctx, endpoint, bearer, options)
	if err != nil && isMCPDialFailure(err) {
		return lifecycle.DiffResult{}, wrapMCPUnreachableErrorWithKind(a.classifyMCPUnreachable(mcpPort), err)
	}
	return diff, err
}

func (a *App) sessionBySerialLocked(sessionID int) *managedTerminal {
	if sessionID <= 0 {
		return nil
	}
	for _, managed := range a.sessions {
		if managed != nil && managed.serial == sessionID {
			return managed
		}
	}
	return nil
}

// spawnStreamSession runs streamSession in its own goroutine, tracked on
// a.sessionWG so shutdown (and tests via t.Cleanup) can wait for every
// spawned reader to actually exit instead of just asking its session to
// close. Without that wait, a goroutine left mid-Read races whatever a
// caller's next teardown step touches — the failure mode that surfaced as a
// race on the adrg/xdg package's globals between a still-running session and
// a later t.Cleanup's xdg.Reload.
func (a *App) spawnStreamSession(managed *managedTerminal) {
	a.sessionWG.Add(1)
	go func() {
		defer a.sessionWG.Done()
		a.streamSession(managed)
	}()
}

func (a *App) streamSession(managed *managedTerminal) {
	buffer := make([]byte, 8192)
	var lastOutputActivity time.Time
	for {
		current := a.currentSessionFor(managed)
		if current == nil {
			// A deliberate teardown marks the session closed before it touches
			// the PTY (closeManaged), so this is where a reader lands when that
			// mark lands between iterations rather than during a Read — which
			// is certain when the close is re-entered from inside
			// handleSessionOutput. The mark is not a claim that there is
			// nothing to report: the close is an exit like any other, and it
			// still owes the terminal-exit event, the deregistration, and the
			// ready release. See finalizeDeliberateClose.
			a.finalizeDeliberateClose(managed)
			return
		}
		count, err := current.Read(buffer)
		if count > 0 {
			a.handleSessionOutput(managed, buffer[:count], &lastOutputActivity)
		}
		if err == nil {
			continue
		}
		reason := terminalSessionExitReason(current, err)
		if a.tryReconnect(managed, reason) {
			continue
		}
		a.finalizeSessionExit(managed, reason)
		return
	}
}

func (a *App) handleSessionOutput(managed *managedTerminal, chunk []byte, lastOutputActivity *time.Time) {
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: managed.serial,
		Data:      base64.StdEncoding.EncodeToString(chunk),
	})
	a.feedActivityTraceFromTerminal(managed, chunk)
	a.recordAIActivity(managed)
	a.maybeNudgeAIRepaint(managed, chunk)
	if managed.clearIdleBlockOnOutput {
		a.mu.Lock()
		a.releaseIdleBlockLocked(managed)
		a.mu.Unlock()
	}
	if time.Since(*lastOutputActivity) >= 2*time.Second {
		// Reconnect noise must not refresh the idle marker; only real
		// interaction (which clears the flag in SendSessionInput) does.
		if !a.isAwaitingPostRespawnInput(managed) {
			a.recordTerminalActivity(managed.selection)
			*lastOutputActivity = time.Now()
		}
	}
}

func (a *App) finalizeSessionExit(managed *managedTerminal, reason string) {
	a.finalizeAIActivity(managed)
	a.mu.Lock()
	// Read before the write below, which makes every exit look deliberate.
	// teardownOwnsExit is what the teardown paths (collectAndMarkClosedForSelection,
	// closeManagedLocked, EndAISessions) set under this same lock before they
	// touch the PTY — and what a teardown whose Close reported the process
	// outlived the kill takes back again — so a session it is true for ended
	// because the desktop ended it, and one it is false for either died on its
	// own or outlived a close that asked. The read is per managed session rather
	// than per env because that is the whole question the frontend has to answer
	// about an exit, and it is the only answer that survives a session killed
	// before the frontend ever saw it.
	deliberate := managed.teardownOwnsExit
	managed.closed = true
	if existing := a.sessions[managed.key]; existing == managed {
		delete(a.sessions, managed.key)
	}
	a.releaseIdleBlockLocked(managed)
	a.mu.Unlock()
	a.emitEvent(terminalExitEvent, terminalExitPayload{
		SessionID:  managed.serial,
		Reason:     reason,
		Deliberate: deliberate,
	})
	// Release any action runner waiting on this session's ready
	// signal. If the session never reached its setup-complete
	// marker (e.g. process exited mid-build), the gate would
	// otherwise hold until the action's hard timeout.
	var readyErr error
	if reason != "" {
		readyErr = fmt.Errorf("%s", reason)
	}
	managed.signalReady(readyErr)
	if reason == "" && strings.HasPrefix(managed.key, "sshd-init\x00") {
		go a.startWorkspaceSyncForSelection(managed.selection)
	}
}

func (a *App) currentSessionFor(managed *managedTerminal) terminalSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	if managed == nil || managed.closed {
		return nil
	}
	return managed.session
}

// emitEnvStatus publishes the env's real condition for the sidebar row.
// Status "" clears; envStatusStopped / envStatusFailed flag
// the row while its tabs are alive but the env is not actually running.
func (a *App) emitEnvStatus(selection uiSelection, status string) {
	a.emitEvent(envStatusEvent, envStatusPayload{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		Status:      status,
	})
}

func terminalSessionExitReason(session terminalSession, readErr error) string {
	if session != nil {
		if waitErr := session.Wait(); waitErr != nil {
			return waitErr.Error()
		}
		return ""
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return readErr.Error()
	}
	return ""
}

func (a *App) emitEvent(name string, payload any) {
	a.emit(name, payload)
}

type managedTerminal struct {
	session   terminalSession
	selection uiSelection
	key       string
	serial    int
	slot      int
	kind      sessionKind
	closed    bool
	// teardownOwnsExit is the claim that this session's exit is the desktop's
	// own doing, and it is what finalizeSessionExit turns into the exit's
	// Deliberate flag. Only markClosedByTeardownLocked sets it, under a.mu, and
	// only before the teardown touches the PTY: a killed reader can reach
	// finalizeSessionExit before Close returns, so the claim has to be in place
	// before the kill rather than after it.
	//
	// Withdrawn, not never-set, is what that order forces: the claim can only be
	// made before the outcome exists, so the outcome has to be able to take it
	// back. withdrawExitOwnershipLocked does exactly that when the teardown's
	// Close reports the process outlived the kill — a survivor's own later death
	// is then reported rather than read back as a close that already finished.
	// It is deliberately not `closed`, which records the desktop being done with
	// a session whatever the teardown managed to do to it.
	teardownOwnsExit       bool
	blocksIdleStop         bool
	clearIdleBlockOnOutput bool
	respawn                func() (terminalSession, error)
	loggedCommands         map[string]struct{}
	lockedByActivity       string
	activityTraceBuffer    string
	startedAt              time.Time

	// lastCols/lastRows track the most recent pty size: the AI-tab repaint
	// nudge needs a size to resize to, and the session does not expose its own
	// geometry.
	lastCols int
	lastRows int

	// repaintNudged guards the once-per-attach AI repaint nudge so it fires
	// on the first output after a (re)attach and not on every chunk.
	// tryReconnect clears it so the next attach nudges again.
	repaintNudged bool

	// lastInputAt/inputGen record real user input into this pane. The repaint
	// nudge changes pty geometry behind the user's back, so it must not fire
	// into a pane someone is typing into and must abandon a hold in progress
	// the moment they start: holding the pty a row short across a keystroke
	// reflowed the line being edited and corrupted the submitted prompt
	// (#1330). inputGen is a counter rather than a flag so a nudge can tell
	// "input since I was scheduled" from "input at some point".
	lastInputAt time.Time
	inputGen    uint64

	// awaitingPostRespawnInput, when true, tells streamSession to skip
	// the 2s output-activity ticker. Set on each successful respawn so
	// reconnect noise (audit lines, "── reconnecting ──" banners, the
	// failure output of a respawned `erun open` that immediately fails
	// again) cannot count as user activity and refresh the idle marker.
	// Cleared on real user input — once the user types into the
	// reattached session, subsequent output is treated as work again.
	awaitingPostRespawnInput bool

	// recentExits records the wall-clock timestamps of recent PTY
	// exits for this managed terminal. tryReconnect uses it to break
	// out of fast-exit loops where the underlying `erun open`
	// (re)spawn keeps failing within seconds — a transitional EC2,
	// an unhealthy cluster, or a helm rollout that times out. Entries
	// older than reconnectLoopWindow are pruned on each tryReconnect
	// call, so a long-running successful session followed by a single
	// exit never trips the cap.
	recentExits []time.Time

	// takenOver is set when the session's output carries the CLI's
	// taken-over notice: another ERun window re-attached this persistent
	// pod session (screen-style detach-and-reattach). tryReconnect must
	// then refuse to respawn — respawning would steal the session straight
	// back and the two windows would fight. Clicking the env in the
	// sidebar starts a fresh session, which is the deliberate take-back.
	takenOver bool

	// appSession is the persistent pod session id this terminal attaches to
	// (`ai`, `open-2`, `contribute-ai`, …), empty for sessions with no pod
	// session. The heartbeat poller keys its observations on it, so what the pod
	// reports is matched to the tab by identity rather than re-derived.
	appSession string

	// aiActiveSince / aiLastOutput / aiBusyEmitted / aiInactivityTimer
	// drive the debounced AI activity signal that powers the sidebar
	// "Claude is working" spinner. Only populated for sessionKindAI
	// managed terminals. See recordAIActivity for the debounce policy
	// (5 s sustained output to flip on, 3 s silence to flip off) and
	// session_heartbeat.go for the observed-liveness override that keeps a
	// quiet-but-running session from reading as finished.
	aiActiveSince     time.Time
	aiLastOutput      time.Time
	aiBusyEmitted     bool
	aiInactivityTimer *time.Timer
	// aiModelLatch records that the current latch was raised by the AI tool's
	// own turn-boundary report rather than by output volume. A volume-raised
	// latch is held open by pod liveness (the program is running, so silence
	// is not evidence); a report-raised one is not, because the same report is
	// what would re-assert it, and liveness is exactly what cannot tell a
	// working agent from a blocked one. See ai_session_status.go.
	aiModelLatch bool

	// readyMu / readyCh / readyErr / readyClosed track the
	// "session is past its setup phase" signal. The desktop action
	// runner blocks on waitReady so the per-env queue gate releases
	// only when the underlying `erun open` has finished its build +
	// deploy work (or fast-pathed straight to kubectl-exec). Detected
	// by feedActivityTraceFromTerminal observing one of:
	//   ==> Deployed / ==> Deploy failed / ==> Skipping
	//   Defaulted container "..."
	// or the session closing.
	readyMu     sync.Mutex
	readyCh     chan struct{}
	readyErr    error
	readyClosed bool
}

// waitReady blocks until the session reaches ready, its process exits, ctx is
// cancelled, or timeout elapses (0 means no timeout).
func (m *managedTerminal) waitReady(ctx context.Context, timeout time.Duration) error {
	if m == nil {
		return nil
	}
	m.readyMu.Lock()
	if m.readyCh == nil {
		m.readyCh = make(chan struct{})
	}
	ch := m.readyCh
	closed := m.readyClosed
	storedErr := m.readyErr
	m.readyMu.Unlock()
	if closed {
		return storedErr
	}
	var timer <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timer = t.C
	}
	select {
	case <-ch:
		m.readyMu.Lock()
		defer m.readyMu.Unlock()
		return m.readyErr
	case <-ctx.Done():
		return ctx.Err()
	case <-timer:
		return context.DeadlineExceeded
	}
}

// signalReady marks the session ready exactly once; later calls are no-ops
// because the first observed terminal-state line is authoritative.
func (m *managedTerminal) signalReady(err error) {
	if m == nil {
		return
	}
	m.readyMu.Lock()
	defer m.readyMu.Unlock()
	if m.readyClosed {
		return
	}
	if m.readyCh == nil {
		m.readyCh = make(chan struct{})
	}
	m.readyErr = err
	m.readyClosed = true
	close(m.readyCh)
}

type sessionKind string

const (
	sessionKindOpen         sessionKind = "erun"
	sessionKindLocal        sessionKind = "local"
	sessionKindAI           sessionKind = "ai"
	sessionKindCommand      sessionKind = "command"
	sessionKindOrchestrator sessionKind = "orchestrator"
)

func selectionKey(selection uiSelection) string {
	selection = normalizeSelection(selection)
	return selection.Tenant + "\x00" + selection.Environment
}

func openSessionKey(selection uiSelection, slot int) string {
	return selectionKey(selection) + "\x00" + fmt.Sprintf("%d", slot)
}

func localSessionKey(selection uiSelection, slot int) string {
	return "local\x00" + selectionKey(selection) + "\x00" + fmt.Sprintf("%d", slot)
}

func aiSessionKey(selection uiSelection, slot int) string {
	return "ai\x00" + selectionKey(selection) + "\x00" + fmt.Sprintf("%d", slot)
}

func (a *App) releaseIdleBlockLocked(managed *managedTerminal) {
	if managed == nil || !managed.blocksIdleStop {
		return
	}
	busyKey := selectionKey(managed.selection)
	if a.busyEnvs[busyKey] <= 1 {
		delete(a.busyEnvs, busyKey)
	} else {
		a.busyEnvs[busyKey]--
	}
	managed.blocksIdleStop = false
	managed.clearIdleBlockOnOutput = false
}
