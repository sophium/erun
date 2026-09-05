package eruncommon

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Sizing has always been a guess made once. A recommendation turns it into an
// answer the environment produced itself — but the two ways of being wrong cost
// very different amounts, and that asymmetry is the design rather than a detail
// of it. An over-provisioned environment quietly consumes cluster capacity; an
// under-provisioned one kills a running agent, which is the failure erun
// currently explains after the fact with "likely out of memory". So erun raises
// on modest evidence and shrinks only on a long, quiet window, never below a
// healthy multiple of the peak it actually observed.

const (
	// runtimeSizingRaiseMemoryFraction is the share of the limit an observed
	// peak has to reach before erun says raise. Set well below 1.0 on purpose:
	// the sample interval means the true peak is always at least the peak
	// observed, so an environment already touching 90% has plausibly touched
	// more between two reads.
	runtimeSizingRaiseMemoryFraction = 0.90

	// runtimeSizingMemoryHeadroom is the multiple of the observed peak every
	// memory recommendation targets and no shrink may cross — the whole safety
	// margin of the shrink direction. 1.5 rather than 2.0 because 2.0 makes the
	// recommender useless in practice: an environment can only be shrunk when
	// its peak is under half the limit, and the two real over-provisioned
	// environments this was measured against sat at 48% and 52% — so a doubling
	// rule declares one of them fine and offers the other a rounding error. 1.5
	// leaves 50% headroom above the worst moment ever recorded across a day or
	// more, and the raise directions remain the backstop if that is ever wrong.
	//
	// It also cannot oscillate against runtimeSizingRaiseMemoryFraction: a
	// shrink needs the peak under 1/1.5 of the limit and a raise needs it over
	// 0.90, so the band between is hold, and a shrink lands exactly on the
	// shrink boundary rather than past it.
	runtimeSizingMemoryHeadroom = 1.5

	// runtimeSizingCPUHeadroom plays the same role for CPU, and is deliberately
	// larger than the memory multiple. memory.peak is a true instantaneous
	// high-water the kernel recorded; the CPU figure is an average over the
	// sample interval, so it flattens exactly the bursts a quota would throttle.
	// A figure that understates its own peak needs more slack, not less.
	runtimeSizingCPUHeadroom = 2.0

	// runtimeSizingCPURaiseMultiple is how much more CPU a throttled
	// environment is offered. Throttling proves demand exceeded the quota but
	// not by how much — the quota itself censors that — so the raise is a
	// deliberate half-step rather than the doubling a shrink's headroom uses.
	runtimeSizingCPURaiseMultiple = 1.5

	// runtimeSizingThrottleRatio is the share of scheduling periods that must
	// be throttled before erun says raise CPU. Measured on a live environment,
	// an ordinary week of work sat at 0.14% throttled — present but harmless —
	// so a threshold anywhere near zero would recommend growing every
	// environment in the fleet forever.
	runtimeSizingThrottleRatio = 0.05

	// runtimeSizingShrinkWindow is how long an environment must have been
	// watched before erun will suggest making it smaller. A day covers a normal
	// working cycle; anything shorter can miss the nightly build or the one
	// heavy agent run that sets the real peak.
	runtimeSizingShrinkWindow = 24 * time.Hour

	// runtimeSizingShrinkSamples guards the other way a window can lie. A
	// history whose first and last reading are a day apart but which holds four
	// samples watched almost nothing; the span alone is not coverage.
	runtimeSizingShrinkSamples = 120

	// runtimeSizingThrottlePeriods is the minimum number of scheduling periods
	// a throttle ratio must be computed over. A container seconds old has a few
	// hundred periods and a ratio that swings wildly.
	runtimeSizingThrottlePeriods = 10000

	// runtimeSizingMemoryGraduationMi rounds a memory suggestion up to a round
	// number of MiB. A recommendation of "18173Mi" implies a precision the
	// evidence does not have; rounding up also keeps the headroom guarantee.
	runtimeSizingMemoryGraduationMi = 256
)

// RuntimeSizingAction is the direction a recommendation points.
type RuntimeSizingAction string

const (
	RuntimeSizingHold  RuntimeSizingAction = "hold"
	RuntimeSizingRaise RuntimeSizingAction = "raise"
	RuntimeSizingLower RuntimeSizingAction = "lower"
	// RuntimeSizingUnknown is the honest answer when the window is too short or
	// the counter was unavailable. It is not the same as hold: hold means erun
	// looked and the size is right.
	RuntimeSizingUnknown RuntimeSizingAction = "insufficient-evidence"
)

// RuntimeSizingConfidence separates "evidence of harm" from "absence of harm".
// Only the raise directions are ever high: an OOM kill or sustained throttling
// is a fact about damage already done, while a quiet window is an argument from
// silence.
type RuntimeSizingConfidence string

const (
	RuntimeSizingConfidenceHigh RuntimeSizingConfidence = "high"
	RuntimeSizingConfidenceLow  RuntimeSizingConfidence = "low"
)

// RuntimeSizingVerdict is one resource's standing recommendation.
type RuntimeSizingVerdict struct {
	Resource   string                  `json:"resource"`
	Action     RuntimeSizingAction     `json:"action"`
	Current    string                  `json:"current,omitempty"`
	Suggested  string                  `json:"suggested,omitempty"`
	Confidence RuntimeSizingConfidence `json:"confidence,omitempty"`
	Reason     string                  `json:"reason"`
}

// RuntimeSizingRecommendation is an environment's standing answer to "how
// should this be sized". It recommends and never applies: resizing means a
// deploy, and an environment silently resized mid-run is a worse failure than
// any this prevents.
type RuntimeSizingRecommendation struct {
	// Knob is the setting an operator changes to act on this, and it is always
	// runtimepod — the one that sizes the runtime container. NamespaceQuota is a
	// different setting that caps the whole namespace; sending an operator there
	// makes them change something that resizes nothing.
	Knob     string                 `json:"knob"`
	Verdicts []RuntimeSizingVerdict `json:"verdicts"`
	Evidence RuntimeSizingEvidence  `json:"evidence"`
}

// RuntimeSizingEvidence is what the recommendation was derived from, carried
// with it so a reader can disagree with the conclusion on the merits.
type RuntimeSizingEvidence struct {
	// ObservedSeconds and Samples are the evidence window. A recommendation
	// that does not state its window cannot be judged.
	ObservedSeconds int64 `json:"observedSeconds"`
	Samples         int   `json:"samples"`
	// Restarts matters because memory.peak resets on each one. A peak that
	// survived restarts was retained by erun, not read from the counter.
	Restarts int `json:"restarts"`

	MemoryLimitBytes        int64 `json:"memoryLimitBytes,omitempty"`
	ObservedPeakMemoryBytes int64 `json:"observedPeakMemoryBytes,omitempty"`
	ObservedOOMKills        int64 `json:"observedOomKills"`

	CPUQuotaMilli            int64 `json:"cpuQuotaMilli,omitempty"`
	ObservedPeakCPUMilli     int64 `json:"observedPeakCpuMilli,omitempty"`
	ObservedPeriods          int64 `json:"observedPeriods,omitempty"`
	ObservedThrottledPeriods int64 `json:"observedThrottledPeriods,omitempty"`

	// Signals names the counter behind every figure above. It exists because the
	// obvious substitute is a host loadavg, and a loadavg answers a different
	// question: it counts the machine's runnable queue, so a 12-core
	// environment sitting at a load of 10 looks saturated while cpu.stat
	// reports not one throttled period. Nothing here is loadavg-derived.
	Signals []string `json:"signals,omitempty"`
	// Unavailable names counters the host did not supply, e.g. PSI, which is
	// absent on some kernels and which nothing here depends on.
	Unavailable []string `json:"unavailable,omitempty"`
}

// RuntimeSizingParams is the recommender's whole input, so it is a pure
// function of retained history and configuration.
type RuntimeSizingParams struct {
	History RuntimeUsageHistory
	// Ceiling bounds a raise. A namespace ResourceQuota is a hard admission
	// limit, so a runtimepod above what it can admit is a recommendation that
	// cannot be deployed. Zero means no quota is configured and no ceiling is
	// known here.
	Ceiling NamespaceResourceQuota
}

// RecommendRuntimeSizing derives an environment's standing recommendation from
// its retained history. Pure, so every direction is testable without a cluster.
// Reports false when there is no observation at all — an environment erun has
// never watched gets silence, not a guess.
//
// Note what it does *not* read: the environment's configured runtimepod. That
// value is the knob to change, not the size to reason from. The in-pod config
// carries no runtimepod at all (the chart injects the container's limits), so
// scoring the live container against the declared value would score it against
// NormalizeRuntimePodResources' defaults — on a 12-core, 23552Mi environment
// whose config is silent, that reads as a 8916Mi limit and turns a 2x
// over-provision into a recommendation to grow. The cgroup limit is the size
// the container is actually running under, so it is the size that is scored.
func RecommendRuntimeSizing(params RuntimeSizingParams) (RuntimeSizingRecommendation, bool) {
	history := params.History
	latest, ok := history.Latest()
	if !ok {
		return RuntimeSizingRecommendation{}, false
	}

	evidence := RuntimeSizingEvidence{
		ObservedSeconds:          int64(history.ObservedWindow() / time.Second),
		Samples:                  len(history.Samples),
		Restarts:                 history.Restarts,
		MemoryLimitBytes:         latest.Memory.LimitBytes,
		ObservedPeakMemoryBytes:  history.ObservedPeakMemoryBytes,
		ObservedOOMKills:         history.ObservedOOMKills,
		CPUQuotaMilli:            runtimeQuotaMilli(latest.CPU.QuotaCores),
		ObservedPeakCPUMilli:     history.ObservedPeakCPUMilli,
		ObservedPeriods:          history.ObservedPeriods,
		ObservedThrottledPeriods: history.ObservedThrottledPeriods,
		Signals:                  []string{"cgroup memory.peak", "cgroup memory.events oom_kill", "cgroup cpu.stat usage_usec/nr_throttled"},
		Unavailable:              runtimeUsageUnavailable(latest),
	}

	return RuntimeSizingRecommendation{
		Knob: "runtimepod",
		Verdicts: []RuntimeSizingVerdict{
			recommendRuntimeMemory(history, latest, params.Ceiling),
			recommendRuntimeCPU(history, latest),
		},
		Evidence: evidence,
	}, true
}

func recommendRuntimeMemory(history RuntimeUsageHistory, latest RuntimeUsage, ceiling NamespaceResourceQuota) RuntimeSizingVerdict {
	verdict := RuntimeSizingVerdict{Resource: "memory"}
	limit := latest.Memory.LimitBytes
	if limit <= 0 {
		verdict.Action = RuntimeSizingUnknown
		verdict.Reason = "memory.max reports no limit, so there is nothing to size against"
		return verdict
	}
	verdict.Current = formatBytesAsMi(limit)
	peak := history.ObservedPeakMemoryBytes

	if history.ObservedOOMKills > 0 {
		// A kill is evidence of harm, and one is enough. The observed peak is
		// also the wrong basis after a kill: the allocation that triggered it
		// was refused, so it never landed in memory.peak. Size from the limit
		// that proved too small instead.
		verdict.Action = RuntimeSizingRaise
		verdict.Confidence = RuntimeSizingConfidenceHigh
		suggested, bounded := boundRuntimeMemorySuggestion(scaleBytesToMi(limit, runtimeSizingMemoryHeadroom), ceiling)
		verdict.Suggested = suggested
		verdict.Reason = fmt.Sprintf("%d oom kill(s) at %s%s", history.ObservedOOMKills, formatBytesAsMi(limit), bounded)
		return verdict
	}

	if float64(peak) >= float64(limit)*runtimeSizingRaiseMemoryFraction {
		verdict.Action = RuntimeSizingRaise
		verdict.Confidence = RuntimeSizingConfidenceHigh
		suggested, bounded := boundRuntimeMemorySuggestion(scaleBytesToMi(peak, runtimeSizingMemoryHeadroom), ceiling)
		verdict.Suggested = suggested
		verdict.Reason = fmt.Sprintf("peak %s of %s (%s) is within the raise margin%s", formatBytesAsMi(peak), formatBytesAsMi(limit), formatPercent(peak, limit), bounded)
		return verdict
	}

	if reason, ok := runtimeSizingShrinkWindowShortfall(history); !ok {
		verdict.Action = RuntimeSizingUnknown
		verdict.Reason = fmt.Sprintf("peak %s of %s (%s), but %s", formatBytesAsMi(peak), formatBytesAsMi(limit), formatPercent(peak, limit), reason)
		return verdict
	}

	suggested := scaleBytesToMi(peak, runtimeSizingMemoryHeadroom)
	if suggested >= limit {
		verdict.Action = RuntimeSizingHold
		verdict.Reason = fmt.Sprintf("peak %s of %s (%s) leaves no room to shrink at %gx headroom", formatBytesAsMi(peak), formatBytesAsMi(limit), formatPercent(peak, limit), runtimeSizingMemoryHeadroom)
		return verdict
	}
	verdict.Action = RuntimeSizingLower
	// Low, always. A quiet window is an argument from silence: it says erun saw
	// no harm, not that none is possible.
	verdict.Confidence = RuntimeSizingConfidenceLow
	verdict.Suggested = formatBytesAsMi(suggested)
	verdict.Reason = fmt.Sprintf("peak %s of %s (%s), no oom kills, keeping %gx headroom", formatBytesAsMi(peak), formatBytesAsMi(limit), formatPercent(peak, limit), runtimeSizingMemoryHeadroom)
	return verdict
}

func recommendRuntimeCPU(history RuntimeUsageHistory, latest RuntimeUsage) RuntimeSizingVerdict {
	verdict := RuntimeSizingVerdict{Resource: "cpu"}
	quota := runtimeQuotaMilli(latest.CPU.QuotaCores)
	if quota <= 0 {
		verdict.Action = RuntimeSizingUnknown
		verdict.Reason = "cpu.max reports no quota, so there is nothing to size against"
		return verdict
	}
	verdict.Current = FormatKubernetesCPUFromMilli(quota)
	periods := history.ObservedPeriods
	throttled := history.ObservedThrottledPeriods

	if periods >= runtimeSizingThrottlePeriods && float64(throttled) >= float64(periods)*runtimeSizingThrottleRatio {
		verdict.Action = RuntimeSizingRaise
		verdict.Confidence = RuntimeSizingConfidenceHigh
		verdict.Suggested = FormatKubernetesCPUFromMilli(scaleMilliToWholeCores(quota, runtimeSizingCPURaiseMultiple))
		verdict.Reason = fmt.Sprintf("%s of scheduling periods throttled (%d of %d)", formatThrottleRatio(throttled, periods), throttled, periods)
		return verdict
	}

	throttleLabel := fmt.Sprintf("%s of scheduling periods throttled (%d of %d)", formatThrottleRatio(throttled, periods), throttled, periods)
	if reason, ok := runtimeSizingShrinkWindowShortfall(history); !ok {
		verdict.Action = RuntimeSizingUnknown
		verdict.Reason = fmt.Sprintf("%s, but %s", throttleLabel, reason)
		return verdict
	}
	if periods < runtimeSizingThrottlePeriods {
		verdict.Action = RuntimeSizingUnknown
		verdict.Reason = fmt.Sprintf("only %d scheduling periods observed, too few to read a throttle ratio from", periods)
		return verdict
	}
	if throttled > 0 {
		// Below the raise threshold but not at zero: the quota already binds
		// sometimes. That is not grounds to grow, and it is emphatically not
		// grounds to shrink — a ratio under the threshold is "tolerable", not
		// "unused". frs/local's measured 0.14% is exactly this case.
		verdict.Action = RuntimeSizingHold
		verdict.Reason = throttleLabel + ", tolerable but not unused"
		return verdict
	}
	if history.ObservedPeakCPUMilli <= 0 {
		verdict.Action = RuntimeSizingUnknown
		verdict.Reason = "no cpu rate observed between samples"
		return verdict
	}

	suggested := scaleMilliToWholeCores(history.ObservedPeakCPUMilli, runtimeSizingCPUHeadroom)
	peakLabel := fmt.Sprintf("busiest interval %s of %s", FormatKubernetesCPUFromMilli(history.ObservedPeakCPUMilli), FormatKubernetesCPUFromMilli(quota))
	if suggested >= quota {
		verdict.Action = RuntimeSizingHold
		verdict.Reason = fmt.Sprintf("%s, %s", peakLabel, throttleLabel)
		return verdict
	}
	verdict.Action = RuntimeSizingLower
	verdict.Confidence = RuntimeSizingConfidenceLow
	verdict.Suggested = FormatKubernetesCPUFromMilli(suggested)
	verdict.Reason = fmt.Sprintf("%s, %s, keeping %gx headroom", peakLabel, throttleLabel, runtimeSizingCPUHeadroom)
	return verdict
}

// runtimeSizingShrinkWindowShortfall gates every shrink on the same two window
// tests, and returns the shortfall as prose so the reason a recommendation was
// withheld is as visible as one that was made.
func runtimeSizingShrinkWindowShortfall(history RuntimeUsageHistory) (string, bool) {
	if window := history.ObservedWindow(); window < runtimeSizingShrinkWindow {
		return fmt.Sprintf("only %s observed of the %s a shrink needs", FormatObservedWindow(window), FormatObservedWindow(runtimeSizingShrinkWindow)), false
	}
	if len(history.Samples) < runtimeSizingShrinkSamples {
		return fmt.Sprintf("only %d samples retained of the %d a shrink needs", len(history.Samples), runtimeSizingShrinkSamples), false
	}
	return "", true
}

// boundRuntimeMemorySuggestion clamps a raise to what the namespace can admit,
// returning the clamped value and a note for the verdict's reason when it bit.
// A ResourceQuota counts every container in the pod, so the erun-dind sidecar's
// own limit is spent before the runtime container gets anything, and a
// suggestion above the remainder is one Kubernetes would refuse to schedule.
// This is a bound, not the knob: raising the quota is a separate decision from
// resizing the pod.
func boundRuntimeMemorySuggestion(suggested int64, ceiling NamespaceResourceQuota) (string, string) {
	quotaMi, err := ParseKubernetesMemoryToMi(ceiling.Memory)
	if err != nil {
		return formatBytesAsMi(suggested), ""
	}
	dindMi, err := ParseKubernetesMemoryToMi(DefaultRuntimeDindMemory)
	if err != nil {
		return formatBytesAsMi(suggested), ""
	}
	const mi = 1024 * 1024
	available := (quotaMi - dindMi) * mi
	if available <= 0 || suggested <= available {
		return formatBytesAsMi(suggested), ""
	}
	graduation := int64(runtimeSizingMemoryGraduationMi) * mi
	note := fmt.Sprintf("; bounded by the %s namespace quota less the dind sidecar's %s", ceiling.Memory, DefaultRuntimeDindMemory)
	return formatBytesAsMi(available - available%graduation), note
}

// scaleBytesToMi multiplies a byte figure and rounds it up to a round number of
// MiB. Rounding up, never down, is what keeps the headroom guarantee intact
// after graduation.
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

// scaleMilliToWholeCores multiplies a millicore figure and rounds up to a whole
// core. Every environment in the fleet is sized in whole cores, and a
// fractional suggestion derived from sampled intervals claims a precision the
// samples do not have.
func scaleMilliToWholeCores(milli int64, multiple float64) int64 {
	scaled := int64(math.Ceil(float64(milli) * multiple))
	cores := (scaled + 999) / 1000
	if cores < 1 {
		cores = 1
	}
	return cores * 1000
}

func formatBytesAsMi(bytes int64) string {
	return fmt.Sprintf("%dMi", (bytes+1024*1024-1)/(1024*1024))
}

// formatThrottleRatio keeps two decimals because the interesting throttle
// ratios are fractions of a percent: a measured 425 of 308631 periods rounds to
// "0%" at whole-number precision, which reads as "never throttled" when the
// point of the figure is that it was, a little.
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

// runtimeQuotaMilli converts RuntimeCPUUsage's cores-based quota into the
// millicore unit every sizing figure and NamespaceResourceQuota comparison in
// this file uses.
func runtimeQuotaMilli(quotaCores float64) int64 {
	return int64(math.Round(quotaCores * 1000))
}

// runtimeUsageUnavailable collects the latest reading's per-resource
// unavailability into the flat list RuntimeSizingEvidence reports, since
// RuntimeUsage names each gap on its own CPU/Memory reading rather than in one
// shared list.
func runtimeUsageUnavailable(latest RuntimeUsage) []string {
	var unavailable []string
	if latest.Memory.Unavailable != "" {
		unavailable = append(unavailable, latest.Memory.Unavailable)
	}
	if latest.CPU.Unavailable != "" {
		unavailable = append(unavailable, latest.CPU.Unavailable)
	}
	return unavailable
}

// FormatRuntimeSizingVerdict renders one resource's verdict as prose: the
// direction, the suggested/current values when set, and the reason -- the
// same text every consumer (`erun list`, `erun resize`) shows, so a
// recommendation reads identically wherever it appears.
func FormatRuntimeSizingVerdict(verdict RuntimeSizingVerdict) string {
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

// FormatRuntimeSizingEvidence renders the window, sample count, restarts,
// knob, and signal sources a recommendation was computed from -- the input a
// verdict cannot be judged without.
func FormatRuntimeSizingEvidence(sizing RuntimeSizingRecommendation) string {
	evidence := sizing.Evidence
	return fmt.Sprintf("%s observed, %d samples, %d restarts, knob=%s, from %s (not loadavg)",
		FormatObservedWindow(time.Duration(evidence.ObservedSeconds)*time.Second),
		evidence.Samples, evidence.Restarts, sizing.Knob, strings.Join(evidence.Signals, ", "))
}

// FormatObservedWindow renders an evidence window at minute resolution. Whole
// minutes keep the figure stable between two readings taken moments apart,
// which a seconds-resolution figure would not be.
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
