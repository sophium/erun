package toolmetrics

import (
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
)

// MetricsRecorder owns the metrics-spec.md series; every method is a nil-receiver no-op.
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

func NewMetricsRecorder(tenant, environment string) *MetricsRecorder {
	labels := prometheus.Labels{"tenant": tenant, "environment": environment}
	r := &MetricsRecorder{Registry: prometheus.NewRegistry()}

	r.IdleEligibility = prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        "erun_idle_eligibility",
		Help:        "Whether the environment is currently eligible for idle-stop: 1 eligible, 0 not.",
		ConstLabels: labels,
	})
	r.TerminalInputSeconds = prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        "erun_terminal_input_seconds_since_last",
		Help:        "Seconds since the last SSH terminal input was observed; 0 if none has been observed yet.",
		ConstLabels: labels,
	})
	r.TrafficWindowBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        "erun_traffic_window_bytes",
		Help:        "Bytes observed at the SSH and MCP sockets during the last completed sampling window.",
		ConstLabels: labels,
	})
	r.McpCallsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name:        "erun_mcp_calls_total",
		Help:        "Total MCP tool calls handled by this pod's MCP edge, by tool and result.",
		ConstLabels: labels,
	}, []string{"tool", "result"})
	r.AuditEventsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name:        "erun_audit_events_total",
		Help:        "Total audited actions recorded in this pod, by action, actor kind, and result.",
		ConstLabels: labels,
	}, []string{"action", "actor_kind", "result"})

	r.Registry.MustRegister(r.IdleEligibility, r.TerminalInputSeconds, r.TrafficWindowBytes, r.McpCallsTotal, r.AuditEventsTotal)
	return r
}
