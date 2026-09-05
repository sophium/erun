package erunmcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// The runtime pod's Prometheus exposition listener, documented at
// erun-docs/docs/agent-reference/metrics-spec.md. It is a separate listener
// from the MCP HTTP edge (DefaultHost/DefaultPort above) so a Prometheus
// scrape never shares a port, session, or auth requirement with a tool call.
const (
	DefaultMetricsHost = "0.0.0.0"
	DefaultMetricsPort = 9100
	DefaultMetricsPath = "/metrics"

	idleMetricsTickInterval   = 10 * time.Second
	trafficWindowTickInterval = 60 * time.Second
)

// MetricsConfig binds the metrics listener. Enabled defaults to false on a
// zero value deliberately (unlike HTTPConfig's port/path, which always have a
// usable default): the caller (cmd/emcp) is the one place that decides the
// env-var-driven default of "on", so a config built without going through it
// (e.g. a test) starts from the safe, inert state.
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

// metricsRecorder owns the five series metrics-spec.md documents plus the
// in-process counter the traffic-window gauge samples from. Every method has
// a nil-receiver no-op so callers (guardTool, the HTTP middleware, the
// tickers) never need a "is metrics enabled" branch of their own.
type metricsRecorder struct {
	registry *prometheus.Registry

	idleEligibility      prometheus.Gauge
	terminalInputSeconds prometheus.Gauge
	trafficWindowBytes   prometheus.Gauge
	mcpCallsTotal        *prometheus.CounterVec
	auditEventsTotal     *prometheus.CounterVec

	mcpBytesSeen atomic.Int64
}

func newMetricsRecorder(tenant, environment string) *metricsRecorder {
	labels := prometheus.Labels{"tenant": tenant, "environment": environment}
	r := &metricsRecorder{registry: prometheus.NewRegistry()}

	r.idleEligibility = prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        "erun_idle_eligibility",
		Help:        "Whether the environment is currently eligible for idle-stop: 1 eligible, 0 not.",
		ConstLabels: labels,
	})
	r.terminalInputSeconds = prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        "erun_terminal_input_seconds_since_last",
		Help:        "Seconds since the last SSH terminal input was observed; 0 if none has been observed yet.",
		ConstLabels: labels,
	})
	r.trafficWindowBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        "erun_traffic_window_bytes",
		Help:        "Bytes observed at the SSH and MCP sockets during the last completed sampling window.",
		ConstLabels: labels,
	})
	r.mcpCallsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name:        "erun_mcp_calls_total",
		Help:        "Total MCP tool calls handled by this pod's MCP edge, by tool and result.",
		ConstLabels: labels,
	}, []string{"tool", "result"})
	r.auditEventsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name:        "erun_audit_events_total",
		Help:        "Total audited actions recorded in this pod, by action, actor kind, and result.",
		ConstLabels: labels,
	}, []string{"action", "actor_kind", "result"})

	r.registry.MustRegister(r.idleEligibility, r.terminalInputSeconds, r.trafficWindowBytes, r.mcpCallsTotal, r.auditEventsTotal)
	return r
}

func (r *metricsRecorder) recordMCPCall(tool, result string) {
	if r == nil {
		return
	}
	r.mcpCallsTotal.WithLabelValues(tool, result).Inc()
}

func (r *metricsRecorder) recordAuditEvent(action, actorKind, result string) {
	if r == nil {
		return
	}
	r.auditEventsTotal.WithLabelValues(action, actorKind, result).Inc()
}

func (r *metricsRecorder) setIdleEligibility(eligible bool) {
	if r == nil {
		return
	}
	value := 0.0
	if eligible {
		value = 1
	}
	r.idleEligibility.Set(value)
}

func (r *metricsRecorder) setTerminalInputSecondsSinceLast(seconds float64) {
	if r == nil {
		return
	}
	r.terminalInputSeconds.Set(seconds)
}

func (r *metricsRecorder) setTrafficWindowBytes(bytes int64) {
	if r == nil {
		return
	}
	r.trafficWindowBytes.Set(float64(bytes))
}

func (r *metricsRecorder) addMCPBytes(n int64) {
	if r == nil || n <= 0 {
		return
	}
	r.mcpBytesSeen.Add(n)
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

// byteCountingResponseWriter measures the response half of an MCP HTTP call
// for erun_traffic_window_bytes. It always implements http.Flusher (a no-op
// when the underlying writer does not support it) so wrapping never breaks a
// streaming response the way a naive wrapper would.
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

// trafficMeteringMiddleware feeds metricsRecorder.mcpBytesSeen, the in-process
// half of the traffic-window gauge (the other half, SSH bytes, is sampled from
// the activity snapshot the ssh-proxy already maintains). An idle probe is
// excluded for the same reason activityHTTPMiddleware excludes it: it is
// synthetic polling, not traffic that should ever read as the env being busy.
func trafficMeteringMiddleware(recorder *metricsRecorder, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if recorder == nil || req.Header.Get(eruncommon.MCPIdleProbeHeader) == "true" {
			next.ServeHTTP(w, req)
			return
		}
		counting := &byteCountingResponseWriter{ResponseWriter: w}
		next.ServeHTTP(counting, req)
		requestBytes := req.ContentLength
		if requestBytes < 0 {
			requestBytes = 0
		}
		recorder.addMCPBytes(requestBytes + counting.bytes)
	})
}

// runMetricsHTTP serves the Prometheus exposition endpoint until ctx is
// cancelled, mirroring RunHTTP's own listen/shutdown shape. A disabled config
// (or a nil recorder, which normalizeRuntimeConfig never produces but a
// direct caller could) just waits for cancellation without binding a port —
// the tickers that feed the registry still run, so re-enabling later needs no
// restart of the rest of the process.
func runMetricsHTTP(ctx context.Context, cfg MetricsConfig, recorder *metricsRecorder) error {
	cfg, err := normalizeMetricsConfig(cfg)
	if err != nil {
		return err
	}
	if !cfg.Enabled || recorder == nil {
		<-ctx.Done()
		return nil
	}

	mux := http.NewServeMux()
	mux.Handle(DefaultMetricsPath, promhttp.HandlerFor(recorder.registry, promhttp.HandlerOpts{}))

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

// startIdleMetricsTicker feeds erun_idle_eligibility and
// erun_terminal_input_seconds_since_last from the same read-only idle-status
// resolution the `idle` MCP tool already exposes (eruncommon.
// ResolveStoredEnvironmentIdleStatus), so the gauges can never disagree with
// what an agent calling `idle` sees. It samples once immediately so a scrape
// right after boot does not read a zero-value gauge as "eligible"/"just typed".
func startIdleMetricsTicker(ctx context.Context, runtime RuntimeConfig, recorder *metricsRecorder) {
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

func sampleIdleMetrics(runtime RuntimeConfig, recorder *metricsRecorder) {
	tenant := strings.TrimSpace(runtime.Context.Tenant)
	environment := strings.TrimSpace(runtime.Context.Environment)
	if tenant == "" || environment == "" {
		return
	}
	now := time.Now()
	status, err := eruncommon.ResolveStoredEnvironmentIdleStatus(runtime.Store, tenant, environment, now)
	if err != nil {
		// Best-effort: leave the gauges at their last known value rather than
		// erroring the whole metrics loop over a transient resolution failure
		// (e.g. env config not yet written at pod boot).
		return
	}
	recorder.setIdleEligibility(status.StopEligible)
	recorder.setTerminalInputSecondsSinceLast(secondsSinceLastTerminalInput(status, now))
}

// terminalInputActivityKinds mirrors idle-policy.md's `last_terminal_input`
// definition: an SSH keystroke, a successful in-pod erun invocation, or any
// MCP tools/call that is not an idle probe. The MCP snapshot already excludes
// idle probes (activityHTTPMiddleware never records one), so taking the max
// LastActivity across these three kinds is the same value that page already
// documents, computed from the same on-disk activity snapshots the `idle`
// tool reads rather than a metrics-only redefinition.
var terminalInputActivityKinds = []string{eruncommon.ActivityKindSSH, eruncommon.ActivityKindCLI, eruncommon.ActivityKindMCP}

func secondsSinceLastTerminalInput(status eruncommon.EnvironmentIdleStatus, now time.Time) float64 {
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

// startTrafficWindowTicker feeds erun_traffic_window_bytes by sampling two
// monotonic counters every tick and reporting the delta since the previous
// sample: the ssh-proxy's cumulative activity-snapshot byte count (on disk,
// shared with the idle predicate) and this process's own in-memory MCP byte
// counter (fed by trafficMeteringMiddleware). Each reported value is the
// last *completed* window, not a window still accumulating — a gauge sampled
// mid-window would read low simply because a scrape landed early in it.
func startTrafficWindowTicker(ctx context.Context, runtime RuntimeConfig, recorder *metricsRecorder) {
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

func sampleTrafficWindow(runtime RuntimeConfig, recorder *metricsRecorder, lastSSHBytes, lastMCPBytes int64) (int64, int64) {
	tenant := strings.TrimSpace(runtime.Context.Tenant)
	environment := strings.TrimSpace(runtime.Context.Environment)
	sshBytes := lastSSHBytes
	if tenant != "" && environment != "" {
		if activity, err := eruncommon.LoadEnvironmentActivity(tenant, environment); err == nil {
			sshBytes = activity[eruncommon.ActivityKindSSH].Bytes
		}
	}
	mcpBytes := recorder.mcpBytesSeen.Load()

	windowBytes := deltaSinceLastSample(sshBytes, lastSSHBytes) + deltaSinceLastSample(mcpBytes, lastMCPBytes)
	recorder.setTrafficWindowBytes(windowBytes)
	return sshBytes, mcpBytes
}

// deltaSinceLastSample treats a counter that reads lower than the previous
// sample as having reset (process restart, fresh activity snapshot) rather
// than as negative traffic, so the whole current value counts as new.
func deltaSinceLastSample(current, previous int64) int64 {
	if current < previous {
		return current
	}
	return current - previous
}
