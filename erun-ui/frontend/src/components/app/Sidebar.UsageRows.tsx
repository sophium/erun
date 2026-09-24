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

// The environment card's usage zone, split out of Sidebar.EnvHoverCard.tsx: it
// is one cohesive responsibility (the sweep's cached reading and the three
// states it can be in) and the card around it is at its own size budget.

// UsageRows renders the environment-usage sweep's cached reading
// (environment_usage.go) as separate CPU and memory rows, each with a decile
// strip, so idle and loaded stop looking identical: one joined string
// ("CPU 12% · Mem 68% of 2048Mi") gave the two metrics the same shape, weight
// and wrap whatever they read, leaving state legible only by parsing digits.
//
// Three states, and they must not collapse into each other:
//
//   - A reading renders two rows. Each metric may carry a `percent`, and only
//     then does it get a strip: a measured zero renders an EMPTY (outlined)
//     strip, while a metric that could not be measured at all renders a dash
//     and no strip, so "idle" and "unmeasured" stay distinguishable.
//   - An unread environment (never sampled, unavailable, or neither figure
//     readable) renders ONE row naming the reason. There are no metrics to
//     give separate rows to, and the reason is the actionable part -- in
//     particular a never-sampled environment must say `no reading yet` rather
//     than borrow the empty strip that means "measured zero".
//   - A stale reading keeps its figures and strips and says so on the caption
//     row; the strip is a real measurement, just an old one.
//
// A stale or unmeasurable reading is rendered as degraded, never as an amber
// warning: nothing the operator did caused either state and no action follows
// from it, so it should recede rather than alarm (see the TYPE note in
// Sidebar.HoverCardRow.tsx). Amber on this card is reserved for the strip's own
// near-ceiling threshold, which is a different claim: the reading is fine, the
// resource is nearly out.
//
// The CPU and Memory rows are scoped to the runtime container's own cgroup,
// which is never where a build runs -- every image build executes in the
// erun-dind sidecar instead, so those figures can read idle while that sidecar
// saturates the node. On a build-capable environment the runtime container is
// near-idle by construction, not because anything is wrong: a release lane
// spends its time waiting on bounded `erun exec job await` calls. So those
// figures cannot answer "is my build working" at all, and the one number that
// can -- the sidecar's own cgroup reading, which `erun usage` already reports
// (erun-common/runtime_usage.go's RunRuntimeUsage execs the same script into
// that container) -- is the Builds row below.
//
// It does not cover the build containers themselves: those run as cgroup
// siblings of the sidecar rather than descendants, so nothing reachable from
// inside this pod attributes their usage to this environment. What the sidecar
// reading does establish is whether the environment is doing work at all, which
// is the question the near-zero runtime figure cannot answer. The `excludesBuilds`
// caption stays beside the age for the environment where the sidecar could not
// be read at all (environmentUsesDindSidecar in Sidebar.helpers.ts).
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
  // The caveat stays on the age row and keeps its original meaning: CPU and
  // Memory above exclude the sidecar, whatever else this card now also shows.
  // The Builds row beneath is where the excluded work actually appears.
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
      {/* The reading's age is its own row, under the metrics it qualifies --
          a caption spanning both metrics must not sit under only one of them. */}
      <HoverCardRow label="">
        <HoverCardMuted>
          {metrics.stale ? 'Stale — as of' : 'As of'} {metrics.ageLabel} ago{scopeCaveat}
        </HoverCardMuted>
      </HoverCardRow>
    </>
  );
}

// BuildsRow is the erun-dind sidecar's own reading, under the two rows it must
// not be confused with. Its label column stays one word so the card's fixed
// label width is unchanged; the caption is where it says which domain it is.
//
// The strip follows the same rule as every other metric on this card: present
// only when a percentage was really measured. A sidecar with no cpu.max quota
// reports cumulative CPU-seconds instead -- a real reading with no ceiling to
// be a fraction of -- and gets no strip rather than an empty one.
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

// UsageMetric is one metric's value, its muted trailing suffix, and its decile
// strip. The suffix is pushed to the right edge of the value column so the
// figures themselves stack down one left edge and stay comparable.
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
