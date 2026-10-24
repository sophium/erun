package shared

import (
	"reflect"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
)

// MetricsRecorder owns the five series metrics-spec.md documents plus the
// in-process counter the traffic-window gauge samples from. Every method has
// a nil-receiver no-op so callers (guardTool, the HTTP middleware, the
// tickers) never need a "is metrics enabled" branch of their own.
type MetricsRecorder struct {
	Registry *prometheus.Registry

	IdleEligibility      prometheus.Gauge
	TerminalInputSeconds prometheus.Gauge
	TrafficWindowBytes   prometheus.Gauge
	McpCallsTotal        *prometheus.CounterVec
	AuditEventsTotal     *prometheus.CounterVec

	McpBytesSeen atomic.Int64
}

func (r *MetricsRecorder) RecordMCPCall(tool, result string) {
	if r == nil {
		return
	}
	r.McpCallsTotal.WithLabelValues(tool, result).Inc()
}

func (r *MetricsRecorder) RecordAuditEvent(action, actorKind, result string) {
	if r == nil {
		return
	}
	r.AuditEventsTotal.WithLabelValues(action, actorKind, result).Inc()
}

func (r *MetricsRecorder) SetIdleEligibility(eligible bool) {
	if r == nil {
		return
	}
	value := 0.0
	if eligible {
		value = 1
	}
	r.IdleEligibility.Set(value)
}

func (r *MetricsRecorder) SetTerminalInputSecondsSinceLast(seconds float64) {
	if r == nil {
		return
	}
	r.TerminalInputSeconds.Set(seconds)
}

func (r *MetricsRecorder) SetTrafficWindowBytes(bytes int64) {
	if r == nil {
		return
	}
	r.TrafficWindowBytes.Set(float64(bytes))
}

func (r *MetricsRecorder) AddMCPBytes(n int64) {
	if r == nil || n <= 0 {
		return
	}
	r.McpBytesSeen.Add(n)
}

// mcpCallResultLabel derives the `result` label from a tool handler's own
// outcome. err is authoritative for "error". Absent an error, most delivery
// tools (build, push, deploy, doctor, pin, terraform, ...) return a
// CommandOutput-shaped struct whose Executed field is already the tool's own
// preview/dry-run signal (runRuntimeCommand sets it false under DryRun); a
// read-only tool with no such field (idle, list, version, usage) has no
// preview concept, so it is always "success" once err is nil.
func mcpCallResultLabel(out any, err error) string {
	if err != nil {
		return "error"
	}
	if executed, ok := commandOutputExecuted(out); ok && !executed {
		return "dry_run"
	}
	return "success"
}

func commandOutputExecuted(out any) (executed bool, ok bool) {
	value := reflect.ValueOf(out)
	if value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return false, false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return false, false
	}
	field := value.FieldByName("Executed")
	if !field.IsValid() || field.Kind() != reflect.Bool {
		return false, false
	}
	return field.Bool(), true
}
