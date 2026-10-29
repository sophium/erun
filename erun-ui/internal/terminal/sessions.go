package terminal

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/sophium/erun/erun-common/base/config/localports"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func (s *Service) OpenIDE(selection uimodel.Selection, ide string) error {
	selection = uimodel.NormalizeSelection(selection)
	ide = strings.TrimSpace(ide)
	if err := uimodel.ErrMissingTenantOrEnvironment("open IDE", selection.Tenant, selection.Environment); err != nil {
		return err
	}
	if ide != "vscode" && ide != "intellij" {
		return fmt.Errorf("unsupported IDE %q", ide)
	}
	params, err := s.resolveOpenIDEParams(selection, ide)
	if err != nil {
		return err
	}

	ctx := s.d.Context()
	output, err := s.d.RunIDECommand(ctx, params)
	if err == nil {
		return nil
	}
	return FormatOpenIDEError(ide, output, err)
}

func (s *Service) resolveOpenIDEParams(selection uimodel.Selection, ide string) (StartParams, error) {
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return StartParams{}, err
	}

	params := StartParams{
		Dir:        resolveDeployStartDir(s.d.FindProjectRoot, result),
		Executable: s.d.ResolveCLIPath(),
		Args:       buildOpenIDEArgs(selection, ide),
		Env:        []string{s.d.AppSessionEnv},
	}
	if !result.RemoteRepo() {
		localParams, err := buildLocalOpenIDEParams(result, ide)
		if err != nil {
			return StartParams{}, err
		}
		params = localParams
		params.Env = []string{s.d.AppSessionEnv}
	} else if !result.EnvConfig.SSHD.Enabled {
		return StartParams{}, fmt.Errorf("open %s requires sshd-enabled remote environment; run `erun sshd init %s %s` first", ide, selection.Tenant, selection.Environment)
	}
	return params, nil
}

func FormatOpenIDEError(ide, output string, err error) error {
	if detail := strings.TrimSpace(output); detail != "" {
		return fmt.Errorf("open %s: %w: %s", ide, err, detail)
	}
	return fmt.Errorf("open %s: %w", ide, err)
}

func (s *Service) LoadDiff(selection uimodel.Selection, options uimodel.DiffOptions) (model.DiffResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	options.Scope = strings.TrimSpace(options.Scope)
	options.SelectedCommit = strings.TrimSpace(options.SelectedCommit)
	if err := uimodel.ErrMissingTenantOrEnvironment("load diff", selection.Tenant, selection.Environment); err != nil {
		return model.DiffResult{}, err
	}
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return model.DiffResult{}, err
	}
	ctx := s.d.Context()
	mcpPort := localports.MCPPortForResult(result)
	if !s.d.CanReachMCPEndpoint(mcpPort) {
		return model.DiffResult{}, s.d.WrapMCPUnreachable(
			s.ClassifyMCPUnreachable(mcpPort),
			errors.New(mcp.DescribeLocalMCPUnreachable(result.Tenant, result.EnvConfig.Name, mcpPort)),
		)
	}
	endpoint := s.d.MCPEndpoint(result)
	bearer := s.d.MCPBearer(result.Tenant, result.EnvConfig.Name)
	diff, err := s.d.LoadDiff(ctx, endpoint, bearer, options)
	if err != nil && s.d.IsMCPDialFailure(err) {
		return model.DiffResult{}, s.d.WrapMCPUnreachable(s.ClassifyMCPUnreachable(mcpPort), err)
	}
	return diff, err
}

func (s *Service) sessionBySerialLocked(sessionID int) *Session {
	if sessionID <= 0 {
		return nil
	}
	for _, managed := range s.sessions {
		if managed != nil && managed.Serial == sessionID {
			return managed
		}
	}
	return nil
}

// SpawnStream tracks the reader on s.sessionWG so shutdown and tests can
// wait for it to exit, not just ask its session to close.
func (s *Service) SpawnStream(managed *Session) {
	s.sessionWG.Add(1)
	go func() {
		defer s.sessionWG.Done()
		s.Stream(managed)
	}()
}

func (s *Service) Stream(managed *Session) {
	buffer := make([]byte, 8192)
	var lastOutputActivity time.Time
	for {
		current := s.currentSessionFor(managed)
		if current == nil {
			// The close mark landed between reads; the close still owes its exit event.
			s.finalizeDeliberateClose(managed)
			return
		}
		count, err := current.Read(buffer)
		if count > 0 {
			s.handleSessionOutput(managed, buffer[:count], &lastOutputActivity)
		}
		if err == nil {
			continue
		}
		reason := terminalSessionExitReason(current, err)
		if s.tryReconnect(managed, reason) {
			continue
		}
		s.finalizeSessionExit(managed, reason)
		return
	}
}

func (s *Service) handleSessionOutput(managed *Session, chunk []byte, lastOutputActivity *time.Time) {
	s.emitEvent(uimodel.TerminalOutputEvent, uimodel.TerminalOutputPayload{
		SessionID: managed.Serial,
		Data:      base64.StdEncoding.EncodeToString(chunk),
	})
	s.d.OnOutput(managed, chunk)
	s.recordAIActivity(managed)
	s.maybeNudgeAIRepaint(managed, chunk)
	if managed.ClearIdleBlockOnOutput {
		s.mu.Lock()
		s.releaseIdleBlockLocked(managed)
		s.mu.Unlock()
	}
	if time.Since(*lastOutputActivity) >= 2*time.Second {
		// Reconnect noise must not refresh the idle marker.
		if !s.isAwaitingPostRespawnInput(managed) {
			s.RecordActivity(managed.Selection)
			*lastOutputActivity = time.Now()
		}
	}
}

func (s *Service) finalizeSessionExit(managed *Session, reason string) {
	s.finalizeAIActivity(managed)
	s.mu.Lock()
	// Read before the write below, which makes every exit look deliberate.
	deliberate := managed.teardownOwnsExit
	managed.Closed = true
	if existing := s.sessions[managed.Key]; existing == managed {
		delete(s.sessions, managed.Key)
	}
	s.releaseIdleBlockLocked(managed)
	s.mu.Unlock()
	s.emitEvent(uimodel.TerminalExitEvent, terminalExitPayload{
		SessionID:  managed.Serial,
		Reason:     reason,
		Deliberate: deliberate,
	})
	// Release waiters even when the session never reached its setup marker.
	var readyErr error
	if reason != "" {
		readyErr = fmt.Errorf("%s", reason)
	}
	managed.SignalReady(readyErr)
	if reason == "" && strings.HasPrefix(managed.Key, "sshd-init\x00") {
		go s.d.StartWorkspaceSync(managed.Selection)
	}
}

func (s *Service) currentSessionFor(managed *Session) PTY {
	s.mu.Lock()
	defer s.mu.Unlock()
	if managed == nil || managed.Closed {
		return nil
	}
	return managed.PTY
}

// emitEnvStatus publishes the env's condition for the sidebar row; "" clears.
func (s *Service) emitEnvStatus(selection uimodel.Selection, status string) {
	s.emitEvent(uimodel.EnvStatusEvent, uimodel.EnvStatusPayload{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		Status:      status,
	})
}

func terminalSessionExitReason(session PTY, readErr error) string {
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

type Session struct {
	PTY       PTY
	Selection uimodel.Selection
	Key       string
	Serial    int
	Slot      int
	Kind      Kind
	Closed    bool
	// teardownOwnsExit becomes the exit's Deliberate flag. It is set before the kill,
	// since a killed reader can finalize before Close returns, and withdrawn when
	// Close reports the process survived. Distinct from closed.
	teardownOwnsExit       bool
	BlocksIdleStop         bool
	ClearIdleBlockOnOutput bool
	Respawn                func() (PTY, error)
	loggedCommands         map[string]struct{}
	LockedByActivity       string
	ActivityTraceBuffer    string
	StartedAt              time.Time

	// LastCols/lastRows give the repaint nudge a size; the session exposes none.
	LastCols int
	LastRows int

	// repaintNudged makes the AI repaint nudge fire once per attach.
	repaintNudged bool

	// LastInputAt/inputGen keep the repaint nudge out of a pane being typed into;
	// inputGen is a counter so a nudge can detect input since it was scheduled.
	LastInputAt time.Time
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

	// AppSession is the pod session id the heartbeat poller keys on; empty when none.
	AppSession string

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

// WaitReady blocks until the session reaches ready, its process exits, ctx is
// cancelled, or timeout elapses (0 means no timeout).
func (m *Session) WaitReady(ctx context.Context, timeout time.Duration) error {
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

// SignalReady is idempotent: the first terminal-state line is authoritative.
func (m *Session) SignalReady(err error) {
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

type Kind string

const (
	KindOpen         Kind = "erun"
	KindLocal        Kind = "local"
	KindAI           Kind = "ai"
	KindCommand      Kind = "command"
	KindOrchestrator Kind = "orchestrator"

	KindContributeERun Kind = "contribute-erun"
	KindContributeAI   Kind = "contribute-ai"
)

// OrchestratorSessionKey is the registry key of an orchestrator's session.
func OrchestratorSessionKey(id string) string {
	return "orchestrator\x00" + id
}

// OrchestratorIDFromKey reverses OrchestratorSessionKey.
func OrchestratorIDFromKey(key string) (string, bool) {
	const prefix = "orchestrator\x00"
	if !strings.HasPrefix(key, prefix) {
		return "", false
	}
	return strings.TrimPrefix(key, prefix), true
}

// DeployInFlight: only deploys lock sibling terminals, so a locked session is the signal.
func (s *Service) DeployInFlight(selection uimodel.Selection) bool {
	selection = uimodel.NormalizeSelection(selection)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, managed := range s.sessions {
		if managed == nil || managed.Closed || managed.LockedByActivity == "" {
			continue
		}
		if strings.TrimSpace(managed.Selection.Tenant) == selection.Tenant &&
			strings.TrimSpace(managed.Selection.Environment) == selection.Environment {
			return true
		}
	}
	return false
}

func openSessionKey(selection uimodel.Selection, slot int) string {
	return uimodel.SelectionKey(selection) + "\x00" + fmt.Sprintf("%d", slot)
}

func localSessionKey(selection uimodel.Selection, slot int) string {
	return "local\x00" + uimodel.SelectionKey(selection) + "\x00" + fmt.Sprintf("%d", slot)
}

func aiSessionKey(selection uimodel.Selection, slot int) string {
	return "ai\x00" + uimodel.SelectionKey(selection) + "\x00" + fmt.Sprintf("%d", slot)
}

func (s *Service) releaseIdleBlockLocked(managed *Session) {
	if managed == nil || !managed.BlocksIdleStop {
		return
	}
	busyKey := uimodel.SelectionKey(managed.Selection)
	if s.busyEnvs[busyKey] <= 1 {
		delete(s.busyEnvs, busyKey)
	} else {
		s.busyEnvs[busyKey]--
	}
	managed.BlocksIdleStop = false
	managed.ClearIdleBlockOnOutput = false
}
