package registrar

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/sophium/erun/erun-common/infra/mcptools"
	"github.com/sophium/erun/erun-mcp/tools/shared/callerid"
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

// guardToolTestOutput mimics the CommandOutput shape guardTool reflects on.
type guardToolTestOutput struct {
	Executed bool
}

func TestGuardToolRecordsMCPCallsAndAuditEvents(t *testing.T) {
	recorder := toolmetrics.NewMetricsRecorder("acme", "dev")
	admin := callerid.AuthIdentity{Tenant: "acme", Capabilities: mcptools.AdminMCPCapabilitySet()}
	readOnly := callerid.AuthIdentity{Tenant: "acme", Capabilities: mcptools.NewMCPCapabilitySet([]string{string(mcptools.MCPCapabilityRead)})}

	successHandler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, guardToolTestOutput, error) {
		return nil, guardToolTestOutput{Executed: true}, nil
	}
	dryRunHandler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, guardToolTestOutput, error) {
		return nil, guardToolTestOutput{Executed: false}, nil
	}
	errorHandler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, guardToolTestOutput, error) {
		return nil, guardToolTestOutput{}, errors.New("boom")
	}

	if _, _, err := GuardTool(admin, "deploy", recorder, successHandler)(context.Background(), nil, struct{}{}); err != nil {
		t.Fatalf("success call failed: %v", err)
	}
	if _, _, err := GuardTool(admin, "deploy", recorder, dryRunHandler)(context.Background(), nil, struct{}{}); err != nil {
		t.Fatalf("dry-run call failed: %v", err)
	}
	if _, _, err := GuardTool(admin, "deploy", recorder, errorHandler)(context.Background(), nil, struct{}{}); err == nil {
		t.Fatal("expected the error handler's error to propagate")
	}
	if _, _, err := GuardTool(readOnly, "deploy", recorder, successHandler)(context.Background(), nil, struct{}{}); err == nil {
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
	var recorder *toolmetrics.MetricsRecorder
	recorder.RecordMCPCall("idle", "success")
	recorder.RecordAuditEvent("mcp.idle", "agent", "success")
	recorder.SetIdleEligibility(true)
	recorder.SetTerminalInputSecondsSinceLast(1)
	recorder.SetTrafficWindowBytes(1)
	recorder.AddMCPBytes(1)

	admin := callerid.AuthIdentity{Tenant: "acme", Capabilities: mcptools.AdminMCPCapabilitySet()}
	handler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, guardToolTestOutput, error) {
		return nil, guardToolTestOutput{Executed: true}, nil
	}
	if _, _, err := GuardTool(admin, "version", nil, handler)(context.Background(), nil, struct{}{}); err != nil {
		t.Fatalf("guardTool with a nil recorder failed: %v", err)
	}
}
