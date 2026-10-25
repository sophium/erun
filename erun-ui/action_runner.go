package main

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
		// Duplicate of an in-flight entry: collapse onto it.
		return entry.ID, nil
	}
	a.rememberKubeContextForActivity(action.selection.KubernetesContext)

	queue := a.ensureEnvActionQueue(envActionQueueKey(action.selection))
	select {
	case queue.pending <- &action:
		return action.id, nil
	default:
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
		// Force-dismissed before its turn.
		return
	}
	if current.Status == activityQueueStatusCancelled || activityQueueStatusIsTerminal(current.Status) {
		return
	}
	promoted, ok := a.activityQueue.promoteToRunning(action.id)
	if !ok {
		return
	}
	// Re-emit for a dropped event or late-mounted frontend; the frontend dedupes.
	a.emitActivityState(promoted)
	a.lockTerminalsForActivity(promoted)
	a.startActivityStatusPollerForAction(action, current)
	ctx, cancel := context.WithCancel(a.context())
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

// Only waiting cancellation is exposed; running cancellation is plumbed for later.
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

// CancelWaitingAction cancels a queued action; returns false once it has started.
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
		// The worker skips the queued action because its entry is now terminal.
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

type gatedSessionStarter func(ctx context.Context) (uimodel.StartSessionResult, *managedTerminal, error)

// gatedSessionMaxSetup is a safety net so the queue drains if every ready marker
// is missed; it spans a cold build+deploy.
const gatedSessionMaxSetup = 6 * time.Minute

// gatedSessionKind's entry stays running only while its terminal is being established.
type gatedSessionKind string

const (
	gatedSessionKindOpen           gatedSessionKind = "open"
	gatedSessionKindAI             gatedSessionKind = "ai"
	gatedSessionKindContributeERun gatedSessionKind = "contribute-erun"
	gatedSessionKindContributeAI   gatedSessionKind = "contribute-ai"
)

func isGatedSessionKind(kind string) bool {
	switch gatedSessionKind(kind) {
	case gatedSessionKindOpen, gatedSessionKindAI, gatedSessionKindContributeERun, gatedSessionKindContributeAI:
		return true
	}
	return false
}

func isGatedSessionSetupEntry(entry activityQueueEntry) bool {
	return entry.Source == "action" && isGatedSessionKind(entry.ActionKind)
}

// enqueueGatedSession blocks until the session is created, then holds the gate
// until it signals ready.
func (a *App) enqueueGatedSession(selection uimodel.Selection, kind gatedSessionKind, start gatedSessionStarter) (uimodel.StartSessionResult, error) {
	if !isGatedSessionKind(string(kind)) {
		return uimodel.StartSessionResult{}, fmt.Errorf("unregistered gated session kind %q", kind)
	}
	type spawnOutcome struct {
		result uimodel.StartSessionResult
		err    error
	}
	ready := make(chan spawnOutcome, 1)

	if _, err := a.enqueueDesktopAction(desktopAction{
		kind:      string(kind),
		selection: selection,
		summary:   string(kind) + " " + strings.TrimSpace(selection.Tenant+"/"+selection.Environment),
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
