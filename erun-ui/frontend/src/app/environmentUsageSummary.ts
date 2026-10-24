import type { UIEnvironmentUsageSnapshot } from '@/uiEnvironmentUsageTypes';

import { formatElapsed } from './activityQueueState';
import { cumulativeCPUSeconds } from './runtimeCPUSeconds';

// EnvironmentUsageSummary is a cached usage reading shaped for a hover card,
// with its age so a stale number never reads as live.
export interface EnvironmentUsageSummary {
  // headline is '' when nothing is measurable; detail then says why.
  headline: string;
  detail: string;
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

// NO_USAGE_READING_DETAIL marks a never-sampled environment; it must never
// render like a measured zero.
export const NO_USAGE_READING_DETAIL = 'no reading yet';

// UsageMetricSummary is one metric (CPU or memory) for its own labelled row.
export interface UsageMetricSummary {
  label: string;
  // value is '—' when only this metric was unreadable, never a bare 0.
  value: string;
  suffix: string;
  // percent is present only when measured: an empty strip means a measured zero,
  // so an unmeasured metric renders no strip.
  percent?: number;
}

// EnvironmentUsageMetrics is the card's CPU and memory rows, or why there are none.
export type EnvironmentUsageMetrics =
  | { kind: 'unread'; detail: string }
  | {
      kind: 'reading';
      cpu: UsageMetricSummary;
      memory: UsageMetricSummary;
      // builds is present whenever the environment has an erun-dind sidecar, even
      // when unreadable ('—' with a reason); the runtime container's CPU stays near
      // zero during builds, so this row is what shows one running.
      builds?: UsageBuildsSummary;
      ageLabel: string;
      stale: boolean;
    };

// UsageBuildsSummary is the dind sidecar's CPU, memory and scope.
export interface UsageBuildsSummary {
  // value is a quota-relative percentage, or cumulative CPU-seconds when the
  // sidecar has no cpu.max quota; '—' when neither was read.
  value: string;
  // utilization is present only for a measured percentage.
  utilization?: number;
  suffix: string;
  caption: string;
  // note is shown only when CPU could not be measured at all.
  note: string;
}

// summarizeEnvironmentUsageMetrics splits the snapshot per metric, keeping the
// raw percentages each row's decile strip needs.
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
    cpu: cpuMetric(usage.cpu, usage.requests),
    memory: memoryMetric(usage.memory, usage.requests),
    builds: buildsMetric(usage),
    ageLabel,
    stale,
  };
}

// DIND_SIDECAR_NAME labels every Builds line so a figure is never mistaken for
// the runtime container's.
const DIND_SIDECAR_NAME = 'erun-dind sidecar';

// buildsMetric returns undefined only when the environment has no sidecar.
// Most sidecars declare no CPU quota, so cumulative CPU-seconds is a real
// reading there, not a fallback.
function buildsMetric(usage: UIEnvironmentUsageSnapshot['usage']): UsageBuildsSummary | undefined {
  const dind = usage.dind;
  if (!dind) {
    // A sidecar that could not be read still gets a not-read row; only an
    // environment without one (excludesBuilds) gets none.
    if (!usage.excludesBuilds) {
      return undefined;
    }
    return {
      value: '—',
      suffix: '',
      caption: DIND_SIDECAR_NAME,
      note: 'the sidecar could not be read',
    };
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

// dindCaption names the sidecar and, when read, its memory against its ceiling.
function dindCaption(memory: UIEnvironmentUsageSnapshot['usage']['memory']): string {
  if (!memory.available) {
    return DIND_SIDECAR_NAME;
  }
  const figure = memory.unlimited
    ? `${memory.current ?? '—'} (no limit)`
    : `${memory.current ?? '—'} of ${memory.limit ?? '—'}`;
  return `${DIND_SIDECAR_NAME} · ${figure}`;
}

function cpuMetric(
  usage: UIEnvironmentUsageSnapshot['usage']['cpu'],
  requests: UIEnvironmentUsageSnapshot['usage']['requests'],
): UsageMetricSummary {
  if (!usage.available) {
    return { label: 'CPU', value: '—', suffix: '' };
  }
  return {
    label: 'CPU',
    value: usage.utilization ?? percentLabel(usage.utilizationPercent),
    suffix: requestSuffix(requests?.runtime.cpu),
    percent: measuredPercent(usage.utilizationPercent),
  };
}

// memoryMetric calls the ceiling a limit and states the request beside it: a
// cgroup limit reserves nothing.
function memoryMetric(
  usage: UIEnvironmentUsageSnapshot['usage']['memory'],
  requests: UIEnvironmentUsageSnapshot['usage']['requests'],
): UsageMetricSummary {
  if (!usage.available) {
    return { label: 'Memory', value: '—', suffix: '' };
  }
  // Unlimited is a real reading with no percentage, so no strip.
  if (usage.unlimited) {
    return { label: 'Memory', value: usage.current ?? '—', suffix: 'no limit' };
  }
  const percent = measuredPercent(usage.percentOfLimit);
  const limit = usage.limit ? `of ${usage.limit} limit` : '';
  const requested = requestSuffix(requests?.runtime.memory);
  return {
    label: 'Memory',
    value: percentLabel(percent),
    suffix: [limit, requested].filter(Boolean).join(' · '),
    percent,
  };
}

// requestSuffix renders nothing for an undeclared request, not "0 requested".
function requestSuffix(requested: string | undefined): string {
  return requested ? `${requested} requested` : '';
}

// measuredPercent treats a missing percentage as zero: the Go side omits zero
// values (omitempty), and callers guard on `available` for unread metrics.
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
        : `Mem ${percentLabel(usage.memory.percentOfLimit)} of ${usage.memory.limit ?? '—'} limit`,
    );
  }
  return parts;
}

function percentLabel(value: number | undefined): string {
  return value === undefined || !Number.isFinite(value) ? '—' : `${value.toFixed(0)}%`;
}
