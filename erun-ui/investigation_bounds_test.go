package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adrg/xdg"
	eruncommon "github.com/sophium/erun/erun-common"
)

// These lock the spawn decision by what it produces, not by what it logs: a
// refused report leaves no session behind, the cap holds when events arrive at
// once, and an investigation past its bound is actually stopped. Every fixture
// below is a report shape observed in /tmp/erun-investigate on the environment
// that ran out of agent quota.

const (
	// The two reports that appeared in pairs milliseconds apart, one failure
	// each time. Neither names a command, a status, or any output.
	investigateThinDeployReport = "deploy blew up"
	investigateThinHelmReport   = "deploy failed: helm timeout"
	// A 7-byte report, the smallest on disk.
	investigateThinProbeReport = "probe 1"
)

// investigationHarness drives the bounds without waiting on wall-clock time:
// the registry's clock and its lifetime timer are both injected, so "two hours
// later" and "the bound elapsed" are assertions rather than sleeps.
type investigationHarness struct {
	app     *App
	emits   *capturedEmits
	mu      sync.Mutex
	now     time.Time
	pending []func()
}

func newInvestigationHarness(t *testing.T) *investigationHarness {
	t.Helper()
	// The job and lease an investigation registers live in the activity cache;
	// isolate it so a test never writes into the developer's own tree.
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
	app.investigations.now = harness.clock
	app.investigations.after = func(_ time.Duration, fire func()) investigationTimer {
		harness.mu.Lock()
		defer harness.mu.Unlock()
		harness.pending = append(harness.pending, fire)
		return stubInvestigationTimer{}
	}
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

// reachLifetimeBound fires every armed lifetime timer, which is what a real
// clock does after investigationLifetime.
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

func assertNoSessions(t *testing.T, app *App, context string) {
	t.Helper()
	if listed := app.ListOrchestrators(); len(listed) != 0 {
		t.Fatalf("%s: expected no orchestrator session, got %+v", context, listed)
	}
}

// The floor on the input. Each of these is a report that reached an agent in the
// environment that ran out of quota; none of them carries anything to
// investigate, and the missing evidence is the bug worth fixing.
func TestReportWithNoDiagnosticContentSpawnsNothing(t *testing.T) {
	for _, report := range []string{
		investigateThinDeployReport,
		investigateThinHelmReport,
		investigateThinProbeReport,
		"e2e probe for issue 1006",
	} {
		t.Run(report, func(t *testing.T) {
			harness := newInvestigationHarness(t)
			_, err := harness.app.InvestigateFailure(report, "frs", "dev", 80, 24)
			if reason := refusalReason(t, err); reason != "thin-report" {
				t.Fatalf("expected a thin-report refusal, got %q (%v)", reason, err)
			}
			if !strings.Contains(err.Error(), "no diagnostic content") {
				t.Fatalf("the refusal must name the missing evidence, got %q", err)
			}
			assertNoSessions(t, harness.app, "thin report")

			// The report is still recorded: it is the evidence of the reporting gap.
			staged := stagedReports(t, harness.app.investigations.reportDir)
			if len(staged) != 1 {
				t.Fatalf("expected the refused report to be staged, got %d files", len(staged))
			}
			if data, err := os.ReadFile(staged[0]); err != nil || !strings.Contains(string(data), report) {
				t.Fatalf("staged report %q does not carry the reported text (%v)", staged[0], err)
			}
		})
	}
}

// A report the desktop's failure card actually produces carries the command,
// the target, the error, and the captured output, and must still get an agent.
func TestReportWithDiagnosticContentStillSpawnsAnInvestigation(t *testing.T) {
	harness := newInvestigationHarness(t)
	info, err := harness.app.InvestigateFailure(investigateHelmTimeoutReport, "frs", "dev", 80, 24)
	if err != nil {
		t.Fatalf("a report with a command, an error, and captured output must be admitted: %v", err)
	}
	if !info.Transient {
		t.Fatalf("expected a transient investigator, got %+v", info)
	}
	if listed := harness.app.ListOrchestrators(); len(listed) != 1 {
		t.Fatalf("expected exactly one investigation, got %+v", listed)
	}
}

// The pair of reports that arrive milliseconds apart is one failure event, so it
// gets one investigation — whatever the second report says.
func TestSecondReportOfOneFailureEventSpawnsNothing(t *testing.T) {
	harness := newInvestigationHarness(t)
	if _, err := harness.app.InvestigateFailure(investigateReport("frs/dev", "UPGRADE FAILED: timed out"), "frs", "dev", 80, 24); err != nil {
		t.Fatalf("first report: %v", err)
	}
	harness.advance(10 * time.Millisecond)
	_, err := harness.app.InvestigateFailure(investigateReport("frs/dev", "release frs-devops has no deployed revision"), "frs", "dev", 80, 24)
	if reason := refusalReason(t, err); reason != "same-event" {
		t.Fatalf("expected the second report to fold into the same event, got %q (%v)", reason, err)
	}
	if listed := harness.app.ListOrchestrators(); len(listed) != 1 {
		t.Fatalf("one event must spawn one investigation, got %+v", listed)
	}
	records := harness.app.investigations.list()
	if len(records) != 1 || records[0].Suppressed != 1 {
		t.Fatalf("the folded report must be counted against the investigation, got %+v", records)
	}
}

// The same failure reported again, past the event window, must reach the running
// investigation rather than a second agent.
func TestRepeatOfAFailureAlreadyUnderInvestigationSpawnsNothing(t *testing.T) {
	harness := newInvestigationHarness(t)
	report := investigateReport("frs/dev", "UPGRADE FAILED: timed out")
	if _, err := harness.app.InvestigateFailure(report, "frs", "dev", 80, 24); err != nil {
		t.Fatalf("first report: %v", err)
	}
	harness.advance(investigationEventWindow + time.Minute)
	_, err := harness.app.InvestigateFailure(report, "frs", "dev", 80, 24)
	if reason := refusalReason(t, err); reason != "already-investigating" {
		t.Fatalf("expected a refusal naming the running investigation, got %q (%v)", reason, err)
	}
	if listed := harness.app.ListOrchestrators(); len(listed) != 1 {
		t.Fatalf("expected the population to stay at one, got %+v", listed)
	}
}

// A failure that keeps repeating must not accumulate agents once its
// investigation has ended: the cooldown spaces them, and releases on time.
func TestRepeatOfAnEndedInvestigationWaitsOutTheCooldown(t *testing.T) {
	harness := newInvestigationHarness(t)
	report := investigateReport("frs/dev", "UPGRADE FAILED: timed out")
	first, err := harness.app.InvestigateFailure(report, "frs", "dev", 80, 24)
	if err != nil {
		t.Fatalf("first report: %v", err)
	}
	if err := harness.app.StopOrchestrator(first.ID); err != nil {
		t.Fatalf("stop the first investigation: %v", err)
	}
	assertNoSessions(t, harness.app, "after the first investigation ended")

	harness.advance(investigationCooldown - time.Minute)
	_, err = harness.app.InvestigateFailure(report, "frs", "dev", 80, 24)
	if reason := refusalReason(t, err); reason != "cooldown" {
		t.Fatalf("expected a cooldown refusal inside the window, got %q (%v)", reason, err)
	}
	assertNoSessions(t, harness.app, "inside the cooldown")

	harness.advance(2 * time.Minute)
	if _, err := harness.app.InvestigateFailure(report, "frs", "dev", 80, 24); err != nil {
		t.Fatalf("the cooldown must release: %v", err)
	}
	if listed := harness.app.ListOrchestrators(); len(listed) != 1 {
		t.Fatalf("expected one investigation after the cooldown released, got %+v", listed)
	}
}

// Unrelated failures arriving at once must not outrun the cap. The events are
// concurrent because that is how they arrived: three agents were spawned from
// one event within 23 seconds.
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
			_, err := harness.app.InvestigateFailure(investigateReport("frs/"+environment, "UPGRADE FAILED: timed out"), "frs", environment, 80, 24)
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
	if listed := harness.app.ListOrchestrators(); len(listed) != maxLiveInvestigations {
		t.Fatalf("expected %d live sessions, got %+v", maxLiveInvestigations, listed)
	}
	// A refusal is not a permanent loss of capacity: ending one frees a slot.
	live := harness.app.ListOrchestrators()
	if err := harness.app.StopOrchestrator(live[0].ID); err != nil {
		t.Fatalf("stop one investigation: %v", err)
	}
	if _, err := harness.app.InvestigateFailure(investigateReport("frs/spare", "UPGRADE FAILED: timed out"), "frs", "spare", 80, 24); err != nil {
		t.Fatalf("a freed slot must admit the next event: %v", err)
	}
}

// The lifetime bound. An investigation still running when it elapses is stopped,
// the operator is told, and the lease it held on the environment is released —
// the runaways sat at 21 hours and nearly seven days with none of that happening.
func TestInvestigationPastItsLifetimeBoundIsTerminated(t *testing.T) {
	harness := newInvestigationHarness(t)
	harness.app.deps.startTerminal = func(startTerminalSessionParams) (terminalSession, error) {
		session := newStubTerminalSession()
		session.pid = os.Getpid()
		return session, nil
	}
	info, err := harness.app.InvestigateFailure(investigateHelmTimeoutReport, "frs", "dev", 80, 24)
	if err != nil {
		t.Fatalf("InvestigateFailure failed: %v", err)
	}
	if leases := activityLeaseIDs(t, "frs", "dev"); len(leases) != 1 {
		t.Fatalf("a running investigation must hold one activity lease, got %v", leases)
	}

	harness.advance(investigationLifetime)
	harness.reachLifetimeBound()

	assertNoSessions(t, harness.app, "after the lifetime bound")
	if leases := activityLeaseIDs(t, "frs", "dev"); len(leases) != 0 {
		t.Fatalf("a terminated investigation must not keep the environment busy, got %v", leases)
	}
	records := harness.app.investigations.list()
	if len(records) != 1 || records[0].State != investigationExpired {
		t.Fatalf("expected the investigation recorded as expired, got %+v", records)
	}
	if data, err := os.ReadFile(records[0].LogPath); err != nil || !strings.Contains(string(data), "terminated:") {
		t.Fatalf("the investigation log must say it was terminated (%v): %q", err, data)
	}
	if !emittedNotificationContains(harness.emits, "was stopped after") {
		t.Fatalf("the operator must be told the investigation was stopped, got %+v", harness.emits.events(appNotificationEvent))
	}
	// The slot the bound reclaimed is usable, which is the point of reclaiming it.
	if _, err := harness.app.InvestigateFailure(investigateReport("frs/other", "ImagePullBackOff"), "frs", "other", 80, 24); err != nil {
		t.Fatalf("the freed slot must admit a new investigation: %v", err)
	}
	_ = info
}

// The population is discoverable where an operator and an agent already look:
// as a job on the environment being investigated.
func TestRunningInvestigationIsVisibleAsAnEnvironmentJob(t *testing.T) {
	harness := newInvestigationHarness(t)
	harness.app.deps.startTerminal = func(startTerminalSessionParams) (terminalSession, error) {
		session := newStubTerminalSession()
		session.pid = os.Getpid()
		return session, nil
	}
	info, err := harness.app.InvestigateFailure(investigateHelmTimeoutReport, "frs", "dev", 80, 24)
	if err != nil {
		t.Fatalf("InvestigateFailure failed: %v", err)
	}
	jobs, err := eruncommon.LoadEnvironmentJobs("frs", "dev", time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJobs failed: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected the investigation to appear as one job, got %+v", jobs)
	}
	job := jobs[0]
	if job.State != eruncommon.EnvironmentJobStateRunning {
		t.Fatalf("expected a running job, got %+v", job)
	}
	if !strings.HasPrefix(job.ID, "investigate-") || job.LeaseID == "" {
		t.Fatalf("expected an investigation job holding a lease, got %+v", job)
	}
	if !strings.Contains(job.Name, "Investigate") {
		t.Fatalf("the job must name what it is, got %q", job.Name)
	}
	output, err := os.ReadFile(job.LogPath)
	if err != nil {
		t.Fatalf("the job's output must be readable: %v", err)
	}
	for _, want := range []string{info.ID, "lifetime bound", "report:"} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("the job output must carry %q, got %q", want, output)
		}
	}
}

// A signature identifies the failure, not the occurrence: a repeat differing
// only in version, timing, and log detail is the same failure, and a different
// error is a different one.
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

// The staged reports are evidence, but a directory that only grows is how a
// handful of real failures came to read as dozens.
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
	leases, err := eruncommon.LoadEnvironmentActivityLeases(tenant, environment, time.Now())
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
	for _, event := range emits.events(appNotificationEvent) {
		payload, ok := event.(appNotificationPayload)
		if !ok {
			continue
		}
		if strings.Contains(payload.Message, want) {
			return true
		}
	}
	return false
}
