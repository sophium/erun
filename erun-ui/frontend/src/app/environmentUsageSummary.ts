import type { UIEnvironmentUsageSnapshot } from '@/uiEnvironmentUsageTypes';

import { formatElapsed } from './activityQueueState';

// EnvironmentUsageSummary reduces one environment's cached usage reading
// (environment_usage.go) to what a hover card can render on one or two lines:
// a compact, comparable headline when there is a real reading, a reason when
// there is not, and the reading's own age so a stale number is never shown as
// if it were live (root AGENTS.md, "Smooth, Seamless, No Dead Ends" — an
// unlabelled stale number is worse than none).
export interface EnvironmentUsageSummary {
  // headline is the compact figures line ("CPU 12% · Mem 68% of 2048Mi"), or
  // '' when there is nothing measurable to show — in which case detail names
  // why.
  headline: string;
  detail: string;
  // ageLabel is '' until a reading has been observed at least once.
  ageLabel: string;
  stale: boolean;
  hasReading: boolean;
}

const NO_READING: EnvironmentUsageSummary = {
  headline: '',
  detail: 'Not yet observed.',
  ageLabel: '',
  stale: false,
  hasReading: false,
};

// NO_USAGE_READING_DETAIL is what an environment that has never been sampled
// says, as distinct from a measured zero. The two must never share a rendering:
// a zero is a reading ("this env is idle"), and this is the absence of one.
export const NO_USAGE_READING_DETAIL = 'no reading yet';

// UsageMetricSummary is one metric of a reading -- CPU or memory -- shaped for
// its own labelled row with a decile strip beneath it.
export interface UsageMetricSummary {
  label: string;
  // value is the primary figure ('68.4%'), or '—' when this one metric could
  // not be read while its sibling could. Never a bare 0 for unmeasured.
  value: string;
  // suffix is the muted trailing detail ('of 23.0 GiB'), '' when there is none.
  suffix: string;
  // percent is the metric's own share of its ceiling, and is present ONLY when
  // that share was really measured. Its absence is what distinguishes "render no
  // strip" from "render an empty strip": an empty strip means a measured zero,
  // so an unmeasured metric must not borrow one.
  percent?: number;
}

// EnvironmentUsageMetrics is the environment card's live-state rows: separate
// CPU and memory metrics, or the reason there are none.
export type EnvironmentUsageMetrics =
  | { kind: 'unread'; detail: string }
  | {
      kind: 'reading';
      cpu: UsageMetricSummary;
      memory: UsageMetricSummary;
      ageLabel: string;
      stale: boolean;
    };

// summarizeEnvironmentUsageMetrics reduces the same cached snapshot as
// summarizeEnvironmentUsage, but per metric rather than to one headline: the
// card renders CPU and memory as separate rows so each can carry its own decile
// strip, and an encoding needs the raw percentage, which a joined string
// ('CPU 12% · Mem 25% of 2048Mi') has already discarded.
export function summarizeEnvironmentUsageMetrics(
  snapshot: UIEnvironmentUsageSnapshot | undefined,
  nowMs: number,
): EnvironmentUsageMetrics {
  if (!snapshot) {
    return { kind: 'unread', detail: NO_USAGE_READING_DETAIL };
  }
  const { usage, observedAtUnix, staleAfterSeconds } = snapshot;
  if (!usage.available) {
    return { kind: 'unread', detail: usage.message ?? 'Usage unavailable.' };
  }
  // A probe that answered but could read neither figure has no rows to render
  // and one reason worth stating, so it reports that reason rather than a pair
  // of empty dashes.
  if (!usage.cpu.available && !usage.memory.available) {
    return {
      kind: 'unread',
      detail: "This environment's own CPU and memory usage could not be read.",
    };
  }
  const ageSeconds = Math.max(0, nowMs / 1000 - observedAtUnix);
  const stale = staleAfterSeconds > 0 && ageSeconds > staleAfterSeconds;
  const ageLabel = formatElapsed(new Date(observedAtUnix * 1000).toISOString(), nowMs).trim();
  return {
    kind: 'reading',
    cpu: cpuMetric(usage.cpu),
    memory: memoryMetric(usage.memory),
    ageLabel,
    stale,
  };
}

function cpuMetric(usage: UIEnvironmentUsageSnapshot['usage']['cpu']): UsageMetricSummary {
  if (!usage.available) {
    return { label: 'CPU', value: '—', suffix: '' };
  }
  return {
    label: 'CPU',
    value: usage.utilization ?? percentLabel(usage.utilizationPercent),
    suffix: '',
    percent: measuredPercent(usage.utilizationPercent),
  };
}

function memoryMetric(usage: UIEnvironmentUsageSnapshot['usage']['memory']): UsageMetricSummary {
  if (!usage.available) {
    return { label: 'Memory', value: '—', suffix: '' };
  }
  // No ceiling declared is a real reading, not a failure -- but it has no
  // percentage, so it gets no strip rather than an empty one.
  if (usage.unlimited) {
    return { label: 'Memory', value: usage.current ?? '—', suffix: 'no limit' };
  }
  const percent = measuredPercent(usage.percentOfLimit);
  return {
    label: 'Memory',
    value: percentLabel(percent),
    suffix: usage.limit ? `of ${usage.limit}` : '',
    percent,
  };
}

// measuredPercent resolves a metric's share of its ceiling to a number whenever
// the metric was measured at all.
//
// A missing percentage on an AVAILABLE metric means the reading was zero, not
// that nothing was measured: the Go side carries these fields with
// `omitempty` (ui_model.go), and it sets `utilization` unconditionally on every
// available CPU reading (`fmt.Sprintf("%.1f%%", ...)`, runtime_usage.go), so a
// zero arrives as `"0.0%"` with the number simply dropped from the JSON. The
// `?? 0` is what keeps a measured zero rendering its empty strip instead of no
// strip at all -- the exact confusion between "idle" and "unmeasured" this
// whole encoding exists to remove.
//
// The `available` guard above is what still separates a genuinely unread metric
// from a zero one.
function measuredPercent(value: number | undefined): number | undefined {
  if (value === undefined) {
    return 0;
  }
  return Number.isFinite(value) ? value : undefined;
}

export function summarizeEnvironmentUsage(
  snapshot: UIEnvironmentUsageSnapshot | undefined,
  nowMs: number,
): EnvironmentUsageSummary {
  if (!snapshot) {
    return NO_READING;
  }
  const { usage, observedAtUnix, staleAfterSeconds } = snapshot;
  const ageSeconds = Math.max(0, nowMs / 1000 - observedAtUnix);
  const stale = staleAfterSeconds > 0 && ageSeconds > staleAfterSeconds;
  const ageLabel = formatElapsed(new Date(observedAtUnix * 1000).toISOString(), nowMs).trim();
  const { headline, detail } = usageHeadlineAndDetail(usage);
  return { headline, detail, ageLabel, stale, hasReading: true };
}

// usageHeadlineAndDetail is split out of summarizeEnvironmentUsage to keep
// that function's complexity within budget: it owns only the figures
// themselves, never the age/staleness inputs.
function usageHeadlineAndDetail(usage: UIEnvironmentUsageSnapshot['usage']): {
  headline: string;
  detail: string;
} {
  if (!usage.available) {
    return { headline: '', detail: usage.message ?? 'Usage unavailable.' };
  }
  const parts = usageFigureParts(usage);
  if (parts.length === 0) {
    return {
      headline: '',
      detail: "This environment's own CPU and memory usage could not be read.",
    };
  }
  return { headline: parts.join(' · '), detail: '' };
}

function usageFigureParts(usage: UIEnvironmentUsageSnapshot['usage']): string[] {
  const parts: string[] = [];
  if (usage.cpu.available) {
    parts.push(`CPU ${usage.cpu.utilization ?? '—'}`);
  }
  if (usage.memory.available) {
    parts.push(
      usage.memory.unlimited
        ? `Mem ${usage.memory.current ?? '—'} (no limit)`
        : `Mem ${percentLabel(usage.memory.percentOfLimit)} of ${usage.memory.limit ?? '—'}`,
    );
  }
  return parts;
}

function percentLabel(value: number | undefined): string {
  return value === undefined || !Number.isFinite(value) ? '—' : `${value.toFixed(0)}%`;
}
