package runtimeusage

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config/runtimeresources"
	"github.com/sophium/erun/erun-common/base/model"
)

// Under-provisioning kills a running agent while over-provisioning only wastes
// capacity, so sizing raises on modest evidence and shrinks only after a long quiet
// window, never below a healthy multiple of the observed peak.

const (
	// Deliberately the memory warning threshold: since peak >= current, any reading
	// that warns also crosses the raise margin.
	runtimeSizingRaiseMemoryPercent = RuntimeUsageMemoryWarnPercent

	// RuntimeSizingMemoryHeadroom is the multiple of observed peak that memory targets
	// and no shrink crosses. 2.0 left real over-provisioned envs unshrinkable; the band
	// between 1/1.5 and the raise threshold is hold, so verdicts cannot oscillate.
	RuntimeSizingMemoryHeadroom = 1.5

	// Larger than memory's: the CPU figure is an interval average that flattens bursts.
	runtimeSizingCPUHeadroom = 2.0

	// Throttling censors true demand, so the raise is a half-step.
	runtimeSizingCPURaiseMultiple = 1.5

	// RuntimeSizingThrottleRatio is the package's one bar for a meaningful throttle
	// ratio (an ordinary week measured 0.14%). Sizing and the build-starvation warning
	// share it but use different period floors.
	RuntimeSizingThrottleRatio = 0.05

	// A day covers the nightly build and heavy agent runs that set the real peak.
	runtimeSizingShrinkWindow = 24 * time.Hour

	// A day-long span with few samples watched almost nothing.
	runtimeSizingShrinkSamples = 120

	// A young container's ratio over a few hundred periods swings wildly.
	runtimeSizingThrottlePeriods = 10000

	// Rounding up avoids false precision and keeps the headroom guarantee.
	runtimeSizingMemoryGraduationMi = 256
)

// runtimeThrottleIsMaterial is sizing's bar for a throttle ratio. A bare
// nr_throttled > 0 is lifetime residue, not current starvation.
func runtimeThrottleIsMaterial(throttled, periods int64) bool {
	return periods >= runtimeSizingThrottlePeriods && float64(throttled) >= float64(periods)*RuntimeSizingThrottleRatio
}

// RuntimeSizingParams is the recommender's whole input, keeping it a pure function.
type RuntimeSizingParams struct {
	History RuntimeUsageHistory
	// Ceiling bounds a raise to what the namespace ResourceQuota can admit; zero means unknown.
	Ceiling model.NamespaceResourceQuota
	// Live is merged into the history before one verdict is computed, so a warning and
	// its recommendation share evidence. Nil when only retained history exists.
	Live *model.RuntimeUsage
}

// RecommendRuntimeSizing derives the recommendation from retained history, false
// when nothing was ever observed. It scores the cgroup limit, not the configured
// runtimepod, which in-pod config does not carry.
func RecommendRuntimeSizing(params RuntimeSizingParams) (model.RuntimeSizingRecommendation, bool) {
	history := params.History
	// A live reading's limit also supersedes a stale retained one after a resize.
	latest, ok := runtimeSizingLatestReading(history, params.Live)
	if !ok {
		return model.RuntimeSizingRecommendation{}, false
	}
	peak := history.ObservedPeakMemoryBytes
	oomKills := history.ObservedOOMKills
	periods := history.ObservedPeriods
	throttled := history.ObservedThrottledPeriods
	if params.Live != nil {
		// Union, not replacement: every figure is monotonic in evidence, so take the larger.
		peak = max(peak, runtimeLivePeakMemoryBytes(*params.Live))
		if params.Live.Memory.OOMKillsObserved {
			oomKills = max(oomKills, params.Live.Memory.OOMKills)
		}
		periods = max(periods, params.Live.CPU.Periods)
		throttled = max(throttled, params.Live.CPU.ThrottledPeriods)
	}
	samples := len(history.Samples)
	if samples == 0 && params.Live != nil {
		samples = 1
	}

	evidence := model.RuntimeSizingEvidence{
		ObservedSeconds:          int64(history.ObservedWindow() / time.Second),
		Samples:                  samples,
		Restarts:                 history.Restarts,
		MemoryLimitBytes:         latest.Memory.LimitBytes,
		ObservedPeakMemoryBytes:  peak,
		ObservedOOMKills:         oomKills,
		CPUQuotaMilli:            runtimeresources.RuntimeQuotaMilli(latest.CPU.QuotaCores),
		ObservedPeakCPUMilli:     history.ObservedPeakCPUMilli,
		ObservedPeriods:          periods,
		ObservedThrottledPeriods: throttled,
		Signals:                  []string{"cgroup memory.peak", "cgroup memory.events oom_kill", "cgroup cpu.stat usage_usec/nr_throttled"},
		Unavailable:              runtimeUsageUnavailable(latest),
	}

	return model.RuntimeSizingRecommendation{
		Knob: "runtimepod",
		Verdicts: []model.RuntimeSizingVerdict{
			recommendRuntimeMemory(history, latest, peak, oomKills, params.Ceiling),
			recommendRuntimeCPU(history, latest, periods, throttled),
		},
		Evidence: evidence,
	}, true
}

func runtimeSizingLatestReading(history RuntimeUsageHistory, live *model.RuntimeUsage) (model.RuntimeUsage, bool) {
	if live != nil && live.HasCounters() {
		return *live, true
	}
	return history.Latest()
}

// Current usage is a legitimate peak when memory.peak is absent; the warnings read it too.
func runtimeLivePeakMemoryBytes(usage model.RuntimeUsage) int64 {
	return max(usage.Memory.PeakBytes, usage.Memory.CurrentBytes)
}

// RuntimeMemoryRaiseVerdict is the single place a memory raise is decided, for both history and live readings.
func RuntimeMemoryRaiseVerdict(limit, peak, oomKills int64, ceiling model.NamespaceResourceQuota) (model.RuntimeSizingVerdict, bool) {
	verdict := model.RuntimeSizingVerdict{Resource: "memory", Current: runtimeresources.FormatBytesAsMi(limit)}
	if oomKills > 0 {
		// After a kill the refused allocation never reached memory.peak, so size from the limit.
		suggested, bounded := boundRuntimeMemorySuggestion(scaleBytesToMi(limit, RuntimeSizingMemoryHeadroom), ceiling)
		verdict.Action = model.RuntimeSizingRaise
		verdict.Confidence = model.RuntimeSizingConfidenceHigh
		verdict.Suggested = suggested
		verdict.Reason = fmt.Sprintf("%d oom kill(s) at %s%s", oomKills, runtimeresources.FormatBytesAsMi(limit), bounded)
		return verdict, true
	}
	if float64(peak) < float64(limit)*runtimeSizingRaiseMemoryPercent/100 {
		return model.RuntimeSizingVerdict{}, false
	}
	suggested, bounded := boundRuntimeMemorySuggestion(scaleBytesToMi(peak, RuntimeSizingMemoryHeadroom), ceiling)
	verdict.Action = model.RuntimeSizingRaise
	verdict.Confidence = model.RuntimeSizingConfidenceHigh
	verdict.Suggested = suggested
	verdict.Reason = fmt.Sprintf("peak %s of %s (%s) is within the raise margin%s", runtimeresources.FormatBytesAsMi(peak), runtimeresources.FormatBytesAsMi(limit), formatPercent(peak, limit), bounded)
	return verdict, true
}

func recommendRuntimeMemory(history RuntimeUsageHistory, latest model.RuntimeUsage, peak, oomKills int64, ceiling model.NamespaceResourceQuota) model.RuntimeSizingVerdict {
	verdict := model.RuntimeSizingVerdict{Resource: "memory"}
	limit := latest.Memory.LimitBytes
	if limit <= 0 {
		verdict.Action = model.RuntimeSizingUnknown
		verdict.Reason = "memory.max reports no limit, so there is nothing to size against"
		return verdict
	}
	verdict.Current = runtimeresources.FormatBytesAsMi(limit)

	if raise, ok := RuntimeMemoryRaiseVerdict(limit, peak, oomKills, ceiling); ok {
		return raise
	}

	if reason, ok := runtimeSizingShrinkWindowShortfall(history); !ok {
		verdict.Action = model.RuntimeSizingUnknown
		verdict.Reason = fmt.Sprintf("peak %s of %s (%s), but %s", runtimeresources.FormatBytesAsMi(peak), runtimeresources.FormatBytesAsMi(limit), formatPercent(peak, limit), reason)
		return verdict
	}

	suggested := scaleBytesToMi(peak, RuntimeSizingMemoryHeadroom)
	if suggested >= limit {
		verdict.Action = model.RuntimeSizingHold
		verdict.Reason = fmt.Sprintf("peak %s of %s (%s) leaves no room to shrink at %gx headroom", runtimeresources.FormatBytesAsMi(peak), runtimeresources.FormatBytesAsMi(limit), formatPercent(peak, limit), RuntimeSizingMemoryHeadroom)
		return verdict
	}
	verdict.Action = model.RuntimeSizingLower
	// A quiet window is an argument from silence.
	verdict.Confidence = model.RuntimeSizingConfidenceLow
	verdict.Suggested = runtimeresources.FormatBytesAsMi(suggested)
	verdict.Reason = fmt.Sprintf("peak %s of %s (%s), no oom kills, keeping %gx headroom", runtimeresources.FormatBytesAsMi(peak), runtimeresources.FormatBytesAsMi(limit), formatPercent(peak, limit), RuntimeSizingMemoryHeadroom)
	return verdict
}

func recommendRuntimeCPU(history RuntimeUsageHistory, latest model.RuntimeUsage, observedPeriods, observedThrottled int64) model.RuntimeSizingVerdict {
	verdict := model.RuntimeSizingVerdict{Resource: "cpu"}
	quota := runtimeresources.RuntimeQuotaMilli(latest.CPU.QuotaCores)
	if quota <= 0 {
		verdict.Action = model.RuntimeSizingUnknown
		verdict.Reason = "cpu.max reports no quota, so there is nothing to size against"
		return verdict
	}
	verdict.Current = runtimeresources.FormatKubernetesCPUFromMilli(quota)

	if runtimeThrottleIsMaterial(observedThrottled, observedPeriods) {
		verdict.Action = model.RuntimeSizingRaise
		verdict.Confidence = model.RuntimeSizingConfidenceHigh
		verdict.Suggested = runtimeresources.FormatKubernetesCPUFromMilli(runtimeresources.ScaleMilliToWholeCores(quota, runtimeSizingCPURaiseMultiple))
		verdict.Reason = runtimeThrottleLabel(observedThrottled, observedPeriods)
		return verdict
	}

	throttleLabel := runtimeThrottleLabel(observedThrottled, observedPeriods)
	if reason, ok := runtimeSizingShrinkWindowShortfall(history); !ok {
		verdict.Action = model.RuntimeSizingUnknown
		verdict.Reason = fmt.Sprintf("%s, but %s", throttleLabel, reason)
		return verdict
	}
	if observedPeriods < runtimeSizingThrottlePeriods {
		verdict.Action = model.RuntimeSizingUnknown
		verdict.Reason = fmt.Sprintf("only %d scheduling periods observed, too few to read a throttle ratio from", observedPeriods)
		return verdict
	}
	if observedThrottled > 0 {
		// Below the threshold the quota still binds sometimes: neither grow nor shrink.
		verdict.Action = model.RuntimeSizingHold
		verdict.Reason = throttleLabel + ", tolerable but not unused"
		return verdict
	}
	if history.ObservedPeakCPUMilli <= 0 {
		verdict.Action = model.RuntimeSizingUnknown
		verdict.Reason = "no cpu rate observed between samples"
		return verdict
	}

	suggested := runtimeresources.ScaleMilliToWholeCores(history.ObservedPeakCPUMilli, runtimeSizingCPUHeadroom)
	peakLabel := fmt.Sprintf("busiest interval %s of %s", runtimeresources.FormatKubernetesCPUFromMilli(history.ObservedPeakCPUMilli), runtimeresources.FormatKubernetesCPUFromMilli(quota))
	if suggested >= quota {
		verdict.Action = model.RuntimeSizingHold
		verdict.Reason = fmt.Sprintf("%s, %s", peakLabel, throttleLabel)
		return verdict
	}
	verdict.Action = model.RuntimeSizingLower
	verdict.Confidence = model.RuntimeSizingConfidenceLow
	verdict.Suggested = runtimeresources.FormatKubernetesCPUFromMilli(suggested)
	verdict.Reason = fmt.Sprintf("%s, %s, keeping %gx headroom", peakLabel, throttleLabel, runtimeSizingCPUHeadroom)
	return verdict
}

func runtimeSizingShrinkWindowShortfall(history RuntimeUsageHistory) (string, bool) {
	if window := history.ObservedWindow(); window < runtimeSizingShrinkWindow {
		return fmt.Sprintf("only %s observed of the %s a shrink needs", FormatObservedWindow(window), FormatObservedWindow(runtimeSizingShrinkWindow)), false
	}
	if len(history.Samples) < runtimeSizingShrinkSamples {
		return fmt.Sprintf("only %d samples retained of the %d a shrink needs", len(history.Samples), runtimeSizingShrinkSamples), false
	}
	return "", true
}

// boundRuntimeMemorySuggestion clamps a raise to what the quota admits after the
// dind sidecar's limit, noting when it bit.
func boundRuntimeMemorySuggestion(suggested int64, ceiling model.NamespaceResourceQuota) (string, string) {
	quotaMi, err := runtimeresources.ParseKubernetesMemoryToMi(ceiling.Memory)
	if err != nil {
		return runtimeresources.FormatBytesAsMi(suggested), ""
	}
	dindMi, err := runtimeresources.ParseKubernetesMemoryToMi(runtimeresources.DefaultRuntimeDindMemory)
	if err != nil {
		return runtimeresources.FormatBytesAsMi(suggested), ""
	}
	const mi = 1024 * 1024
	available := (quotaMi - dindMi) * mi
	if available <= 0 || suggested <= available {
		return runtimeresources.FormatBytesAsMi(suggested), ""
	}
	graduation := int64(runtimeSizingMemoryGraduationMi) * mi
	note := fmt.Sprintf("; bounded by the %s namespace quota less the dind sidecar's %s", ceiling.Memory, runtimeresources.DefaultRuntimeDindMemory)
	return runtimeresources.FormatBytesAsMi(available - available%graduation), note
}

// Rounding up keeps the headroom guarantee.
func scaleBytesToMi(bytes int64, multiple float64) int64 {
	const mi = 1024 * 1024
	graduation := int64(runtimeSizingMemoryGraduationMi) * mi
	scaled := int64(math.Ceil(float64(bytes) * multiple))
	steps := (scaled + graduation - 1) / graduation
	if steps < 1 {
		steps = 1
	}
	return steps * graduation
}

// One function so raise and hold cite the ratio identically.
func runtimeThrottleLabel(throttled, periods int64) string {
	return fmt.Sprintf("%s of scheduling periods throttled (%d of %d)", formatThrottleRatio(throttled, periods), throttled, periods)
}

// Two decimals: meaningful ratios are fractions of a percent that "0%" would hide.
func formatThrottleRatio(throttled, periods int64) string {
	if periods <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.2f%%", float64(throttled)/float64(periods)*100)
}

func formatPercent(part, whole int64) string {
	if whole <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%%", float64(part)/float64(whole)*100)
}

func runtimeUsageUnavailable(latest model.RuntimeUsage) []string {
	var unavailable []string
	if latest.Memory.Unavailable != "" {
		unavailable = append(unavailable, latest.Memory.Unavailable)
	}
	if latest.CPU.Unavailable != "" {
		unavailable = append(unavailable, latest.CPU.Unavailable)
	}
	return unavailable
}

// FormatRuntimeSizingVerdict renders a verdict identically for every consumer.
func FormatRuntimeSizingVerdict(verdict model.RuntimeSizingVerdict) string {
	label := verdict.Resource + " " + string(verdict.Action)
	if suggested := strings.TrimSpace(verdict.Suggested); suggested != "" {
		label += " to " + suggested
	}
	if current := strings.TrimSpace(verdict.Current); current != "" {
		label += " from " + current
	}
	label += " (" + verdict.Reason
	if verdict.Confidence != "" {
		label += ", " + string(verdict.Confidence) + " confidence"
	}
	return label + ")"
}

// FormatRuntimeSizingEvidence renders the inputs a verdict was computed from.
func FormatRuntimeSizingEvidence(sizing model.RuntimeSizingRecommendation) string {
	evidence := sizing.Evidence
	return fmt.Sprintf("%s observed, %d samples, %d restarts, knob=%s, from %s (not loadavg)",
		FormatObservedWindow(time.Duration(evidence.ObservedSeconds)*time.Second),
		evidence.Samples, evidence.Restarts, sizing.Knob, strings.Join(evidence.Signals, ", "))
}

// FormatObservedWindow uses whole minutes so readings moments apart stay stable.
func FormatObservedWindow(window time.Duration) string {
	if window < 0 {
		window = 0
	}
	minutes := int64(window / time.Minute)
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh%dm", minutes/60, minutes%60)
}
