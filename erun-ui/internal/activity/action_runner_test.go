package activity

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestRunnerSerializesSameEnv(t *testing.T) {
	service := newTestService(Deps{})
	defer service.StopActionRunners()

	var mu sync.Mutex
	var sequence []int
	gate := make(chan struct{})
	for i := 0; i < 3; i++ {
		// Distinct ids: Windows' coarse clock would give auto ids equal timestamps and dedup them.
		_, err := service.enqueueDesktopAction(desktopAction{
			id:        fmt.Sprintf("serialize-%d", i),
			kind:      "open",
			selection: uimodel.Selection{Tenant: "erun", Environment: "local"},
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
	service := newTestService(Deps{})
	defer service.StopActionRunners()

	bothStarted := make(chan struct{}, 2)
	releaseA := make(chan struct{})
	releaseB := make(chan struct{})

	_, err := service.enqueueDesktopAction(desktopAction{
		kind:      "open",
		selection: uimodel.Selection{Tenant: "a", Environment: "dev"},
		run: func(ctx context.Context) error {
			bothStarted <- struct{}{}
			<-releaseA
			return nil
		},
	})
	if err != nil {
		t.Fatalf("enqueue A: %v", err)
	}
	_, err = service.enqueueDesktopAction(desktopAction{
		kind:      "open",
		selection: uimodel.Selection{Tenant: "b", Environment: "dev"},
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
	service := newTestService(Deps{})
	defer service.StopActionRunners()

	gate := make(chan struct{})
	firstStarted := make(chan struct{})

	firstID, err := service.enqueueDesktopAction(desktopAction{
		kind:      "open",
		selection: uimodel.Selection{Tenant: "erun", Environment: "local"},
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

	secondID, err := service.enqueueDesktopAction(desktopAction{
		kind:      "deploy",
		selection: uimodel.Selection{Tenant: "erun", Environment: "local"},
		run: func(ctx context.Context) error {
			t.Fatal("cancelled action should not run")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("enqueue second: %v", err)
	}

	if !service.CancelWaitingAction(secondID) {
		t.Fatalf("CancelWaitingAction(second) returned false; want true")
	}
	if service.CancelWaitingAction(secondID) {
		t.Fatalf("CancelWaitingAction(second) twice should be no-op")
	}
	close(gate)

	if !waitFor(t, 2*time.Second, func() bool {
		entry, ok := service.queue.findByID(secondID)
		return ok && entry.Status == StatusCancelled
	}) {
		t.Fatalf("cancelled entry never reached cancelled status")
	}
	if !waitFor(t, 2*time.Second, func() bool {
		entry, ok := service.queue.findByID(firstID)
		return ok && entry.Status == StatusSucceeded
	}) {
		t.Fatalf("first action never finalized as succeeded")
	}
}

func TestRunnerActionErrorFinalizesAsFailed(t *testing.T) {
	service := newTestService(Deps{})
	defer service.StopActionRunners()

	id, err := service.enqueueDesktopAction(desktopAction{
		kind:      "open",
		selection: uimodel.Selection{Tenant: "erun", Environment: "local"},
		run: func(ctx context.Context) error {
			return errors.New("boom")
		},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if !waitFor(t, 2*time.Second, func() bool {
		entry, ok := service.queue.findByID(id)
		return ok && entry.Status == StatusFailed
	}) {
		t.Fatalf("entry never reached failed status")
	}
	entry, _ := service.queue.findByID(id)
	if entry.Error != "boom" {
		t.Fatalf("error message lost: %q", entry.Error)
	}
}

func TestRunnerCancelledRunningActionFinalizesAsCancelled(t *testing.T) {
	service := newTestService(Deps{})
	defer service.StopActionRunners()

	started := make(chan struct{})
	id, err := service.enqueueDesktopAction(desktopAction{
		kind:      "open",
		selection: uimodel.Selection{Tenant: "erun", Environment: "local"},
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

	service.actionQueueMu.Lock()
	cancel := service.actionCancels[id]
	service.actionQueueMu.Unlock()
	if cancel == nil {
		t.Fatalf("expected cancel func registered for running action")
	}
	cancel()

	if !waitFor(t, 2*time.Second, func() bool {
		entry, ok := service.queue.findByID(id)
		return ok && entry.Status == StatusCancelled
	}) {
		t.Fatalf("ctx-cancelled action did not finalize as cancelled")
	}
}
