import type { UIEnvironmentUsageSnapshot } from '@/uiEnvironmentUsageTypes';
import type { UIRuntimeCPUUsage } from '@/uiRuntimeTypes';

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
  const build = buildFigurePart(usage.build);
  if (build) {
    parts.push(build);
  }
  return parts;
}

// buildFigurePart renders the build cap cgroup's own reading, because on a
// build-capable environment it is the figure that moves: an image build runs in
// the erun-dind sidecar, so a saturated build shows here at its cap while the
// CPU figure beside it sits near zero. Without it an operator comparing
// environments sees a busy builder as an idle one and adds work to it.
function buildFigurePart(build: UIRuntimeCPUUsage | undefined): string {
  if (!build?.available) {
    return '';
  }
  return `Build ${build.utilization ?? '—'} of ${build.quota ?? '—'}${throttleLabel(build)}`;
}

// throttleLabel names starvation, which the percentage alone cannot: a build
// pinned at its cap and one merely busy at it read the same utilization.
function throttleLabel(cpu: UIRuntimeCPUUsage): string {
  if (!cpu.periods || !cpu.throttledPeriods) {
    return '';
  }
  return ` (throttled ${String(cpu.throttledPeriods)}/${String(cpu.periods)})`;
}

function percentLabel(value: number | undefined): string {
  return value === undefined || !Number.isFinite(value) ? '—' : `${value.toFixed(0)}%`;
}
