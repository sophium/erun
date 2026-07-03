package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// desktopAction is one unit of work the runner serializes; its run
// closure must honour ctx cancellation.
type desktopAction struct {
	id        string
	kind      string
	summary   string
	selection uiSelection
	run       func(ctx context.Context) error
}

// envActionQueue serializes work for one (tenant, env). Its channel is
// buffered so enqueue never blocks the Wails caller and a user clicking
// through the UI can queue requests without dropping them.
type envActionQueue struct {
	pending chan *desktopAction
}

const desktopActionQueueDepth = 64

// envActionQueueKey groups all tenant/env-less actions (cloud provider
// login, etc.) under one global queue so they serialize among
// themselves without blocking per-env work.
func envActionQueueKey(selection uiSelection) string {
	tenant := strings.TrimSpace(selection.Tenant)
	env := strings.TrimSpace(selection.Environment)
	if tenant == "" && env == "" {
		return "__global__"
	}
	return tenant + "\x00" + env
}

// enqueueDesktopAction returns an error only for setup failures (no
// activity store, malformed action); the action's own errors surface
// through the entry's terminal status, not the return value.
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
		// Duplicate of an in-flight entry: collapse onto it and discard
		// this payload rather than double-running the action.
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

func (a *App) runEnvActionWorker(key string, queue *envActionQueue) {
	for action := range queue.pending {
		a.executeDesktopAction(action)
	}
	_ = key
}

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
		return
	}
	promoted, ok := a.activityQueue.promoteToRunning(action.id)
	if !ok {
		return
	}
	// Re-emit the running snapshot so a dropped activity:state event or
	// a late-mounted frontend still sees the transition; safe because
	// the frontend dedupes identical payloads.
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

func (a *App) startActivityStatusPollerForAction(action *desktopAction, entry activityQueueEntry) {
	if action.kind != "deploy" && action.kind != "force-deploy" {
		return
	}
	if a.activityStatusPoller == nil {
		return
	}
	a.activityStatusPoller(entry)
}

func desktopActionTerminalStatus(err error) (activityQueueStatus, string) {
	if err == nil {
		return activityQueueStatusSucceeded, ""
	}
	if errors.Is(err, context.Canceled) {
		return activityQueueStatusCancelled, "cancelled"
	}
	return activityQueueStatusFailed, err.Error()
}

// registerCancel/clearCancel retain per-action cancel plumbing so a
// running action can be cancelled. Only waiting cancellation is exposed
// today; this keeps a future Cancel-running button ready.
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

// CancelWaitingAction cancels a queued-but-not-yet-started action;
// no-op (returns false) once it is running, finalized, or unknown.
// Wails-exported.
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
		// The queued action stays in the channel; the worker pops it
		// later and skips it because the entry is now terminal.
		return true
	}
	return false
}

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

// gatedSessionStarter returns both the result the Wails caller wants
// and the managedTerminal the runner waits on for the session-ready
// signal.
type gatedSessionStarter func(ctx context.Context) (startSessionResult, *managedTerminal, error)

// gatedSessionMaxSetup caps how long the runner holds the gate waiting
// for a session-ready marker: a safety net so the queue still drains if
// every marker is missed, sized generously to span a cold build+deploy.
const gatedSessionMaxSetup = 6 * time.Minute

// enqueueGatedSession blocks the Wails caller until the enqueued action
// runs and creates the session, then holds the gate until the session
// signals ready.
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
			// A safety-net timeout is a successful release, not a
			// failure: the session is interactive, only its ready
			// marker went unmatched, so failing the entry would mislead.
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
