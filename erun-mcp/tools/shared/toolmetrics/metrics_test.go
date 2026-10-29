package toolmetrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// scrapeMetrics parses the response as real Prometheus text exposition.
func scrapeMetrics(t *testing.T, recorder *MetricsRecorder) map[string]*dto.MetricFamily {
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

func TestMetricsEndpointServesAllFiveDocumentedSeries(t *testing.T) {
	recorder := NewMetricsRecorder("acme", "dev")
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
