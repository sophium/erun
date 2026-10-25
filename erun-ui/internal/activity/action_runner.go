package activity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// desktopAction's run closure must honour ctx cancellation.
type desktopAction struct {
	id        string
	kind      string
	summary   string
	selection uimodel.Selection
	run       func(ctx context.Context) error
}

// envActionQueue serializes work for one (tenant, env); its buffered channel
// keeps enqueue from blocking the Wails caller.
type envActionQueue struct {
	pending chan *desktopAction
}

const desktopActionQueueDepth = 64

// Tenant/env-less actions share one global queue.
func envActionQueueKey(selection uimodel.Selection) string {
	tenant := strings.TrimSpace(selection.Tenant)
	env := strings.TrimSpace(selection.Environment)
	if tenant == "" && env == "" {
		return "__global__"
	}
	return tenant + "\x00" + env
}

// enqueueDesktopAction errors only on setup failures; action errors surface in
// the entry's terminal status.
func (s *Service) enqueueDesktopAction(action desktopAction) (string, error) {
	if strings.TrimSpace(action.kind) == "" {
		return "", errors.New("action kind is required")
	}
	if action.run == nil {
		return "", errors.New("action run closure is required")
	}
	now := time.Now().UTC()
	if strings.TrimSpace(action.id) == "" {
		seedForID := Entry{
			Command:     action.kind,
			Tenant:      action.selection.Tenant,
			Environment: action.selection.Environment,
			Version:     action.selection.Version,
			StartedAt:   now,
		}
		action.id = generateID(seedForID)
	}
	enq := now
	entry, fresh := s.queue.start(Entry{
		ID:                action.id,
		Command:           action.kind,
		ActionKind:        action.kind,
		Tenant:            action.selection.Tenant,
		Environment:       action.selection.Environment,
		Version:           action.selection.Version,
		Release:           releaseNameForTenant(action.selection.Tenant),
		Namespace:         namespaceForTenantEnv(action.selection.Tenant, action.selection.Environment),
		KubernetesContext: strings.TrimSpace(action.selection.KubernetesContext),
		Summary:           summaryForAction(action),
		Status:            StatusWaiting,
		Source:            "action",
		EnqueuedAt:        &enq,
		StartedAt:         enq,
	})
	if !fresh {
		// Duplicate of an in-flight entry: collapse onto it.
		return entry.ID, nil
	}
	s.RememberKubeContext(action.selection.KubernetesContext)

	queue := s.ensureEnvActionQueue(envActionQueueKey(action.selection))
	select {
	case queue.pending <- &action:
		return action.id, nil
	default:
		s.queue.finish(action.id, StatusFailed, "desktop action queue is full")
		return action.id, fmt.Errorf("action queue full for %s", envActionQueueKey(action.selection))
	}
}

func summaryForAction(action desktopAction) string {
	if s := strings.TrimSpace(action.summary); s != "" {
		return s
	}
	tenant := strings.TrimSpace(action.selection.Tenant)
	env := strings.TrimSpace(action.selection.Environment)
	if tenant == "" && env == "" {
		return action.kind
	}
	if env == "" {
		return action.kind + " " + tenant
	}
	if tenant == "" {
		return action.kind + " " + env
	}
	return action.kind + " " + tenant + "/" + env
}

func (s *Service) ensureEnvActionQueue(key string) *envActionQueue {
	s.actionQueueMu.Lock()
	defer s.actionQueueMu.Unlock()
	if s.actionQueues == nil {
		s.actionQueues = make(map[string]*envActionQueue)
	}
	queue, ok := s.actionQueues[key]
	if ok {
		return queue
	}
	queue = &envActionQueue{pending: make(chan *desktopAction, desktopActionQueueDepth)}
	s.actionQueues[key] = queue
	go s.runEnvActionWorker(key, queue)
	return queue
}

func (s *Service) runEnvActionWorker(key string, queue *envActionQueue) {
	for action := range queue.pending {
		s.executeDesktopAction(action)
	}
	_ = key
}

func (s *Service) executeDesktopAction(action *desktopAction) {
	current, ok := s.queue.findByID(action.id)
	if !ok {
		// Force-dismissed before its turn.
		return
	}
	if current.Status == StatusCancelled || statusIsTerminal(current.Status) {
		return
	}
	promoted, ok := s.queue.promoteToRunning(action.id)
	if !ok {
		return
	}
	// Re-emit for a dropped event or late-mounted frontend; the frontend dedupes.
	s.emitState(promoted)
	s.lockTerminalsForActivity(promoted)
	s.startActivityStatusPollerForAction(action, current)
	ctx, cancel := context.WithCancel(s.d.Context())
	s.registerCancel(action.id, cancel)
	defer s.clearCancel(action.id)
	defer cancel()

	err := action.run(ctx)

	status, errMsg := desktopActionTerminalStatus(err)
	if final, finished := s.queue.finish(action.id, status, errMsg); finished {
		s.unlockTerminalsForActivity(final)
	}
}

func (s *Service) startActivityStatusPollerForAction(action *desktopAction, entry Entry) {
	if action.kind != "deploy" && action.kind != "force-deploy" {
		return
	}
	s.startStatusPoller(entry)
}

func desktopActionTerminalStatus(err error) (Status, string) {
	if err == nil {
		return StatusSucceeded, ""
	}
	if errors.Is(err, context.Canceled) {
		return StatusCancelled, "cancelled"
	}
	return StatusFailed, err.Error()
}

// Only waiting cancellation is exposed; running cancellation is plumbed for later.
func (s *Service) registerCancel(id string, cancel context.CancelFunc) {
	s.actionQueueMu.Lock()
	defer s.actionQueueMu.Unlock()
	if s.actionCancels == nil {
		s.actionCancels = make(map[string]context.CancelFunc)
	}
	s.actionCancels[id] = cancel
}

func (s *Service) clearCancel(id string) {
	s.actionQueueMu.Lock()
	defer s.actionQueueMu.Unlock()
	delete(s.actionCancels, id)
}

// CancelWaitingAction cancels a queued action; returns false once it has started.
func (s *Service) CancelWaitingAction(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	entry, ok := s.queue.findByID(id)
	if !ok {
		return false
	}
	if entry.Status != StatusWaiting {
		return false
	}
	if final, finished := s.queue.finish(id, StatusCancelled, "cancelled before start"); finished {
		s.unlockTerminalsForActivity(final)
		// The worker skips the queued action because its entry is now terminal.
		return true
	}
	return false
}

func (s *queueStore) findByID(id string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.active[id]; ok {
		return *cloneEntry(entry), true
	}
	for _, entry := range s.history {
		if entry.ID == id {
			return *cloneEntry(entry), true
		}
	}
	return Entry{}, false
}

// Readiness is a started session the runner's gate waits on.
type Readiness interface {
	WaitReady(ctx context.Context, timeout time.Duration) error
}

// GatedStart starts a session once the per-env action runner admits it; the
// session it returns gates the runner until it is ready, and a nil one gates nothing.
type GatedStart func(ctx context.Context) (uimodel.StartSessionResult, Readiness, error)

// gatedSessionMaxSetup is a safety net so the queue drains if every ready marker
// is missed; it spans a cold build+deploy.
const gatedSessionMaxSetup = 6 * time.Minute

// A gated kind's entry stays running only while its terminal is being established.
const (
	GatedOpen           = "open"
	GatedAI             = "ai"
	GatedContributeERun = "contribute-erun"
	GatedContributeAI   = "contribute-ai"
)

func isGatedSessionKind(kind string) bool {
	switch kind {
	case GatedOpen, GatedAI, GatedContributeERun, GatedContributeAI:
		return true
	}
	return false
}

func isGatedSessionSetupEntry(entry Entry) bool {
	return entry.Source == "action" && isGatedSessionKind(entry.ActionKind)
}

// EnqueueGatedSession blocks until the session is created, then holds the gate
// until it signals ready.
func (s *Service) EnqueueGatedSession(selection uimodel.Selection, kind string, start GatedStart) (uimodel.StartSessionResult, error) {
	if !isGatedSessionKind(kind) {
		return uimodel.StartSessionResult{}, fmt.Errorf("unregistered gated session kind %q", kind)
	}
	type spawnOutcome struct {
		result uimodel.StartSessionResult
		err    error
	}
	ready := make(chan spawnOutcome, 1)

	if _, err := s.enqueueDesktopAction(desktopAction{
		kind:      kind,
		selection: selection,
		summary:   kind + " " + strings.TrimSpace(selection.Tenant+"/"+selection.Environment),
		run: func(ctx context.Context) error {
			result, managed, startErr := start(ctx)
			ready <- spawnOutcome{result: result, err: startErr}
			if startErr != nil {
				return startErr
			}
			if managed == nil {
				return nil
			}
			waitErr := managed.WaitReady(ctx, gatedSessionMaxSetup)
			// A timeout releases successfully: the session is usable, only its marker was missed.
			if errors.Is(waitErr, context.DeadlineExceeded) {
				return nil
			}
			return waitErr
		},
	}); err != nil {
		return uimodel.StartSessionResult{}, err
	}

	outcome := <-ready
	return outcome.result, outcome.err
}

// StopActionRunners closes every per-env queue and cancels the running actions.
func (s *Service) StopActionRunners() {
	s.actionQueueMu.Lock()
	queues := s.actionQueues
	s.actionQueues = nil
	cancels := s.actionCancels
	s.actionCancels = nil
	s.actionQueueMu.Unlock()
	for _, queue := range queues {
		close(queue.pending)
	}
	for _, cancel := range cancels {
		cancel()
	}
}
