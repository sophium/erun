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
	"github.com/sophium/erun/erun-common/base/config"
	modelpkg "github.com/sophium/erun/erun-common/base/model"
	activitypkg "github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/infra/mcptools"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// scrapeMetrics parses the response as real Prometheus text exposition.
func scrapeMetrics(t *testing.T, recorder *shared.MetricsRecorder) map[string]*dto.MetricFamily {
	t.Helper()
	server := httptest.NewServer(promhttp.HandlerFor(recorder.Registry, promhttp.HandlerOpts{}))
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

func TestMetricsEndpointServesAllFiveDocumentedSeries(t *testing.T) {
	recorder := newMetricsRecorder("acme", "dev")
	recorder.SetIdleEligibility(true)
	recorder.SetTerminalInputSecondsSinceLast(42)
	recorder.SetTrafficWindowBytes(1024)
	recorder.RecordMCPCall("idle", "success")
	recorder.RecordAuditEvent("mcp.idle", "agent", "success")

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

func TestIdleEligibilityGaugeTracksUnderlyingActivityState(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	xdg.Reload()

	runtime := normalizeRuntimeConfig(shared.RuntimeConfig{
		Context: shared.RuntimeContext{Tenant: "acme", Environment: "dev"},
	})
	// Near-all-day working hours so the result does not depend on the wall clock.
	envConfig := config.EnvConfig{Name: "dev", ManagedCloud: true, Idle: config.EnvironmentIdleConfig{WorkingHours: "00:00-23:59"}}
	if err := runtime.Store.SaveEnvConfig("acme", envConfig); err != nil {
		t.Fatalf("SaveEnvConfig failed: %v", err)
	}
	recorder := newMetricsRecorder("acme", "dev")

	sampleIdleMetrics(runtime, recorder)
	families := scrapeMetrics(t, recorder)
	if got := gaugeValue(t, families, "erun_idle_eligibility"); got != 1 {
		t.Fatalf("erun_idle_eligibility = %v before any activity, want 1 (eligible)", got)
	}
	if got := gaugeValue(t, families, "erun_terminal_input_seconds_since_last"); got != 0 {
		t.Fatalf("erun_terminal_input_seconds_since_last = %v before any activity, want 0", got)
	}

	if err := activitypkg.RecordEnvironmentActivity(modelpkg.EnvironmentActivityParams{
		Tenant: "acme", Environment: "dev", Kind: config.ActivityKindSSH,
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

func TestTerminalInputUnionsSSHCLIAndMCPActivity(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)

	now := time.Now()
	if err := activitypkg.RecordEnvironmentActivity(modelpkg.EnvironmentActivityParams{
		Tenant: "acme", Environment: "dev", Kind: config.ActivityKindSSH, Now: now.Add(-1 * time.Hour),
	}); err != nil {
		t.Fatalf("RecordEnvironmentActivity(ssh) failed: %v", err)
	}
	if err := activitypkg.RecordEnvironmentActivity(modelpkg.EnvironmentActivityParams{
		Tenant: "acme", Environment: "dev", Kind: config.ActivityKindCLI, Now: now.Add(-2 * time.Second),
	}); err != nil {
		t.Fatalf("RecordEnvironmentActivity(cli) failed: %v", err)
	}

	status := modelpkg.EnvironmentIdleStatus{}
	activity, err := activitypkg.LoadEnvironmentActivity("acme", "dev")
	if err != nil {
		t.Fatalf("LoadEnvironmentActivity failed: %v", err)
	}
	status.Activity = activity

	got := secondsSinceLastTerminalInput(status, now)
	if got < 1 || got > 10 {
		t.Fatalf("secondsSinceLastTerminalInput = %v, want roughly 2 seconds (the more recent CLI activity, not the older SSH activity)", got)
	}
}

func TestTrafficWindowTracksByteDeltasNotARunningTotal(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)

	runtime := normalizeRuntimeConfig(shared.RuntimeConfig{
		Context: shared.RuntimeContext{Tenant: "acme", Environment: "dev"},
	})
	recorder := newMetricsRecorder("acme", "dev")
	recorder.AddMCPBytes(100)

	lastSSH, lastMCP := sampleTrafficWindow(runtime, recorder, 0, 0)
	if got := gaugeValue(t, scrapeMetrics(t, recorder), "erun_traffic_window_bytes"); got != 100 {
		t.Fatalf("first window = %v, want 100", got)
	}

	lastSSH, lastMCP = sampleTrafficWindow(runtime, recorder, lastSSH, lastMCP)
	if got := gaugeValue(t, scrapeMetrics(t, recorder), "erun_traffic_window_bytes"); got != 0 {
		t.Fatalf("quiet window = %v, want 0", got)
	}

	recorder.AddMCPBytes(50)
	_, _ = sampleTrafficWindow(runtime, recorder, lastSSH, lastMCP)
	if got := gaugeValue(t, scrapeMetrics(t, recorder), "erun_traffic_window_bytes"); got != 50 {
		t.Fatalf("second active window = %v, want 50 (only the new bytes)", got)
	}
}

// guardToolTestOutput mimics the CommandOutput shape guardTool reflects on.
type guardToolTestOutput struct {
	Executed bool
}

func TestGuardToolRecordsMCPCallsAndAuditEvents(t *testing.T) {
	recorder := newMetricsRecorder("acme", "dev")
	admin := shared.AuthIdentity{Tenant: "acme", Capabilities: mcptools.AdminMCPCapabilitySet()}
	readOnly := shared.AuthIdentity{Tenant: "acme", Capabilities: mcptools.NewMCPCapabilitySet([]string{string(mcptools.MCPCapabilityRead)})}

	successHandler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, guardToolTestOutput, error) {
		return nil, guardToolTestOutput{Executed: true}, nil
	}
	dryRunHandler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, guardToolTestOutput, error) {
		return nil, guardToolTestOutput{Executed: false}, nil
	}
	errorHandler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, guardToolTestOutput, error) {
		return nil, guardToolTestOutput{}, errors.New("boom")
	}

	if _, _, err := shared.GuardTool(admin, "deploy", recorder, successHandler)(context.Background(), nil, struct{}{}); err != nil {
		t.Fatalf("success call failed: %v", err)
	}
	if _, _, err := shared.GuardTool(admin, "deploy", recorder, dryRunHandler)(context.Background(), nil, struct{}{}); err != nil {
		t.Fatalf("dry-run call failed: %v", err)
	}
	if _, _, err := shared.GuardTool(admin, "deploy", recorder, errorHandler)(context.Background(), nil, struct{}{}); err == nil {
		t.Fatal("expected the error handler's error to propagate")
	}
	if _, _, err := shared.GuardTool(readOnly, "deploy", recorder, successHandler)(context.Background(), nil, struct{}{}); err == nil {
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
	// A refused call is audited but not counted as an MCP call.
	if got := counterValue(t, families, "erun_audit_events_total", map[string]string{"action": "mcp.deploy", "actor_kind": "agent", "result": "error"}); got < 2 {
		t.Fatalf("erun_audit_events_total{action=mcp.deploy,actor_kind=agent,result=error} = %v, want at least 2 (the error call + the capability refusal)", got)
	}
	if got := counterValue(t, families, "erun_audit_events_total", map[string]string{"action": "mcp.deploy", "actor_kind": "agent", "result": "success"}); got != 1 {
		t.Fatalf("erun_audit_events_total{action=mcp.deploy,actor_kind=agent,result=success} = %v, want 1", got)
	}
}

func TestNilMetricsRecorderIsSafe(t *testing.T) {
	var recorder *shared.MetricsRecorder
	recorder.RecordMCPCall("idle", "success")
	recorder.RecordAuditEvent("mcp.idle", "agent", "success")
	recorder.SetIdleEligibility(true)
	recorder.SetTerminalInputSecondsSinceLast(1)
	recorder.SetTrafficWindowBytes(1)
	recorder.AddMCPBytes(1)

	admin := shared.AuthIdentity{Tenant: "acme", Capabilities: mcptools.AdminMCPCapabilitySet()}
	handler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, guardToolTestOutput, error) {
		return nil, guardToolTestOutput{Executed: true}, nil
	}
	if _, _, err := shared.GuardTool(admin, "version", nil, handler)(context.Background(), nil, struct{}{}); err != nil {
		t.Fatalf("guardTool with a nil recorder failed: %v", err)
	}
}

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
