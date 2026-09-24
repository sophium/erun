import type { UIEnvironmentUsageSnapshot } from '@/uiEnvironmentUsageTypes';

import { formatElapsed } from './activityQueueState';
import { cumulativeCPUSeconds } from './runtimeCPUSeconds';

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
      // builds is the erun-dind sidecar's own reading, present only when the
      // environment carries that sidecar AND its cgroup could be read. Its
      // absence is a real distinction — an older runtime image, a sidecar
      // mid-restart — and must render as "not read", never as a zero: on a
      // build-capable environment the runtime container's CPU is near zero by
      // construction (a release lane spends its time waiting on bounded
      // `erun exec job await` calls), so a fabricated 0 here would be
      // indistinguishable from a build that is genuinely not running.
      builds?: UsageBuildsSummary;
      ageLabel: string;
      stale: boolean;
    };

// UsageBuildsSummary is the sidecar's CPU as the card can honestly state it,
// with its memory and its scope as the lines beneath.
export interface UsageBuildsSummary {
  // value is the sidecar's CPU figure: a quota-relative percentage when the
  // sidecar declares a cpu.max quota, and otherwise its cumulative
  // CPU-seconds, which is the only CPU number a container without a ceiling can
  // offer. '—' when neither was read.
  value: string;
  // utilization is present only when a percentage was really measured; its
  // absence is what keeps an unmeasured or cumulative figure from borrowing a
  // strip that means "measured".
  utilization?: number;
  // suffix is the short muted qualifier beside the value ('no quota'), '' when
  // the value speaks for itself.
  suffix: string;
  // caption names the sidecar and its memory ('erun-dind sidecar · 267Mi of
  // 20.0Gi'), with either part omitted when it could not be read.
  caption: string;
  // note is the reader's own reason, rendered only when the CPU could not be
  // measured at all.
  note: string;
}

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
    builds: buildsMetric(usage.dind),
    ageLabel,
    stale,
  };
}

// buildsMetric reduces the erun-dind sidecar's reading to one row, or returns
// undefined when there is no sidecar reading to show — which the card renders
// as nothing at all, leaving the age caption's "excludes builds" caveat to do
// its original job.
//
// The cumulative-CPU-seconds arm is not a fallback for a failure: cpu.max has
// no quota on most sidecars (it declares no limit so a build can use the node),
// so a percentage cannot exist there and utilisation alone would report
// "Unavailable" on exactly the environments this row is for. `usageUsec` is a
// real measurement, and stating it as CPU-seconds rather than as a percentage
// keeps a cumulative counter from reading as a rate.
function buildsMetric(
  dind: UIEnvironmentUsageSnapshot['usage']['dind'],
): UsageBuildsSummary | undefined {
  if (!dind) {
    return undefined;
  }
  const caption = dindCaption(dind.memory);
  if (dind.cpu.available) {
    const percent = measuredPercent(dind.cpu.utilizationPercent);
    return {
      value: dind.cpu.utilization ?? percentLabel(percent),
      utilization: percent,
      suffix: '',
      caption,
      note: '',
    };
  }
  const seconds = cumulativeCPUSeconds(dind.cpu.usageUsec);
  if (seconds !== null) {
    return {
      value: `${String(seconds)} CPU-s`,
      suffix: 'no quota',
      caption,
      note: '',
    };
  }
  return {
    value: '—',
    suffix: '',
    caption,
    note: dind.cpu.unavailable ?? 'the erun-dind CPU reading was not available',
  };
}

// dindCaption names the domain the Builds row belongs to and, when it could be
// read, the sidecar's memory: current against its own ceiling when it has one,
// and the used figure alone when it declares none — the same distinction the
// Memory row above makes, at caption length. The domain name is not optional:
// the card's other rows are the runtime container, and "which of these two
// numbers is my build" is the whole question this row exists to answer.
function dindCaption(memory: UIEnvironmentUsageSnapshot['usage']['memory']): string {
  const sidecar = 'erun-dind sidecar';
  if (!memory.available) {
    return sidecar;
  }
  const figure = memory.unlimited
    ? `${memory.current ?? '—'} (no limit)`
    : `${memory.current ?? '—'} of ${memory.limit ?? '—'}`;
  return `${sidecar} · ${figure}`;
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
