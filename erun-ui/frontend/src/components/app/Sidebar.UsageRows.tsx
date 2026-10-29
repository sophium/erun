import * as React from 'react';

import {
  summarizeEnvironmentUsageMetrics,
  type UsageBuildsSummary,
  type UsageMetricSummary,
} from '@/app/environmentUsageSummary';
import { DecileStrip } from '@/components/app/Sidebar.DecileStrip';
import {
  HOVER_CARD_CAPTION_CLASS,
  HOVER_CARD_CAPTION_DEGRADED_CLASS,
  HOVER_CARD_VALUE_STACK_CLASS,
  HoverCardMuted,
  HoverCardRow,
} from '@/components/app/Sidebar.HoverCardRow';
import type { UIEnvironmentUsageSnapshot } from '@/uiEnvironmentUsageTypes';

// UsageRows renders the cached usage reading. Measured zero (empty strip) and
// unmeasured (dash) stay distinct, and CPU/Memory exclude the erun-dind sidecar,
// which the Builds row reports.
export function UsageRows({
  usage,
  excludesBuilds,
}: {
  usage: UIEnvironmentUsageSnapshot | undefined;
  excludesBuilds: boolean;
}): React.ReactElement {
  const metrics = summarizeEnvironmentUsageMetrics(usage, Date.now());
  if (metrics.kind === 'unread') {
    return (
      <HoverCardRow label="Usage">
        <HoverCardMuted>{metrics.detail}</HoverCardMuted>
      </HoverCardRow>
    );
  }
  const scopeCaveat = excludesBuilds ? ' — CPU and memory exclude builds' : '';
  return (
    <>
      <HoverCardRow label="CPU">
        <UsageMetric metric={metrics.cpu} stale={metrics.stale} />
      </HoverCardRow>
      <HoverCardRow label="Memory">
        <UsageMetric metric={metrics.memory} stale={metrics.stale} />
      </HoverCardRow>
      {metrics.builds && <BuildsRow builds={metrics.builds} stale={metrics.stale} />}
      <HoverCardRow label="">
        <HoverCardMuted>
          {metrics.stale ? 'Stale — as of' : 'As of'} {metrics.ageLabel} ago{scopeCaveat}
        </HoverCardMuted>
      </HoverCardRow>
    </>
  );
}

// BuildsRow is the erun-dind sidecar's reading. A sidecar without a cpu.max
// quota reports CPU-seconds and gets no strip.
function BuildsRow({
  builds,
  stale,
}: {
  builds: UsageBuildsSummary;
  stale: boolean;
}): React.ReactElement {
  return (
    <HoverCardRow label="Builds">
      <div className={HOVER_CARD_VALUE_STACK_CLASS}>
        <UsageMetric
          metric={{
            label: 'Builds',
            value: builds.value,
            suffix: builds.suffix,
            percent: builds.utilization,
          }}
          stale={stale}
        />
        {builds.caption && <span className={HOVER_CARD_CAPTION_CLASS}>{builds.caption}</span>}
        {builds.note && <span className={HOVER_CARD_CAPTION_DEGRADED_CLASS}>{builds.note}</span>}
      </div>
    </HoverCardRow>
  );
}

function UsageMetric({
  metric,
  stale,
}: {
  metric: UsageMetricSummary;
  stale: boolean;
}): React.ReactElement {
  const figure = (
    <span className={stale ? 'text-muted-foreground/70' : undefined}>{metric.value}</span>
  );
  return (
    <div className={HOVER_CARD_VALUE_STACK_CLASS}>
      {metric.suffix ? (
        <span className="flex items-baseline justify-between gap-2">
          {figure}
          <span className={HOVER_CARD_CAPTION_CLASS}>{metric.suffix}</span>
        </span>
      ) : (
        figure
      )}
      {metric.percent !== undefined && <DecileStrip percent={metric.percent} />}
    </div>
  );
}
