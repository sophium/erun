package erunmcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sophium/erun/erun-common/base/config"
	activitypkg "github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// The Prometheus listener is separate from the MCP edge so a scrape never
// shares its port or auth.
const (
	DefaultMetricsHost = "0.0.0.0"
	DefaultMetricsPort = 9100
	DefaultMetricsPath = "/metrics"

	idleMetricsTickInterval   = 10 * time.Second
	trafficWindowTickInterval = 60 * time.Second
)

// MetricsConfig binds the metrics listener; the zero value is disabled.
type MetricsConfig struct {
	Host    string
	Port    int
	Enabled bool
}

func normalizeMetricsConfig(cfg MetricsConfig) (MetricsConfig, error) {
	if cfg.Host == "" {
		cfg.Host = DefaultMetricsHost
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultMetricsPort
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return MetricsConfig{}, fmt.Errorf("invalid metrics HTTP port %d", cfg.Port)
	}
	return cfg, nil
}

func newMetricsRecorder(tenant, environment string) *shared.MetricsRecorder {
	labels := prometheus.Labels{"tenant": tenant, "environment": environment}
	r := &shared.MetricsRecorder{Registry: prometheus.NewRegistry()}

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

// byteCountingResponseWriter always implements http.Flusher so wrapping never
// breaks a streaming response.
type byteCountingResponseWriter struct {
	http.ResponseWriter
	bytes int64
}

func (w *byteCountingResponseWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *byteCountingResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// trafficMeteringMiddleware counts MCP bytes, excluding idle probes.
func trafficMeteringMiddleware(recorder *shared.MetricsRecorder, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if recorder == nil || req.Header.Get(mcp.MCPIdleProbeHeader) == "true" {
			next.ServeHTTP(w, req)
			return
		}
		counting := &byteCountingResponseWriter{ResponseWriter: w}
		next.ServeHTTP(counting, req)
		requestBytes := req.ContentLength
		if requestBytes < 0 {
			requestBytes = 0
		}
		recorder.AddMCPBytes(requestBytes + counting.bytes)
	})
}

// runMetricsHTTP serves the metrics endpoint until ctx is cancelled; when
// disabled it only waits.
func runMetricsHTTP(ctx context.Context, cfg MetricsConfig, recorder *shared.MetricsRecorder) error {
	cfg, err := normalizeMetricsConfig(cfg)
	if err != nil {
		return err
	}
	if !cfg.Enabled || recorder == nil {
		<-ctx.Done()
		return nil
	}

	mux := http.NewServeMux()
	mux.Handle(DefaultMetricsPath, promhttp.HandlerFor(recorder.Registry, promhttp.HandlerOpts{}))

	server := &http.Server{
		Addr:              net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	shutdownErr := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr <- server.Shutdown(shutdownCtx)
	}()

	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return <-shutdownErr
	}
	return err
}

// startIdleMetricsTicker samples the same idle status the `idle` tool exposes,
// once immediately so an early scrape is not read as zero.
func startIdleMetricsTicker(ctx context.Context, runtime shared.RuntimeConfig, recorder *shared.MetricsRecorder) {
	if recorder == nil {
		return
	}
	sampleIdleMetrics(runtime, recorder)
	ticker := time.NewTicker(idleMetricsTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sampleIdleMetrics(runtime, recorder)
		}
	}
}

func sampleIdleMetrics(runtime shared.RuntimeConfig, recorder *shared.MetricsRecorder) {
	tenant := strings.TrimSpace(runtime.Context.Tenant)
	environment := strings.TrimSpace(runtime.Context.Environment)
	if tenant == "" || environment == "" {
		return
	}
	now := time.Now()
	status, err := activitypkg.ResolveStoredEnvironmentIdleStatus(runtime.Store, tenant, environment, now)
	if err != nil {
		// Keep the last known gauge values on a transient failure.
		return
	}
	recorder.SetIdleEligibility(status.StopEligible)
	recorder.SetTerminalInputSecondsSinceLast(secondsSinceLastTerminalInput(status, now))
}

// terminalInputActivityKinds are the kinds counted as last_terminal_input (idle-policy.md).
var terminalInputActivityKinds = []string{config.ActivityKindSSH, config.ActivityKindCLI, config.ActivityKindMCP}

func secondsSinceLastTerminalInput(status activitypkg.EnvironmentIdleStatus, now time.Time) float64 {
	var last time.Time
	for _, kind := range terminalInputActivityKinds {
		snapshot, ok := status.Activity[kind]
		if !ok || snapshot.LastActivity.IsZero() {
			continue
		}
		if snapshot.LastActivity.After(last) {
			last = snapshot.LastActivity
		}
	}
	if last.IsZero() {
		return 0
	}
	seconds := now.Sub(last).Seconds()
	if seconds < 0 {
		return 0
	}
	return seconds
}

// startTrafficWindowTicker reports SSH plus MCP bytes over the last completed window.
func startTrafficWindowTicker(ctx context.Context, runtime shared.RuntimeConfig, recorder *shared.MetricsRecorder) {
	if recorder == nil {
		return
	}
	var lastSSHBytes, lastMCPBytes int64
	ticker := time.NewTicker(trafficWindowTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			lastSSHBytes, lastMCPBytes = sampleTrafficWindow(runtime, recorder, lastSSHBytes, lastMCPBytes)
		}
	}
}

func sampleTrafficWindow(runtime shared.RuntimeConfig, recorder *shared.MetricsRecorder, lastSSHBytes, lastMCPBytes int64) (int64, int64) {
	tenant := strings.TrimSpace(runtime.Context.Tenant)
	environment := strings.TrimSpace(runtime.Context.Environment)
	sshBytes := lastSSHBytes
	if tenant != "" && environment != "" {
		if activity, err := activitypkg.LoadEnvironmentActivity(tenant, environment); err == nil {
			sshBytes = activity[config.ActivityKindSSH].Bytes
		}
	}
	mcpBytes := recorder.McpBytesSeen.Load()

	windowBytes := deltaSinceLastSample(sshBytes, lastSSHBytes) + deltaSinceLastSample(mcpBytes, lastMCPBytes)
	recorder.SetTrafficWindowBytes(windowBytes)
	return sshBytes, mcpBytes
}

// deltaSinceLastSample treats a counter that went backwards as reset.
func deltaSinceLastSample(current, previous int64) int64 {
	if current < previous {
		return current
	}
	return current - previous
}
