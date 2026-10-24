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
