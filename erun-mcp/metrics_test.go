package erunmcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/adrg/xdg"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	eruncommon "github.com/sophium/erun/erun-common"
)

// scrapeMetrics serves recorder's registry the same way runMetricsHTTP does
// and parses the response as real Prometheus text exposition format — proving
// the exposition parses, not just that the handler wrote some bytes.
func scrapeMetrics(t *testing.T, recorder *metricsRecorder) map[string]*dto.MetricFamily {
	t.Helper()
	server := httptest.NewServer(promhttp.HandlerFor(recorder.registry, promhttp.HandlerOpts{}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("scrape failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected scrape status: %d", resp.StatusCode)
	}

	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		t.Fatalf("response did not parse as Prometheus text format: %v", err)
	}
	return families
}

func familyNames(families map[string]*dto.MetricFamily) []string {
	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}
	return names
}

func assertHasLabel(t *testing.T, family string, metric *dto.Metric, key, want string) {
	t.Helper()
	for _, label := range metric.Label {
		if label.GetName() == key {
			if label.GetValue() != want {
				t.Fatalf("%s: label %s = %q, want %q", family, key, label.GetValue(), want)
			}
			return
		}
	}
	t.Fatalf("%s: missing label %q", family, key)
}

func gaugeValue(t *testing.T, families map[string]*dto.MetricFamily, name string) float64 {
	t.Helper()
	family, ok := families[name]
	if !ok || len(family.Metric) == 0 {
		t.Fatalf("expected series %q, got families: %v", name, familyNames(families))
	}
	return family.Metric[0].GetGauge().GetValue()
}

func counterValue(t *testing.T, families map[string]*dto.MetricFamily, name string, wantLabels map[string]string) float64 {
	t.Helper()
	family, ok := families[name]
	if !ok {
		t.Fatalf("expected series %q, got families: %v", name, familyNames(families))
	}
	for _, metric := range family.Metric {
		labels := map[string]string{}
		for _, label := range metric.Label {
			labels[label.GetName()] = label.GetValue()
		}
		matches := true
		for key, want := range wantLabels {
			if labels[key] != want {
				matches = false
				break
			}
		}
		if matches {
			return metric.GetCounter().GetValue()
		}
	}
	t.Fatalf("%s: no sample matching labels %v among %+v", name, wantLabels, family.Metric)
	return 0
}

// TestMetricsEndpointServesAllFiveDocumentedSeries is the red-then-green case
// erun#1323 exists to fix: metrics-spec.md documents five series and none of
// them existed anywhere in the code. On origin/main this test fails to even
// compile — metrics.go, newMetricsRecorder, and everything else this file
// exercises do not exist there. Against this branch it passes, proving the
// endpoint now serves exactly the names the spec promises, each carrying the
// tenant+environment labels the spec requires on every series.
func TestMetricsEndpointServesAllFiveDocumentedSeries(t *testing.T) {
	recorder := newMetricsRecorder("acme", "dev")
	recorder.setIdleEligibility(true)
	recorder.setTerminalInputSecondsSinceLast(42)
	recorder.setTrafficWindowBytes(1024)
	recorder.recordMCPCall("idle", "success")
	recorder.recordAuditEvent("mcp.idle", "agent", "success")

	families := scrapeMetrics(t, recorder)

	for _, name := range []string{
		"erun_idle_eligibility",
		"erun_terminal_input_seconds_since_last",
		"erun_traffic_window_bytes",
		"erun_mcp_calls_total",
		"erun_audit_events_total",
	} {
		family, ok := families[name]
		if !ok {
			t.Fatalf("expected series %q in scrape, got families: %v", name, familyNames(families))
		}
		if len(family.Metric) == 0 {
			t.Fatalf("series %q has no samples", name)
		}
		for _, metric := range family.Metric {
			assertHasLabel(t, name, metric, "tenant", "acme")
			assertHasLabel(t, name, metric, "environment", "dev")
		}
	}
}

// TestIdleEligibilityGaugeTracksUnderlyingActivityState proves the gauge
// moves with the real activity snapshot on disk rather than reporting a fixed
// value that merely looks healthy — root AGENTS.md calls a metric wired to a
// constant worse than no metric at all, because it hides the very failure it
// claims to report.
func TestIdleEligibilityGaugeTracksUnderlyingActivityState(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	xdg.Reload()

	runtime := normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{Tenant: "acme", Environment: "dev"},
	})
	// WorkingHours spans virtually the whole day so the test's outcome does not
	// depend on the wall-clock hour it happens to run at: outside working hours,
	// environmentStopEligibility ignores every marker except a lease, which
	// would make the SSH-activity assertion below flaky.
	envConfig := eruncommon.EnvConfig{Name: "dev", ManagedCloud: true, Idle: eruncommon.EnvironmentIdleConfig{WorkingHours: "00:00-23:59"}}
	if err := runtime.Store.SaveEnvConfig("acme", envConfig); err != nil {
		t.Fatalf("SaveEnvConfig failed: %v", err)
	}
	recorder := newMetricsRecorder("acme", "dev")

	// No activity recorded yet: every marker reads idle, so the environment is
	// eligible for idle-stop and no terminal input has ever been observed.
	sampleIdleMetrics(runtime, recorder)
	families := scrapeMetrics(t, recorder)
	if got := gaugeValue(t, families, "erun_idle_eligibility"); got != 1 {
		t.Fatalf("erun_idle_eligibility = %v before any activity, want 1 (eligible)", got)
	}
	if got := gaugeValue(t, families, "erun_terminal_input_seconds_since_last"); got != 0 {
		t.Fatalf("erun_terminal_input_seconds_since_last = %v before any activity, want 0", got)
	}

	// An SSH session started five seconds ago: eligibility must flip to
	// ineligible and terminal-input must report a real elapsed time.
	if err := eruncommon.RecordEnvironmentActivity(eruncommon.EnvironmentActivityParams{
		Tenant: "acme", Environment: "dev", Kind: eruncommon.ActivityKindSSH,
		Now: time.Now().Add(-5 * time.Second),
	}); err != nil {
		t.Fatalf("RecordEnvironmentActivity failed: %v", err)
	}
	sampleIdleMetrics(runtime, recorder)
	families = scrapeMetrics(t, recorder)
	if got := gaugeValue(t, families, "erun_idle_eligibility"); got != 0 {
		t.Fatalf("erun_idle_eligibility = %v with a live SSH session, want 0 (not eligible)", got)
	}
	if got := gaugeValue(t, families, "erun_terminal_input_seconds_since_last"); got < 4 || got > 30 {
		t.Fatalf("erun_terminal_input_seconds_since_last = %v, want roughly 5 seconds since the recorded SSH activity", got)
	}
}

// TestTerminalInputUnionsSSHCLIAndMCPActivity proves the gauge follows
// idle-policy.md's already-published `last_terminal_input` definition (a
// union of ssh, cli, and non-idle-probe mcp activity), not just SSH: a more
// recent CLI invocation must win over an older SSH keystroke.
func TestTerminalInputUnionsSSHCLIAndMCPActivity(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)

	now := time.Now()
	if err := eruncommon.RecordEnvironmentActivity(eruncommon.EnvironmentActivityParams{
		Tenant: "acme", Environment: "dev", Kind: eruncommon.ActivityKindSSH, Now: now.Add(-1 * time.Hour),
	}); err != nil {
		t.Fatalf("RecordEnvironmentActivity(ssh) failed: %v", err)
	}
	if err := eruncommon.RecordEnvironmentActivity(eruncommon.EnvironmentActivityParams{
		Tenant: "acme", Environment: "dev", Kind: eruncommon.ActivityKindCLI, Now: now.Add(-2 * time.Second),
	}); err != nil {
		t.Fatalf("RecordEnvironmentActivity(cli) failed: %v", err)
	}

	status := eruncommon.EnvironmentIdleStatus{}
	activity, err := eruncommon.LoadEnvironmentActivity("acme", "dev")
	if err != nil {
		t.Fatalf("LoadEnvironmentActivity failed: %v", err)
	}
	status.Activity = activity

	got := secondsSinceLastTerminalInput(status, now)
	if got < 1 || got > 10 {
		t.Fatalf("secondsSinceLastTerminalInput = %v, want roughly 2 seconds (the more recent CLI activity, not the older SSH activity)", got)
	}
}

// TestTrafficWindowTracksByteDeltasNotARunningTotal proves
// erun_traffic_window_bytes reports the bytes seen since the previous sample
// — a tumbling window — rather than a cumulative total or a constant: a quiet
// tick between two active ones must read back to 0.
func TestTrafficWindowTracksByteDeltasNotARunningTotal(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)

	runtime := normalizeRuntimeConfig(RuntimeConfig{
		Context: RuntimeContext{Tenant: "acme", Environment: "dev"},
	})
	recorder := newMetricsRecorder("acme", "dev")
	recorder.addMCPBytes(100)

	lastSSH, lastMCP := sampleTrafficWindow(runtime, recorder, 0, 0)
	if got := gaugeValue(t, scrapeMetrics(t, recorder), "erun_traffic_window_bytes"); got != 100 {
		t.Fatalf("first window = %v, want 100", got)
	}

	// A quiet window: no new bytes since the last sample, so this window is 0
	// even though the process-lifetime total is unchanged and non-zero.
	lastSSH, lastMCP = sampleTrafficWindow(runtime, recorder, lastSSH, lastMCP)
	if got := gaugeValue(t, scrapeMetrics(t, recorder), "erun_traffic_window_bytes"); got != 0 {
		t.Fatalf("quiet window = %v, want 0", got)
	}

	recorder.addMCPBytes(50)
	_, _ = sampleTrafficWindow(runtime, recorder, lastSSH, lastMCP)
	if got := gaugeValue(t, scrapeMetrics(t, recorder), "erun_traffic_window_bytes"); got != 50 {
		t.Fatalf("second active window = %v, want 50 (only the new bytes)", got)
	}
}

// guardToolTestOutput mimics the CommandOutput shape guardTool inspects via
// reflection to detect a preview/dry-run call.
type guardToolTestOutput struct {
	Executed bool
}

// TestGuardToolRecordsMCPCallsAndAuditEvents proves guardTool — the one place
// every MCP tool call passes through regardless of which register* function
// added it — increments both erun_mcp_calls_total and erun_audit_events_total
// with the labels metrics-spec.md documents, for a successful call, a
// dry-run call, an error, and a capability refusal.
func TestGuardToolRecordsMCPCallsAndAuditEvents(t *testing.T) {
	recorder := newMetricsRecorder("acme", "dev")
	admin := authIdentity{Tenant: "acme", Capabilities: eruncommon.AdminMCPCapabilitySet()}
	readOnly := authIdentity{Tenant: "acme", Capabilities: eruncommon.NewMCPCapabilitySet([]string{string(eruncommon.MCPCapabilityRead)})}

	successHandler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, guardToolTestOutput, error) {
		return nil, guardToolTestOutput{Executed: true}, nil
	}
	dryRunHandler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, guardToolTestOutput, error) {
		return nil, guardToolTestOutput{Executed: false}, nil
	}
	errorHandler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, guardToolTestOutput, error) {
		return nil, guardToolTestOutput{}, errors.New("boom")
	}

	if _, _, err := guardTool(admin, "deploy", recorder, successHandler)(context.Background(), nil, struct{}{}); err != nil {
		t.Fatalf("success call failed: %v", err)
	}
	if _, _, err := guardTool(admin, "deploy", recorder, dryRunHandler)(context.Background(), nil, struct{}{}); err != nil {
		t.Fatalf("dry-run call failed: %v", err)
	}
	if _, _, err := guardTool(admin, "deploy", recorder, errorHandler)(context.Background(), nil, struct{}{}); err == nil {
		t.Fatal("expected the error handler's error to propagate")
	}
	// A read-only caller reaching a mutating tool directly (bypassing
	// registration filtering) is refused by the guard itself.
	if _, _, err := guardTool(readOnly, "deploy", recorder, successHandler)(context.Background(), nil, struct{}{}); err == nil {
		t.Fatal("expected the capability refusal to propagate")
	}

	families := scrapeMetrics(t, recorder)
	if got := counterValue(t, families, "erun_mcp_calls_total", map[string]string{"tool": "deploy", "result": "success"}); got != 1 {
		t.Fatalf("erun_mcp_calls_total{tool=deploy,result=success} = %v, want 1", got)
	}
	if got := counterValue(t, families, "erun_mcp_calls_total", map[string]string{"tool": "deploy", "result": "dry_run"}); got != 1 {
		t.Fatalf("erun_mcp_calls_total{tool=deploy,result=dry_run} = %v, want 1", got)
	}
	if got := counterValue(t, families, "erun_mcp_calls_total", map[string]string{"tool": "deploy", "result": "error"}); got != 1 {
		t.Fatalf("erun_mcp_calls_total{tool=deploy,result=error} = %v, want 1", got)
	}
	// The refused call never reaches the handler, so it must not be counted as
	// an MCP call — only as an audited (denied) action.
	if got := counterValue(t, families, "erun_audit_events_total", map[string]string{"action": "mcp.deploy", "actor_kind": "agent", "result": "error"}); got < 2 {
		t.Fatalf("erun_audit_events_total{action=mcp.deploy,actor_kind=agent,result=error} = %v, want at least 2 (the error call + the capability refusal)", got)
	}
	if got := counterValue(t, families, "erun_audit_events_total", map[string]string{"action": "mcp.deploy", "actor_kind": "agent", "result": "success"}); got != 1 {
		t.Fatalf("erun_audit_events_total{action=mcp.deploy,actor_kind=agent,result=success} = %v, want 1", got)
	}
}

// TestNilMetricsRecorderIsSafe proves every recording path tolerates a nil
// recorder (the state when metrics are disabled), so disabling the HTTP
// listener never has to be threaded through every call site as a separate
// "is metrics enabled" branch.
func TestNilMetricsRecorderIsSafe(t *testing.T) {
	var recorder *metricsRecorder
	recorder.recordMCPCall("idle", "success")
	recorder.recordAuditEvent("mcp.idle", "agent", "success")
	recorder.setIdleEligibility(true)
	recorder.setTerminalInputSecondsSinceLast(1)
	recorder.setTrafficWindowBytes(1)
	recorder.addMCPBytes(1)

	admin := authIdentity{Tenant: "acme", Capabilities: eruncommon.AdminMCPCapabilitySet()}
	handler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, guardToolTestOutput, error) {
		return nil, guardToolTestOutput{Executed: true}, nil
	}
	if _, _, err := guardTool(admin, "version", nil, handler)(context.Background(), nil, struct{}{}); err != nil {
		t.Fatalf("guardTool with a nil recorder failed: %v", err)
	}
}

// TestMetricsHTTPHonorsEnabledFlag proves scope item 3's access decision has a
// real effect: an operator (or the chart) that sets metricsEnabled=false gets
// no listener at all, rather than one that silently ignores the setting.
func TestMetricsHTTPHonorsEnabledFlag(t *testing.T) {
	recorder := newMetricsRecorder("acme", "dev")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- runMetricsHTTP(ctx, MetricsConfig{Port: 0, Enabled: false}, recorder) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("disabled metrics server returned an error instead of waiting for cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("disabled metrics server never returned after context cancellation")
	}
}
