package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adrg/xdg"
	jobspkg "github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
	"github.com/sophium/erun/erun-ui/internal/investigate"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// The investigation facade's tests: the bounds reaching real orchestrator
// sessions. The bounds themselves are tested in internal/investigate.

// No command, status, or output.
const investigateThinDeployReport = "deploy blew up"

// investigationHarness injects the clock and lifetime timer, so no test sleeps.
type investigationHarness struct {
	app     *App
	emits   *capturedEmits
	mu      sync.Mutex
	now     time.Time
	pending []func()
}

func newInvestigationHarness(t *testing.T) *investigationHarness {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)

	app := orchestratorTestApp(t)
	t.Cleanup(func() { app.shutdown(context.Background()) })
	harness := &investigationHarness{
		app:   app,
		emits: newCapturedEmits(),
		now:   time.Date(2026, 8, 16, 6, 0, 0, 0, time.UTC),
	}
	app.SetEmitter(harness.emits.fn())
	app.investigations = app.newInvestigationService(harness.clock, func(_ time.Duration, fire func()) investigate.Timer {
		harness.mu.Lock()
		defer harness.mu.Unlock()
		harness.pending = append(harness.pending, fire)
		return stubInvestigationTimer{}
	})
	return harness
}

func (h *investigationHarness) clock() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func (h *investigationHarness) advance(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.now = h.now.Add(d)
}

func (h *investigationHarness) reachLifetimeBound() {
	h.mu.Lock()
	pending := h.pending
	h.pending = nil
	h.mu.Unlock()
	for _, fire := range pending {
		fire()
	}
}

type stubInvestigationTimer struct{}

func (stubInvestigationTimer) Stop() bool { return true }

// trackSessionProcess gives each started session this test process's pid, so
// the job it attaches reads as alive.
func (h *investigationHarness) trackSessionProcess() {
	h.app.deps.startTerminal = func(terminal.StartParams) (terminal.PTY, error) {
		session := newStubTerminalSession()
		session.pid = os.Getpid()
		return session, nil
	}
}

func investigateReport(target, failure string) string {
	return fmt.Sprintf("erun deploy failed\nTarget: %s\nVersion: 1.0.179\nStarted: 2026-08-16T06:01:12Z\nElapsed: 4s\n\nError: %s\n\nOutput:\nhelm upgrade --install devops ./chart\n%s\n", target, failure, failure)
}

func assertNoSessions(t *testing.T, app *App, context string) {
	t.Helper()
	if listed := app.ListOrchestrators(); len(listed) != 0 {
		t.Fatalf("%s: expected no orchestrator session, got %+v", context, listed)
	}
}

func soleEnvironmentJob(t *testing.T, tenant, environment string) jobstate.EnvironmentJob {
	t.Helper()
	jobs, err := jobspkg.LoadEnvironmentJobs(tenant, environment, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJobs failed: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected the investigation to appear as one job, got %+v", jobs)
	}
	return jobs[0]
}

// The lifetime bound stops the real orchestrator session, releases its lease
// and tells the operator, which frees the slot for the next investigation.
func TestInvestigationPastItsLifetimeBoundStopsItsOrchestratorSession(t *testing.T) {
	harness := newInvestigationHarness(t)
	harness.trackSessionProcess()
	info, err := harness.app.InvestigateFailure(investigateHelmTimeoutReport, "frs", "dev", 80, 24)
	if err != nil {
		t.Fatalf("InvestigateFailure failed: %v", err)
	}
	if job := soleEnvironmentJob(t, "frs", "dev"); job.ID != info.ID || job.LeaseID == "" {
		t.Fatalf("expected the investigation to be the job, holding a lease, got %+v", job)
	}

	harness.reachLifetimeBound()

	assertNoSessions(t, harness.app, "after the lifetime bound")
	leases, err := activity.LoadEnvironmentActivityLeases("frs", "dev", time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentActivityLeases failed: %v", err)
	}
	if len(leases) != 0 {
		t.Fatalf("a terminated investigation must not keep the environment busy, got %+v", leases)
	}
	if !emittedNotificationContains(harness.emits, "was stopped after") {
		t.Fatalf("the operator must be told the investigation was stopped, got %+v", harness.emits.events(uimodel.AppNotificationEvent))
	}
	if _, err := harness.app.InvestigateFailure(investigateReport("frs/other", "ImagePullBackOff"), "frs", "other", 80, 24); err != nil {
		t.Fatalf("the freed slot must admit a new investigation: %v", err)
	}
}

func emittedNotificationContains(emits *capturedEmits, want string) bool {
	for _, event := range emits.events(uimodel.AppNotificationEvent) {
		payload, ok := event.(uimodel.AppNotificationPayload)
		if !ok {
			continue
		}
		if strings.Contains(payload.Message, want) {
			return true
		}
	}
	return false
}
