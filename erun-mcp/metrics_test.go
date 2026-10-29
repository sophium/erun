package erunmcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/adrg/xdg"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/sophium/erun/erun-common/base/config"
	modelpkg "github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity/records"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolmetrics"
)

// scrapeMetrics parses the response as real Prometheus text exposition.
func scrapeMetrics(t *testing.T, recorder *toolmetrics.MetricsRecorder) map[string]*dto.MetricFamily {
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

func gaugeValue(t *testing.T, families map[string]*dto.MetricFamily, name string) float64 {
	t.Helper()
	family, ok := families[name]
	if !ok || len(family.Metric) == 0 {
		t.Fatalf("expected series %q, got families: %v", name, familyNames(families))
	}
	return family.Metric[0].GetGauge().GetValue()
}

func TestIdleEligibilityGaugeTracksUnderlyingActivityState(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	xdg.Reload()

	runtime := withStubRuntime(runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{Tenant: "acme", Environment: "dev"},
	})
	// Near-all-day working hours so the result does not depend on the wall clock.
	envConfig := config.EnvConfig{Name: "dev", ManagedCloud: true, Idle: config.EnvironmentIdleConfig{WorkingHours: "00:00-23:59"}}
	if err := runtime.Store.SaveEnvConfig("acme", envConfig); err != nil {
		t.Fatalf("SaveEnvConfig failed: %v", err)
	}
	recorder := toolmetrics.NewMetricsRecorder("acme", "dev")

	sampleIdleMetrics(runtime, recorder)
	families := scrapeMetrics(t, recorder)
	if got := gaugeValue(t, families, "erun_idle_eligibility"); got != 1 {
		t.Fatalf("erun_idle_eligibility = %v before any activity, want 1 (eligible)", got)
	}
	if got := gaugeValue(t, families, "erun_terminal_input_seconds_since_last"); got != 0 {
		t.Fatalf("erun_terminal_input_seconds_since_last = %v before any activity, want 0", got)
	}

	if err := records.RecordEnvironmentActivity(modelpkg.EnvironmentActivityParams{
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
	if err := records.RecordEnvironmentActivity(modelpkg.EnvironmentActivityParams{
		Tenant: "acme", Environment: "dev", Kind: config.ActivityKindSSH, Now: now.Add(-1 * time.Hour),
	}); err != nil {
		t.Fatalf("RecordEnvironmentActivity(ssh) failed: %v", err)
	}
	if err := records.RecordEnvironmentActivity(modelpkg.EnvironmentActivityParams{
		Tenant: "acme", Environment: "dev", Kind: config.ActivityKindCLI, Now: now.Add(-2 * time.Second),
	}); err != nil {
		t.Fatalf("RecordEnvironmentActivity(cli) failed: %v", err)
	}

	status := modelpkg.EnvironmentIdleStatus{}
	activity, err := records.LoadEnvironmentActivity("acme", "dev")
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

	runtime := withStubRuntime(runtimecfg.RuntimeConfig{
		Context: toolio.RuntimeContext{Tenant: "acme", Environment: "dev"},
	})
	recorder := toolmetrics.NewMetricsRecorder("acme", "dev")
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

func TestMetricsHTTPHonorsEnabledFlag(t *testing.T) {
	recorder := toolmetrics.NewMetricsRecorder("acme", "dev")
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
