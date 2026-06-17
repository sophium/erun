package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// desktopAction is one piece of work the runner serializes. The run
// closure is called by the env-worker goroutine when this action's turn
// comes; it should respect ctx.Done() for cancellation.
type desktopAction struct {
	id        string
	kind      string
	summary   string
	selection uiSelection
	run       func(ctx context.Context) error
}

// envActionQueue owns the channel + worker for one (tenant, env)
// queue. Channels are buffered so enqueue never blocks the Wails
// caller; the buffer is sized so a single user clicking through the UI
// can pile up requests without dropping.
type envActionQueue struct {
	pending chan *desktopAction
}

const desktopActionQueueDepth = 64

// envActionQueueKey returns the key the runner uses to map selection →
// per-env worker. Tenant/env-less actions (cloud provider login, etc.)
// share a single global queue, so they too serialize among themselves
// without blocking per-env work.
func envActionQueueKey(selection uiSelection) string {
	tenant := strings.TrimSpace(selection.Tenant)
	env := strings.TrimSpace(selection.Environment)
	if tenant == "" && env == "" {
		return "__global__"
	}
	return tenant + "\x00" + env
}

// enqueueDesktopAction registers a fresh waiting entry in the activity
// queue and pushes the action onto its env-queue's worker channel.
// Returns the entry ID so callers can correlate later events. Errors
// only surface for setup failures (no activity store, malformed action);
// the action's own errors are reported through the entry's terminal
// status.
func (a *App) enqueueDesktopAction(action desktopAction) (string, error) {
	if a == nil || a.activityQueue == nil {
		return "", errors.New("activity queue not initialized")
	}
	if strings.TrimSpace(action.kind) == "" {
		return "", errors.New("action kind is required")
	}
	if action.run == nil {
		return "", errors.New("action run closure is required")
	}
	now := time.Now().UTC()
	if strings.TrimSpace(action.id) == "" {
		seedForID := activityQueueEntry{
			Command:     action.kind,
			Tenant:      action.selection.Tenant,
			Environment: action.selection.Environment,
			Version:     action.selection.Version,
			StartedAt:   now,
		}
		action.id = generateActivityQueueID(seedForID)
	}
	enq := now
	entry, fresh := a.activityQueue.start(activityQueueEntry{
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
		Status:            activityQueueStatusWaiting,
		Source:            "action",
		EnqueuedAt:        &enq,
		StartedAt:         enq,
	})
	if !fresh {
		// A waiting entry with this ID already exists — collapse onto
		// it rather than creating a duplicate. The original action's
		// run closure stays in flight; the duplicate's payload is
		// discarded.
		return entry.ID, nil
	}
	a.rememberKubeContextForActivity(action.selection.KubernetesContext)

	queue := a.ensureEnvActionQueue(envActionQueueKey(action.selection))
	select {
	case queue.pending <- &action:
		return action.id, nil
	default:
		// Queue full — finalize the entry as failed so the user sees
		// what happened instead of a silent dropped request.
		a.activityQueue.finish(action.id, activityQueueStatusFailed, "desktop action queue is full")
		return action.id, fmt.Errorf("action queue full for %s", envActionQueueKey(action.selection))
	}
}

// summaryForAction renders a default human label when the caller did
// not supply one. Mirrors the conventions used elsewhere in the queue
// so the drawer's command-subtitle helpers continue to render nicely.
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

func (a *App) ensureEnvActionQueue(key string) *envActionQueue {
	a.actionQueueMu.Lock()
	defer a.actionQueueMu.Unlock()
	if a.actionQueues == nil {
		a.actionQueues = make(map[string]*envActionQueue)
	}
	queue, ok := a.actionQueues[key]
	if ok {
		return queue
	}
	queue = &envActionQueue{pending: make(chan *desktopAction, desktopActionQueueDepth)}
	a.actionQueues[key] = queue
	go a.runEnvActionWorker(key, queue)
	return queue
}

// runEnvActionWorker is the per-(tenant,env) goroutine that drains
// pending actions one at a time. It exits when the channel is closed
// (which happens at desktop shutdown or never).
func (a *App) runEnvActionWorker(key string, queue *envActionQueue) {
	for action := range queue.pending {
		a.executeDesktopAction(action)
	}
	_ = key
}

// executeDesktopAction promotes the entry from waiting → running, runs
// the action's closure with a cancellable context, then finalizes the
// entry to a terminal status. If the entry was cancelled while waiting
// (forceDismiss/cancelWaitingAction), the action is skipped without
// running.
func (a *App) executeDesktopAction(action *desktopAction) {
	if a == nil || a.activityQueue == nil {
		return
	}
	current, ok := a.activityQueue.findByID(action.id)
	if !ok {
		// Entry was force-dismissed before its turn — drop silently.
		return
	}
	if current.Status == activityQueueStatusCancelled || activityQueueStatusIsTerminal(current.Status) {
		// Already cancelled by the user or finalized externally.
		return
	}
	promoted, ok := a.activityQueue.promoteToRunning(action.id)
	if !ok {
		return
	}
	// Re-emit the running snapshot so a dropped activity:state event
	// or a frontend that mounted after the first emit still sees the
	// transition. The store's notifyLocked already fired once; this
	// belt-and-braces resync covers Wails-runtime delivery hiccups
	// without changing semantics (the frontend dedupes on identical
	// payloads via activityEntriesShallowEqual).
	a.emitActivityState(promoted)
	a.lockTerminalsForActivity(promoted)
	a.startActivityStatusPollerForAction(action, current)
	ctx, cancel := context.WithCancel(a.activityWatcherCtx())
	a.registerCancel(action.id, cancel)
	defer a.clearCancel(action.id)
	defer cancel()

	err := action.run(ctx)

	status, errMsg := desktopActionTerminalStatus(err)
	if final, finished := a.activityQueue.finish(action.id, status, errMsg); finished {
		a.unlockTerminalsForActivity(final)
	}
}

// startActivityStatusPollerForAction kicks off the container-status poller
// for deploy/force-deploy actions when a poller is configured. Other action
// kinds (and a nil poller) are a no-op. Extracted from executeDesktopAction
// so it stays under the cyclomatic-complexity limit; behavior is unchanged.
func (a *App) startActivityStatusPollerForAction(action *desktopAction, entry activityQueueEntry) {
	if action.kind != "deploy" && action.kind != "force-deploy" {
		return
	}
	if a.activityStatusPoller == nil {
		return
	}
	a.activityStatusPoller(entry)
}

// desktopActionTerminalStatus maps an action's run error to the terminal
// queue status and message. A nil error succeeds; a context.Canceled error
// is reported as cancelled; anything else fails with the error text.
// Extracted from executeDesktopAction so it stays under the
// cyclomatic-complexity limit; the mapping is unchanged.
func desktopActionTerminalStatus(err error) (activityQueueStatus, string) {
	if err == nil {
		return activityQueueStatusSucceeded, ""
	}
	if errors.Is(err, context.Canceled) {
		return activityQueueStatusCancelled, "cancelled"
	}
	return activityQueueStatusFailed, err.Error()
}

// registerCancel and clearCancel track the active context.CancelFunc
// for each in-flight action so a follow-up cancelWaitingAction can
// cancel the *running* action when the user explicitly opts in (today
// only waiting cancellation is exposed; this hook keeps the plumbing
// ready for a future Cancel-running button).
func (a *App) registerCancel(id string, cancel context.CancelFunc) {
	a.actionQueueMu.Lock()
	defer a.actionQueueMu.Unlock()
	if a.actionCancels == nil {
		a.actionCancels = make(map[string]context.CancelFunc)
	}
	a.actionCancels[id] = cancel
}

func (a *App) clearCancel(id string) {
	a.actionQueueMu.Lock()
	defer a.actionQueueMu.Unlock()
	delete(a.actionCancels, id)
}

// CancelWaitingAction removes a waiting (queued but not yet started)
// action from its env-queue and finalizes the entry as cancelled. No-op
// for actions already running, finalized, or unknown — returns false in
// those cases. Wails-exported.
func (a *App) CancelWaitingAction(id string) bool {
	if a == nil || a.activityQueue == nil {
		return false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	entry, ok := a.activityQueue.findByID(id)
	if !ok {
		return false
	}
	if entry.Status != activityQueueStatusWaiting {
		return false
	}
	if final, finished := a.activityQueue.finish(id, activityQueueStatusCancelled, "cancelled before start"); finished {
		a.unlockTerminalsForActivity(final)
		// The worker goroutine will pop this action eventually and
		// observe its terminal status via findByID; executeDesktopAction
		// short-circuits when status is already terminal.
		return true
	}
	return false
}

// findByID is a convenience for callers (the runner, recovery actions)
// that need a single entry without iterating the full snapshot.
func (s *activityQueueStore) findByID(id string) (activityQueueEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.active[id]; ok {
		return *cloneActivityQueueEntry(entry), true
	}
	for _, entry := range s.history {
		if entry.ID == id {
			return *cloneActivityQueueEntry(entry), true
		}
	}
	return activityQueueEntry{}, false
}

// gatedSessionStarter is the closure provided by Wails-exported
// session methods (StartSession, StartAISession). It runs once the
// runner pops the action and produces both the result the Wails
// caller wants AND the managedTerminal the runner uses to wait for
// the session-ready signal.
type gatedSessionStarter func(ctx context.Context) (startSessionResult, *managedTerminal, error)

// gatedSessionMaxSetup is the upper bound on how long the runner will
// hold the gate waiting for a session's setup-complete marker. The
// session-ready detector (signalSessionReadyOnLine) covers the common
// markers, but if all of them are missed the gate must still release
// so the queue can drain. The bound is intentionally generous enough
// to span a cold cache build+deploy (~5 min) without prematurely
// releasing.
const gatedSessionMaxSetup = 6 * time.Minute

// enqueueGatedSession is the shared helper used by StartSession /
// StartAISession (and any future PTY-backed gated session). It
// enqueues a desktop action keyed on the selection, blocks the Wails
// caller until the action runs and the session is created, and lets
// the runner hold the gate until the session signals ready.
func (a *App) enqueueGatedSession(selection uiSelection, kind string, start gatedSessionStarter) (startSessionResult, error) {
	type spawnOutcome struct {
		result startSessionResult
		err    error
	}
	ready := make(chan spawnOutcome, 1)

	if _, err := a.enqueueDesktopAction(desktopAction{
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
			waitErr := managed.waitReady(ctx, gatedSessionMaxSetup)
			// Treat the safety-net timeout as a successful release.
			// If we hit it, the session is interactive but its
			// setup-complete marker wasn't matched (regex drift,
			// custom shell, etc.). Failing the entry here would be
			// misleading — the user's session is fine; the gate just
			// took longer than expected to release.
			if errors.Is(waitErr, context.DeadlineExceeded) {
				return nil
			}
			return waitErr
		},
	}); err != nil {
		return startSessionResult{}, err
	}

	outcome := <-ready
	return outcome.result, outcome.err
}

// stopActionRunners closes every per-env channel so worker goroutines
// drain and exit. Called from App.shutdown.
func (a *App) stopActionRunners() {
	a.actionQueueMu.Lock()
	queues := a.actionQueues
	a.actionQueues = nil
	cancels := a.actionCancels
	a.actionCancels = nil
	a.actionQueueMu.Unlock()
	for _, queue := range queues {
		close(queue.pending)
	}
	for _, cancel := range cancels {
		cancel()
	}
}
