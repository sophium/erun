package investigate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adrg/xdg"
	jobspkg "github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/infra/activity/activitylease"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Fixtures are report shapes observed on the environment that ran out of quota.

const (
	// No command, status, or output.
	investigateThinDeployReport = "deploy blew up"
	investigateThinHelmReport   = "deploy failed: helm timeout"
	investigateThinProbeReport  = "probe 1"
)

const investigateHelmTimeoutReport = `erun deploy failed
Target: frs/dev
Version: 1.0.179
Release: frs-devops
Namespace: frs-dev
Started: 2026-08-16T06:01:12Z
Elapsed: 4s

Error: ==> Deploy failed after 4s

Output:
helm upgrade --install frs-devops ./chart
Error: UPGRADE FAILED: timed out waiting for the condition`

// fakeSessions stands in for the orchestrator sessions a spawn starts.
type fakeSessions struct {
	mu      sync.Mutex
	live    map[string]bool
	tracked bool
	started []Transient
}

func (f *fakeSessions) start(spawn Transient) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.live[spawn.ID] = true
	f.started = append(f.started, spawn)
	return nil
}

func (f *fakeSessions) running(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.live[id]
}

func (f *fakeSessions) stop(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.live, id)
}

// pid is this test process for a tracked session, so the job it attaches reads as alive.
func (f *fakeSessions) pid(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.tracked || !f.live[id] {
		return 0
	}
	return os.Getpid()
}

func (f *fakeSessions) liveIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.live))
	for id := range f.live {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

type capturedEmits struct {
	mu     sync.Mutex
	byName map[string][]any
}

func (c *capturedEmits) emit(name string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(args) > 0 {
		c.byName[name] = append(c.byName[name], args[0])
	}
}

func (c *capturedEmits) events(name string) []any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]any(nil), c.byName[name]...)
}

// investigationHarness injects the clock and lifetime timer, so no test sleeps.
type investigationHarness struct {
	service  *Service
	sessions *fakeSessions
	emits    *capturedEmits
	mu       sync.Mutex
	now      time.Time
	pending  []func()
}

func newInvestigationHarness(t *testing.T) *investigationHarness {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)

	harness := &investigationHarness{
		sessions: &fakeSessions{live: map[string]bool{}},
		emits:    &capturedEmits{byName: map[string][]any{}},
		now:      time.Date(2026, 8, 16, 6, 0, 0, 0, time.UTC),
	}
	harness.service = New(Deps{
		Running:     harness.sessions.running,
		StopSession: harness.sessions.stop,
		SessionPID:  harness.sessions.pid,
		HostWorkspace: func(uimodel.Selection) (string, error) {
			return "", errors.New("no host workspace in this test")
		},
		Emit:      harness.emits.emit,
		ReportDir: t.TempDir(),
		Now:       harness.clock,
		After: func(_ time.Duration, fire func()) Timer {
			harness.mu.Lock()
			defer harness.mu.Unlock()
			harness.pending = append(harness.pending, fire)
			return stubInvestigationTimer{}
		},
	})
	t.Cleanup(harness.service.StopTimers)
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

// investigate is InvestigateFailure as the facade runs it, returning the session it started.
func (h *investigationHarness) investigate(report, tenant, environment string) (Transient, error) {
	var started Transient
	err := h.service.Spawn(InvestigateRequest(report, tenant, environment, 80, 24), func(spawn Transient) error {
		started = spawn
		return h.sessions.start(spawn)
	})
	return started, err
}

func (h *investigationHarness) investigateWithTrackedProcess(t *testing.T, report, tenant, environment string) Transient {
	t.Helper()
	h.sessions.mu.Lock()
	h.sessions.tracked = true
	h.sessions.mu.Unlock()
	started, err := h.investigate(report, tenant, environment)
	if err != nil {
		t.Fatalf("investigate failed: %v", err)
	}
	return started
}

func investigateReport(target, failure string) string {
	return fmt.Sprintf("erun deploy failed\nTarget: %s\nVersion: 1.0.179\nStarted: 2026-08-16T06:01:12Z\nElapsed: 4s\n\nError: %s\n\nOutput:\nhelm upgrade --install devops ./chart\n%s\n", target, failure, failure)
}

func refusalReason(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("expected the investigation to be refused, but it was admitted")
	}
	return investigationRefusalReason(err)
}

func assertNoSessions(t *testing.T, h *investigationHarness, context string) {
	t.Helper()
	if live := h.sessions.liveIDs(); len(live) != 0 {
		t.Fatalf("%s: expected no orchestrator session, got %v", context, live)
	}
}

func TestEmptyReportIsRejectedBeforeAnythingIsStaged(t *testing.T) {
	harness := newInvestigationHarness(t)
	if _, err := harness.investigate("   ", "frs", "dev"); err == nil {
		t.Fatal("expected an empty failure report to be rejected")
	}
	if staged := stagedReports(t, harness.service.registry.reportDir); len(staged) != 0 {
		t.Fatalf("an empty report must stage nothing, got %v", staged)
	}
}

func TestAFailedSpawnHoldsNoSlot(t *testing.T) {
	harness := newInvestigationHarness(t)
	err := harness.service.Spawn(InvestigateRequest(investigateHelmTimeoutReport, "frs", "dev", 80, 24), func(Transient) error {
		return errors.New("spawn refused")
	})
	if err == nil || err.Error() != "spawn refused" {
		t.Fatalf("expected the spawn's own error, got %v", err)
	}
	if _, err := harness.investigate(investigateHelmTimeoutReport, "frs", "dev"); err != nil {
		t.Fatalf("a failed spawn must start no cooldown and hold no capacity: %v", err)
	}
}

func TestInvestigationIsSeededWithTheStagedReport(t *testing.T) {
	harness := newInvestigationHarness(t)
	started, err := harness.investigate(investigateHelmTimeoutReport, "frs", "dev")
	if err != nil {
		t.Fatalf("investigate failed: %v", err)
	}
	if started.Name != "Investigate dev" || len(started.Envs) != 1 || started.Envs[0].Tenant != "frs" || started.Envs[0].Environment != "dev" {
		t.Fatalf("expected a session named for and scoped to frs/dev, got %+v", started)
	}
	if !strings.HasPrefix(started.ID, "investigate-") {
		t.Fatalf("expected an investigate id, got %q", started.ID)
	}
	staged := stagedReports(t, harness.service.registry.reportDir)
	if len(staged) != 1 || !strings.Contains(started.InitialPrompt, staged[0]) || !strings.Contains(started.InitialPrompt, "erun-file-issue") {
		t.Fatalf("the prompt must name the staged report %v, got %q", staged, started.InitialPrompt)
	}
}

func TestReportWithNoDiagnosticContentSpawnsNothing(t *testing.T) {
	for _, report := range []string{
		investigateThinDeployReport,
		investigateThinHelmReport,
		investigateThinProbeReport,
		"e2e probe for issue 1006",
	} {
		t.Run(report, func(t *testing.T) {
			harness := newInvestigationHarness(t)
			_, err := harness.investigate(report, "frs", "dev")
			if reason := refusalReason(t, err); reason != "thin-report" {
				t.Fatalf("expected a thin-report refusal, got %q (%v)", reason, err)
			}
			if !strings.Contains(err.Error(), "no diagnostic content") {
				t.Fatalf("the refusal must name the missing evidence, got %q", err)
			}
			assertNoSessions(t, harness, "thin report")

			staged := stagedReports(t, harness.service.registry.reportDir)
			if len(staged) != 1 {
				t.Fatalf("expected the refused report to be staged, got %d files", len(staged))
			}
			if data, err := os.ReadFile(staged[0]); err != nil || !strings.Contains(string(data), report) {
				t.Fatalf("staged report %q does not carry the reported text (%v)", staged[0], err)
			}
		})
	}
}

func TestReportWithDiagnosticContentStillSpawnsAnInvestigation(t *testing.T) {
	harness := newInvestigationHarness(t)
	if _, err := harness.investigate(investigateHelmTimeoutReport, "frs", "dev"); err != nil {
		t.Fatalf("a report with a command, an error, and captured output must be admitted: %v", err)
	}
	if listed := harness.sessions.liveIDs(); len(listed) != 1 {
		t.Fatalf("expected exactly one investigation, got %+v", listed)
	}
}

func TestSecondReportOfOneFailureEventSpawnsNothing(t *testing.T) {
	harness := newInvestigationHarness(t)
	if _, err := harness.investigate(investigateReport("frs/dev", "UPGRADE FAILED: timed out"), "frs", "dev"); err != nil {
		t.Fatalf("first report: %v", err)
	}
	harness.advance(10 * time.Millisecond)
	_, err := harness.investigate(investigateReport("frs/dev", "release frs-devops has no deployed revision"), "frs", "dev")
	if reason := refusalReason(t, err); reason != "same-event" {
		t.Fatalf("expected the second report to fold into the same event, got %q (%v)", reason, err)
	}
	if listed := harness.sessions.liveIDs(); len(listed) != 1 {
		t.Fatalf("one event must spawn one investigation, got %+v", listed)
	}
	records := harness.service.registry.list()
	if len(records) != 1 || records[0].Suppressed != 1 {
		t.Fatalf("the folded report must be counted against the investigation, got %+v", records)
	}
}

func TestRepeatOfAFailureAlreadyUnderInvestigationSpawnsNothing(t *testing.T) {
	harness := newInvestigationHarness(t)
	report := investigateReport("frs/dev", "UPGRADE FAILED: timed out")
	if _, err := harness.investigate(report, "frs", "dev"); err != nil {
		t.Fatalf("first report: %v", err)
	}
	harness.advance(EventWindow + time.Minute)
	_, err := harness.investigate(report, "frs", "dev")
	if reason := refusalReason(t, err); reason != "already-investigating" {
		t.Fatalf("expected a refusal naming the running investigation, got %q (%v)", reason, err)
	}
	if listed := harness.sessions.liveIDs(); len(listed) != 1 {
		t.Fatalf("expected the population to stay at one, got %+v", listed)
	}
}

func TestRepeatOfAnEndedInvestigationWaitsOutTheCooldown(t *testing.T) {
	harness := newInvestigationHarness(t)
	report := investigateReport("frs/dev", "UPGRADE FAILED: timed out")
	first, err := harness.investigate(report, "frs", "dev")
	if err != nil {
		t.Fatalf("first report: %v", err)
	}
	harness.sessions.stop(first.ID)
	assertNoSessions(t, harness, "after the first investigation ended")

	harness.advance(investigationCooldown - time.Minute)
	_, err = harness.investigate(report, "frs", "dev")
	if reason := refusalReason(t, err); reason != "cooldown" {
		t.Fatalf("expected a cooldown refusal inside the window, got %q (%v)", reason, err)
	}
	assertNoSessions(t, harness, "inside the cooldown")

	harness.advance(2 * time.Minute)
	if _, err := harness.investigate(report, "frs", "dev"); err != nil {
		t.Fatalf("the cooldown must release: %v", err)
	}
	if listed := harness.sessions.liveIDs(); len(listed) != 1 {
		t.Fatalf("expected one investigation after the cooldown released, got %+v", listed)
	}
}

func TestConcurrentFailureEventsCannotOutrunTheCap(t *testing.T) {
	harness := newInvestigationHarness(t)
	const events = 6
	var wait sync.WaitGroup
	admitted := make([]bool, events)
	for index := range events {
		wait.Add(1)
		go func() {
			defer wait.Done()
			environment := fmt.Sprintf("env%d", index)
			_, err := harness.investigate(investigateReport("frs/"+environment, "UPGRADE FAILED: timed out"), "frs", environment)
			admitted[index] = err == nil
		}()
	}
	wait.Wait()

	count := 0
	for _, ok := range admitted {
		if ok {
			count++
		}
	}
	if count != maxLiveInvestigations {
		t.Fatalf("expected exactly %d of %d concurrent events to be admitted, got %d", maxLiveInvestigations, events, count)
	}
	if listed := harness.sessions.liveIDs(); len(listed) != maxLiveInvestigations {
		t.Fatalf("expected %d live sessions, got %+v", maxLiveInvestigations, listed)
	}
	harness.sessions.stop(harness.sessions.liveIDs()[0])
	if _, err := harness.investigate(investigateReport("frs/spare", "UPGRADE FAILED: timed out"), "frs", "spare"); err != nil {
		t.Fatalf("a freed slot must admit the next event: %v", err)
	}
}

func TestInvestigationPastItsLifetimeBoundIsTerminated(t *testing.T) {
	harness := newInvestigationHarness(t)
	harness.investigateWithTrackedProcess(t, investigateHelmTimeoutReport, "frs", "dev")
	if leases := activityLeaseIDs(t, "frs", "dev"); len(leases) != 1 {
		t.Fatalf("a running investigation must hold one activity lease, got %v", leases)
	}

	harness.advance(investigationLifetime)
	harness.reachLifetimeBound()

	assertNoSessions(t, harness, "after the lifetime bound")
	if leases := activityLeaseIDs(t, "frs", "dev"); len(leases) != 0 {
		t.Fatalf("a terminated investigation must not keep the environment busy, got %v", leases)
	}
	records := harness.service.registry.list()
	if len(records) != 1 || records[0].State != investigationExpired {
		t.Fatalf("expected the investigation recorded as expired, got %+v", records)
	}
	if data, err := os.ReadFile(records[0].LogPath); err != nil || !strings.Contains(string(data), "terminated:") {
		t.Fatalf("the investigation log must say it was terminated (%v): %q", err, data)
	}
	if !emittedNotificationContains(harness.emits, "was stopped after") {
		t.Fatalf("the operator must be told the investigation was stopped, got %+v", harness.emits.events(uimodel.AppNotificationEvent))
	}
	if _, err := harness.investigate(investigateReport("frs/other", "ImagePullBackOff"), "frs", "other"); err != nil {
		t.Fatalf("the freed slot must admit a new investigation: %v", err)
	}
}

func TestRunningInvestigationIsVisibleAsAnEnvironmentJob(t *testing.T) {
	harness := newInvestigationHarness(t)
	info := harness.investigateWithTrackedProcess(t, investigateHelmTimeoutReport, "frs", "dev")

	job := soleEnvironmentJob(t, "frs", "dev")
	if job.State != jobspkg.EnvironmentJobStateRunning {
		t.Fatalf("expected a running job, got %+v", job)
	}
	if job.ID != info.ID || job.LeaseID == "" {
		t.Fatalf("expected the investigation to be the job, holding a lease, got %+v", job)
	}
	if !strings.Contains(job.Name, "Investigate") {
		t.Fatalf("the job must name what it is, got %q", job.Name)
	}
	output, err := os.ReadFile(job.LogPath)
	if err != nil {
		t.Fatalf("the job's output must be readable: %v", err)
	}
	assertContainsAll(t, "job output", string(output), info.ID, "lifetime bound", "report:")
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

func assertContainsAll(t *testing.T, label, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("%s must carry %q, got %q", label, want, text)
		}
	}
}

func TestSignatureIdentifiesTheFailureRatherThanTheOccurrence(t *testing.T) {
	first := "erun deploy failed\nTarget: frs/dev\nVersion: 1.0.179\nStarted: 2026-08-16T06:01:12Z\nElapsed: 4s\n\nError: UPGRADE FAILED: timed out waiting for the condition\n"
	repeat := "erun deploy failed\nTarget: frs/dev\nVersion: 1.0.181\nStarted: 2026-08-16T11:44:02Z\nElapsed: 71s\n\nError: UPGRADE FAILED: timed out waiting for the condition\n"
	other := "erun deploy failed\nTarget: frs/dev\nVersion: 1.0.179\nStarted: 2026-08-16T06:01:12Z\nElapsed: 4s\n\nError: ImagePullBackOff on container runtime\n"

	if investigationSignature(first, "frs", "dev") != investigationSignature(repeat, "frs", "dev") {
		t.Fatal("the same failure reported twice must share one signature")
	}
	if investigationSignature(first, "frs", "dev") == investigationSignature(other, "frs", "dev") {
		t.Fatal("two different failures must not share a signature")
	}
	if investigationSignature(first, "frs", "dev") == investigationSignature(first, "frs", "prod") {
		t.Fatal("the same failure in another environment is another failure")
	}
}

func TestStagedReportsArePrunedToTheRetentionBound(t *testing.T) {
	dir := t.TempDir()
	registry := newInvestigationRegistry(dir)
	for index := range investigationReportRetention + 12 {
		if _, err := registry.stageReport(fmt.Sprintf("report %d", index)); err != nil {
			t.Fatalf("stage report %d: %v", index, err)
		}
	}
	if staged := stagedReports(t, dir); len(staged) != investigationReportRetention {
		t.Fatalf("expected the report directory bounded at %d, got %d", investigationReportRetention, len(staged))
	}
}

func stagedReports(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "report-*.md"))
	if err != nil {
		t.Fatalf("list staged reports: %v", err)
	}
	return matches
}

func activityLeaseIDs(t *testing.T, tenant, environment string) []string {
	t.Helper()
	leases, err := activitylease.LoadEnvironmentActivityLeases(tenant, environment, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentActivityLeases failed: %v", err)
	}
	out := make([]string, 0, len(leases))
	for _, lease := range leases {
		out = append(out, lease.ID)
	}
	return out
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
