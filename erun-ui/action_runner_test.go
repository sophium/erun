package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func newRunnerTestApp(t *testing.T) *App {
	t.Helper()
	app := &App{
		sessions: make(map[string]*managedTerminal),
	}
	app.activityQueue = newActivityQueueStore(nil, nil)
	return app
}

func TestRunnerSerializesSameEnv(t *testing.T) {
	app := newRunnerTestApp(t)
	defer app.stopActionRunners()

	var mu sync.Mutex
	var sequence []int
	gate := make(chan struct{})
	for i := 0; i < 3; i++ {
		i := i
		// Distinct ids keep the three same-env actions from collapsing onto one
		// entry: the auto-generated id embeds time.Now().UnixNano(), and the coarse
		// Windows clock returns the same value for these rapid enqueues, so the
		// in-flight-duplicate dedup would otherwise drop actions 1 and 2.
		_, err := app.enqueueDesktopAction(desktopAction{
			id:        fmt.Sprintf("serialize-%d", i),
			kind:      "open",
			selection: uiSelection{Tenant: "erun", Environment: "local"},
			run: func(ctx context.Context) error {
				mu.Lock()
				sequence = append(sequence, i)
				mu.Unlock()
				if i == 0 {
					<-gate
				}
				return nil
			},
		})
		if err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	if !waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(sequence) == 1
	}) {
		t.Fatalf("first action never started")
	}
	mu.Lock()
	if got := len(sequence); got != 1 {
		mu.Unlock()
		t.Fatalf("expected exactly 1 action started while gate held, got %d (%v)", got, sequence)
	}
	mu.Unlock()
	close(gate)

	if !waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(sequence) == 3
	}) {
		t.Fatalf("not all actions ran (got %v)", sequence)
	}
	mu.Lock()
	got := append([]int(nil), sequence...)
	mu.Unlock()
	for i, n := range got {
		if n != i {
			t.Fatalf("actions ran out of order: %v", got)
		}
	}
}

func TestRunnerDifferentEnvsRunConcurrently(t *testing.T) {
	app := newRunnerTestApp(t)
	defer app.stopActionRunners()

	bothStarted := make(chan struct{}, 2)
	releaseA := make(chan struct{})
	releaseB := make(chan struct{})

	_, err := app.enqueueDesktopAction(desktopAction{
		kind:      "open",
		selection: uiSelection{Tenant: "a", Environment: "dev"},
		run: func(ctx context.Context) error {
			bothStarted <- struct{}{}
			<-releaseA
			return nil
		},
	})
	if err != nil {
		t.Fatalf("enqueue A: %v", err)
	}
	_, err = app.enqueueDesktopAction(desktopAction{
		kind:      "open",
		selection: uiSelection{Tenant: "b", Environment: "dev"},
		run: func(ctx context.Context) error {
			bothStarted <- struct{}{}
			<-releaseB
			return nil
		},
	})
	if err != nil {
		t.Fatalf("enqueue B: %v", err)
	}

	for i := 0; i < 2; i++ {
		select {
		case <-bothStarted:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of 2 actions started (per-env queues should run concurrently)", i)
		}
	}
	close(releaseA)
	close(releaseB)
}

func TestRunnerCancelWaitingActionRemovesEntry(t *testing.T) {
	app := newRunnerTestApp(t)
	defer app.stopActionRunners()

	gate := make(chan struct{})
	firstStarted := make(chan struct{})

	firstID, err := app.enqueueDesktopAction(desktopAction{
		kind:      "open",
		selection: uiSelection{Tenant: "erun", Environment: "local"},
		run: func(ctx context.Context) error {
			close(firstStarted)
			<-gate
			return nil
		},
	})
	if err != nil {
		t.Fatalf("enqueue first: %v", err)
	}
	<-firstStarted

	secondID, err := app.enqueueDesktopAction(desktopAction{
		kind:      "deploy",
		selection: uiSelection{Tenant: "erun", Environment: "local"},
		run: func(ctx context.Context) error {
			t.Fatal("cancelled action should not run")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("enqueue second: %v", err)
	}

	if !app.CancelWaitingAction(secondID) {
		t.Fatalf("CancelWaitingAction(second) returned false; want true")
	}
	if app.CancelWaitingAction(secondID) {
		t.Fatalf("CancelWaitingAction(second) twice should be no-op")
	}
	close(gate)

	if !waitFor(t, 2*time.Second, func() bool {
		entry, ok := app.activityQueue.findByID(secondID)
		return ok && entry.Status == activityQueueStatusCancelled
	}) {
		t.Fatalf("cancelled entry never reached cancelled status")
	}
	if !waitFor(t, 2*time.Second, func() bool {
		entry, ok := app.activityQueue.findByID(firstID)
		return ok && entry.Status == activityQueueStatusSucceeded
	}) {
		t.Fatalf("first action never finalized as succeeded")
	}
}

func TestRunnerActionErrorFinalizesAsFailed(t *testing.T) {
	app := newRunnerTestApp(t)
	defer app.stopActionRunners()

	id, err := app.enqueueDesktopAction(desktopAction{
		kind:      "open",
		selection: uiSelection{Tenant: "erun", Environment: "local"},
		run: func(ctx context.Context) error {
			return errors.New("boom")
		},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if !waitFor(t, 2*time.Second, func() bool {
		entry, ok := app.activityQueue.findByID(id)
		return ok && entry.Status == activityQueueStatusFailed
	}) {
		t.Fatalf("entry never reached failed status")
	}
	entry, _ := app.activityQueue.findByID(id)
	if entry.Error != "boom" {
		t.Fatalf("error message lost: %q", entry.Error)
	}
}

func TestRunnerCancelledRunningActionFinalizesAsCancelled(t *testing.T) {
	app := newRunnerTestApp(t)
	defer app.stopActionRunners()

	started := make(chan struct{})
	id, err := app.enqueueDesktopAction(desktopAction{
		kind:      "open",
		selection: uiSelection{Tenant: "erun", Environment: "local"},
		run: func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-started

	app.actionQueueMu.Lock()
	cancel := app.actionCancels[id]
	app.actionQueueMu.Unlock()
	if cancel == nil {
		t.Fatalf("expected cancel func registered for running action")
	}
	cancel()

	if !waitFor(t, 2*time.Second, func() bool {
		entry, ok := app.activityQueue.findByID(id)
		return ok && entry.Status == activityQueueStatusCancelled
	}) {
		t.Fatalf("ctx-cancelled action did not finalize as cancelled")
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}
