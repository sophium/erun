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
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func (a *App) OpenIDE(selection uimodel.Selection, ide string) error {
	selection = uimodel.NormalizeSelection(selection)
	ide = strings.TrimSpace(ide)
	if err := uimodel.ErrMissingTenantOrEnvironment("open IDE", selection.Tenant, selection.Environment); err != nil {
		return err
	}
	if ide != "vscode" && ide != "intellij" {
		return fmt.Errorf("unsupported IDE %q", ide)
	}
	params, err := a.resolveOpenIDEParams(selection, ide)
	if err != nil {
		return err
	}

	ctx := a.context()
	output, err := a.deps.runIDECommand(ctx, params)
	if err == nil {
		return nil
	}
	return formatOpenIDEError(ide, output, err)
}

func (a *App) resolveOpenIDEParams(selection uimodel.Selection, ide string) (startTerminalSessionParams, error) {
	result, err := resolve.ResolveOpen(a.deps.store, model.OpenParams{
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

func (a *App) LoadDiff(selection uimodel.Selection, options uiDiffOptions) (model.DiffResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	options.Scope = strings.TrimSpace(options.Scope)
	options.SelectedCommit = strings.TrimSpace(options.SelectedCommit)
	if err := uimodel.ErrMissingTenantOrEnvironment("load diff", selection.Tenant, selection.Environment); err != nil {
		return model.DiffResult{}, err
	}
	result, err := resolve.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return model.DiffResult{}, err
	}
	ctx := a.context()
	mcpPort := configpkg.MCPPortForResult(result)
	if a.deps.canReachMCPEndpoint != nil && !a.deps.canReachMCPEndpoint(mcpPort) {
		return model.DiffResult{}, wrapMCPUnreachableErrorWithKind(
			a.classifyMCPUnreachable(mcpPort),
			errors.New(mcp.DescribeLocalMCPUnreachable(result.Tenant, result.EnvConfig.Name, mcpPort)),
		)
	}
	endpoint := mcpEndpointForOpenResult(result)
	bearer := a.mcpBearer(result.Tenant, result.EnvConfig.Name)
	diff, err := a.deps.loadDiff(ctx, endpoint, bearer, options)
	if err != nil && isMCPDialFailure(err) {
		return model.DiffResult{}, wrapMCPUnreachableErrorWithKind(a.classifyMCPUnreachable(mcpPort), err)
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

// spawnStreamSession tracks the reader on a.sessionWG so shutdown and tests can
// wait for it to exit, not just ask its session to close.
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
			// The close mark landed between reads; the close still owes its exit event.
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
	a.emitEvent(uimodel.TerminalOutputEvent, terminalOutputPayload{
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
		// Reconnect noise must not refresh the idle marker.
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
	deliberate := managed.teardownOwnsExit
	managed.closed = true
	if existing := a.sessions[managed.key]; existing == managed {
		delete(a.sessions, managed.key)
	}
	a.releaseIdleBlockLocked(managed)
	a.mu.Unlock()
	a.emitEvent(uimodel.TerminalExitEvent, terminalExitPayload{
		SessionID:  managed.serial,
		Reason:     reason,
		Deliberate: deliberate,
	})
	// Release waiters even when the session never reached its setup marker.
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

// emitEnvStatus publishes the env's condition for the sidebar row; "" clears.
func (a *App) emitEnvStatus(selection uimodel.Selection, status string) {
	a.emitEvent(uimodel.EnvStatusEvent, uimodel.EnvStatusPayload{
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
	selection uimodel.Selection
	key       string
	serial    int
	slot      int
	kind      sessionKind
	closed    bool
	// teardownOwnsExit becomes the exit's Deliberate flag. It is set before the kill,
	// since a killed reader can finalize before Close returns, and withdrawn when
	// Close reports the process survived. Distinct from closed.
	teardownOwnsExit       bool
	blocksIdleStop         bool
	clearIdleBlockOnOutput bool
	respawn                func() (terminalSession, error)
	loggedCommands         map[string]struct{}
	lockedByActivity       string
	activityTraceBuffer    string
	startedAt              time.Time

	// lastCols/lastRows give the repaint nudge a size; the session exposes none.
	lastCols int
	lastRows int

	// repaintNudged makes the AI repaint nudge fire once per attach.
	repaintNudged bool

	// lastInputAt/inputGen keep the repaint nudge out of a pane being typed into;
	// inputGen is a counter so a nudge can detect input since it was scheduled.
	lastInputAt time.Time
	inputGen    uint64

	// awaitingPostRespawnInput keeps reconnect noise from counting as activity
	// until the user types.
	awaitingPostRespawnInput bool

	// recentExits feeds the fast-exit loop guard; entries older than
	// reconnectLoopWindow are pruned.
	recentExits []time.Time

	// takenOver means another window reattached this pod session, so it must not
	// respawn.
	takenOver bool

	// appSession is the pod session id the heartbeat poller keys on; empty when none.
	appSession string

	// These drive the debounced sidebar working spinner for AI sessions.
	aiActiveSince     time.Time
	aiLastOutput      time.Time
	aiBusyEmitted     bool
	aiInactivityTimer *time.Timer
	// aiModelLatch marks a latch raised by the tool's own report, which pod liveness
	// does not hold open.
	aiModelLatch bool

	// The ready signal releases the per-env action gate once `erun open` finishes
	// setup or the session closes.
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

// signalReady is idempotent: the first terminal-state line is authoritative.
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

func selectionKey(selection uimodel.Selection) string {
	return uimodel.SelectionKey(selection)
}

func openSessionKey(selection uimodel.Selection, slot int) string {
	return selectionKey(selection) + "\x00" + fmt.Sprintf("%d", slot)
}

func localSessionKey(selection uimodel.Selection, slot int) string {
	return "local\x00" + selectionKey(selection) + "\x00" + fmt.Sprintf("%d", slot)
}

func aiSessionKey(selection uimodel.Selection, slot int) string {
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
