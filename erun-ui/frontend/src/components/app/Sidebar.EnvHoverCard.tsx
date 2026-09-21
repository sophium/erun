import { Popover, PopoverAnchor, PopoverContent } from 'erun-kit';
import { TriangleAlert } from 'lucide-react';
import * as React from 'react';

import { type EnvironmentNodeIndicator, environmentNodeLabel } from '@/app/environmentNodeState';
import {
  summarizeEnvironmentUsageMetrics,
  type UsageMetricSummary,
} from '@/app/environmentUsageSummary';
import {
  type ErunVersionSummary,
  summarizeErunVersion,
  summarizeRuntimeVersionLine,
} from '@/app/environmentVersionLines';
import { useHoverCardOpenState } from '@/app/useHoverCardOpenState';
import { DecileStrip } from '@/components/app/Sidebar.DecileStrip';
import type { EnvironmentIndicator } from '@/components/app/Sidebar.helpers';
import {
  HOVER_CARD_ALERT_CLASS,
  HOVER_CARD_CAPTION_CLASS,
  HOVER_CARD_GRID_CLASS,
  HOVER_CARD_TRUNCATE_CLASS,
  HOVER_CARD_VALUE_STACK_CLASS,
  HoverCardBadge,
  HoverCardMuted,
  HoverCardRow,
  HoverCardTitle,
} from '@/components/app/Sidebar.HoverCardRow';
import type { UISelection, UIWorkingIssue } from '@/types';
import type { UIEnvironmentNodeSnapshot } from '@/uiEnvironmentNodeTypes';
import type { UIEnvironmentUsageSnapshot } from '@/uiEnvironmentUsageTypes';
import type {
  UIErunVersion,
  UIRuntimeImageLineMismatch,
  UIRuntimeVersionLine,
} from '@/uiRuntimeVersionLineTypes';

import { EnvironmentWorkingIssue } from '../../../wailsjs/go/main/App';

// EnvTypeBadge shows a host env's distinct badge, a local-agent env's "Local"
// badge, or nothing for a remote/runtime env.
function EnvTypeBadge({
  isLocal,
  isHost,
}: {
  isLocal: boolean;
  isHost: boolean;
}): React.ReactElement | null {
  if (isHost) {
    return (
      <HoverCardBadge ariaLabel="Host environment — no pod, this machine only">Host</HoverCardBadge>
    );
  }
  if (!isLocal) {
    return null;
  }
  return <HoverCardBadge>Local</HoverCardBadge>;
}

// EnvHoverCard shows an env row's details in a Popover rather than a tooltip
// (a multi-field card doesn't belong in a tooltip; see erun-ui/AGENTS.md),
// without swallowing the row's own click-to-open and edit affordances.
export function EnvHoverCard({
  className,
  tenantName,
  environmentName,
  selection,
  isLocal,
  isHost,
  runtimeVersion,
  runtimeVersionLine,
  erunVersion,
  runtimeImageLineMismatch,
  activityLabel,
  indicator,
  nodeIndicator,
  node,
  usage,
  usageExcludesBuilds,
  children,
}: {
  className?: string;
  tenantName: string;
  environmentName: string;
  selection: UISelection;
  isLocal: boolean;
  isHost: boolean;
  runtimeVersion: string;
  runtimeVersionLine?: UIRuntimeVersionLine;
  erunVersion?: UIErunVersion;
  runtimeImageLineMismatch?: UIRuntimeImageLineMismatch;
  activityLabel: string;
  indicator: EnvironmentIndicator;
  nodeIndicator: EnvironmentNodeIndicator;
  node: UIEnvironmentNodeSnapshot | undefined;
  usage: UIEnvironmentUsageSnapshot | undefined;
  usageExcludesBuilds: boolean;
  children: React.ReactNode;
}): React.ReactElement {
  const { open, setOpen, openNow, closeSoon } = useHoverCardOpenState();

  const issue = useWorkingIssue(selection, open);
  const erunVersionSummary = summarizeErunVersion(runtimeVersion.trim() !== '', erunVersion);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverAnchor asChild>
        <div
          className={className}
          onMouseEnter={openNow}
          onMouseLeave={closeSoon}
          onFocusCapture={openNow}
          onBlurCapture={closeSoon}
        >
          {children}
        </div>
      </PopoverAnchor>
      <PopoverContent
        side="right"
        align="start"
        sideOffset={8}
        // Hover-card semantics: not a focus trap, and pointer-down outside
        // (e.g. clicking the row to open the env) must not be swallowed.
        onOpenAutoFocus={(event) => {
          event.preventDefault();
        }}
        onMouseEnter={openNow}
        onMouseLeave={closeSoon}
        className="w-72 p-0"
        role="dialog"
        aria-label={`${tenantName} / ${environmentName} details`}
      >
        <div className="border-b border-border px-3 py-2">
          <div className="flex items-center gap-1.5">
            <HoverCardTitle>
              {tenantName} / {environmentName}
            </HoverCardTitle>
            <EnvTypeBadge isLocal={isLocal} isHost={isHost} />
          </div>
        </div>
        <EnvHoverCardFields
          runtimeVersion={runtimeVersion}
          runtimeVersionLine={runtimeVersionLine}
          erunVersionSummary={erunVersionSummary}
          runtimeImageLineMismatch={runtimeImageLineMismatch}
          issue={issue}
          activityLabel={activityLabel}
          indicator={indicator}
          usage={usage}
          usageExcludesBuilds={usageExcludesBuilds}
          node={node}
          nodeIndicator={nodeIndicator}
        />
      </PopoverContent>
    </Popover>
  );
}

// EnvHoverCardFields is the card's body, split out from EnvHoverCard so that
// component stays the popover's open/close lifecycle and this one stays markup.
function EnvHoverCardFields({
  runtimeVersion,
  runtimeVersionLine,
  erunVersionSummary,
  runtimeImageLineMismatch,
  issue,
  activityLabel,
  indicator,
  usage,
  usageExcludesBuilds,
  node,
  nodeIndicator,
}: {
  runtimeVersion: string;
  runtimeVersionLine: UIRuntimeVersionLine | undefined;
  erunVersionSummary: ErunVersionSummary | null;
  runtimeImageLineMismatch: UIRuntimeImageLineMismatch | undefined;
  issue: WorkingIssueState;
  activityLabel: string;
  indicator: EnvironmentIndicator;
  usage: UIEnvironmentUsageSnapshot | undefined;
  usageExcludesBuilds: boolean;
  node: UIEnvironmentNodeSnapshot | undefined;
  nodeIndicator: EnvironmentNodeIndicator;
}): React.ReactElement {
  return (
    // Two zones, per the spacing hierarchy's level 3 (Sidebar.HoverCardRow.tsx):
    // stable identity above the hairline, live state below it, so a conditional
    // row here (Erun version, Line mismatch) changes only this zone's height.
    // tabular-nums lives here, once, rather than per row -- every digit in the
    // card is equal-width with no per-value decision left to make.
    <div className="tabular-nums">
      <dl className={`${HOVER_CARD_GRID_CLASS} px-3 pt-2.5 pb-2`}>
        <HoverCardRow label="Version">
          <RuntimeVersionState runtimeVersion={runtimeVersion} line={runtimeVersionLine} />
        </HoverCardRow>
        {erunVersionSummary && (
          <HoverCardRow label="Erun version">
            <ErunVersionState summary={erunVersionSummary} />
          </HoverCardRow>
        )}
        {runtimeImageLineMismatch && (
          <HoverCardRow label="Line mismatch">
            <LineMismatchWarning mismatch={runtimeImageLineMismatch} />
          </HoverCardRow>
        )}
        <HoverCardRow label="Working on">
          <WorkingOn issue={issue} />
        </HoverCardRow>
      </dl>
      <dl className={`${HOVER_CARD_GRID_CLASS} border-t border-border/60 px-3 pt-2 pb-2.5`}>
        <HoverCardRow label="Activity">
          <ActivityState activityLabel={activityLabel} indicator={indicator} />
        </HoverCardRow>
        <UsageRows usage={usage} excludesBuilds={usageExcludesBuilds} />
        {/* The Cloud node row is omitted entirely when there is no node: "this
            cluster is not power-managed by erun" explains an absence the
            operator cannot act on, and it was the longest row on the card. */}
        {node && (
          <HoverCardRow label="Cloud node">
            <NodeState node={node} nodeIndicator={nodeIndicator} />
          </HoverCardRow>
        )}
      </dl>
    </div>
  );
}

function Muted({ children }: { children: React.ReactNode }): React.ReactElement {
  return <HoverCardMuted>{children}</HoverCardMuted>;
}

// RuntimeVersionState names the release line beside the runtime-version
// number, the same convention `erun list` already uses (erun-cli's
// runtimeVersionLabel) -- a bare number reads as an erun version even when it
// names a tenant's own <tenant>-devops line.
function RuntimeVersionState({
  runtimeVersion,
  line,
}: {
  runtimeVersion: string;
  line: UIRuntimeVersionLine | undefined;
}): React.ReactElement {
  const summary = summarizeRuntimeVersionLine(runtimeVersion, line);
  if (!summary.hasVersion) {
    return <Muted>Not set</Muted>;
  }
  return (
    <div className={HOVER_CARD_VALUE_STACK_CLASS}>
      <span className={HOVER_CARD_TRUNCATE_CLASS} title={summary.version}>
        {summary.version}
      </span>
      {summary.caption && <span className={HOVER_CARD_CAPTION_CLASS}>{summary.caption}</span>}
    </div>
  );
}

// ErunVersionState is the erun version this environment's runtime chart
// carries, distinct from the runtime-version row above whenever the running
// image itself rides a tenant's own release line. Coincides with the runtime
// version whenever both are confirmed on erun's own line, in which case this
// says so rather than repeating an identical-looking number unexplained.
function ErunVersionState({ summary }: { summary: ErunVersionSummary }): React.ReactElement {
  if (!summary.known) {
    return <Muted>Undetermined — no chart recorded to confirm it</Muted>;
  }
  if (summary.sameAsRuntime) {
    return <Muted>Same as runtime version</Muted>;
  }
  return (
    <span className={HOVER_CARD_TRUNCATE_CLASS} title={summary.version}>
      {summary.version}
    </span>
  );
}

// LineMismatchWarning surfaces EnvConfig.RuntimeImageLineMismatch: the
// environment's recorded and last-observed runtime images name different
// release lines -- the case an operator most needs to see, since it means a
// redeploy would pull a different line than what is actually running.
function LineMismatchWarning({
  mismatch,
}: {
  mismatch: UIRuntimeImageLineMismatch;
}): React.ReactElement {
  return (
    <span className={`flex items-start gap-1.5 ${HOVER_CARD_ALERT_CLASS}`}>
      <TriangleAlert aria-hidden="true" className="mt-px size-3 shrink-0" />
      <span>
        Recorded {mismatch.recordedLine} line, last observed running {mismatch.observedLine} line —
        redeploy to realign.
      </span>
    </span>
  );
}

// ActivityState reports the desktop's own in-flight command when there is one,
// and otherwise the environment's condition — which now distinguishes an env
// that is busy (and with what), one that is merely reachable because someone
// opened it outside the desktop, and one nobody has opened at all.
function ActivityState({
  activityLabel,
  indicator,
}: {
  activityLabel: string;
  indicator: EnvironmentIndicator;
}): React.ReactElement {
  if (activityLabel) {
    return <span>{activityLabel}</span>;
  }
  if (indicator.dot === 'busy') {
    return <span>{indicator.activity}</span>;
  }
  return <Muted>{indicator.activity}</Muted>;
}

// NodeState names the machine the environment's cluster runs on and the power
// state it was last observed in. It renders whenever there IS a node, including
// for a running node the row itself stays silent about: "the node is fine, it
// is the environment that could not be determined" is the answer a blank row
// cannot give, and this is where it is available without new row vocabulary.
// The no-node case is not handled here -- the caller omits the row outright,
// because "nothing power-manages this cluster" is not a fact an operator acts
// on and was the longest line on the card.
function NodeState({
  node,
  nodeIndicator,
}: {
  node: UIEnvironmentNodeSnapshot;
  nodeIndicator: EnvironmentNodeIndicator;
}): React.ReactElement {
  const label = environmentNodeLabel(node);
  if (nodeIndicator.state === 'stopped') {
    return (
      <span className={HOVER_CARD_VALUE_STACK_CLASS}>
        <span className={HOVER_CARD_TRUNCATE_CLASS} title={label}>
          {label}
        </span>
        <span className={`flex items-center gap-1.5 ${HOVER_CARD_ALERT_CLASS}`}>
          <TriangleAlert aria-hidden="true" className="size-3 shrink-0" />
          Stopped — start it from the titlebar
        </span>
      </span>
    );
  }
  return (
    <span className={HOVER_CARD_VALUE_STACK_CLASS}>
      <span className={HOVER_CARD_TRUNCATE_CLASS} title={label}>
        {label}
      </span>
      <span className={HOVER_CARD_CAPTION_CLASS}>{nodeStateCaption(nodeIndicator.state)}</span>
    </span>
  );
}

function nodeStateCaption(state: EnvironmentNodeIndicator['state']): string {
  switch (state) {
    case 'running':
      return 'Running';
    case 'pending':
      return 'Starting';
    case 'stopped':
      return 'Stopped';
    case 'unknown':
      return 'State unknown — could not be checked';
  }
}

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
// The reading itself is scoped to the runtime container's own cgroup, which
// is never where a build runs -- every image build executes in the erun-dind
// sidecar instead, so this figure can read idle while that sidecar saturates
// the node. Reading the sidecar's own cgroup would not fix it either: its
// build containers run as cgroup siblings, not descendants, so nothing this
// card could read would ever account for them, and the one place that view
// is reachable is a host-wide path shared by every build-capable pod on the
// node -- not attributable to this environment alone. Qualifying the reading
// is the only honest option left, so `excludesBuilds` (environmentUsesDindSidecar
// in Sidebar.helpers.ts) makes the caption say so on every build-capable
// environment, not just the ones currently building.
function UsageRows({
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
        <Muted>{metrics.detail}</Muted>
      </HoverCardRow>
    );
  }
  const scopeCaveat = excludesBuilds ? ' — excludes builds' : '';
  return (
    <>
      <HoverCardRow label="CPU">
        <UsageMetric metric={metrics.cpu} stale={metrics.stale} />
      </HoverCardRow>
      <HoverCardRow label="Memory">
        <UsageMetric metric={metrics.memory} stale={metrics.stale} />
      </HoverCardRow>
      {/* The reading's age is its own row, under the metrics it qualifies --
          a caption spanning both metrics must not sit under only one of them. */}
      <HoverCardRow label="">
        <Muted>
          {metrics.stale ? 'Stale — as of' : 'As of'} {metrics.ageLabel} ago{scopeCaveat}
        </Muted>
      </HoverCardRow>
    </>
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

function WorkingOn({ issue }: { issue: WorkingIssueState }): React.ReactElement {
  if (issue.status === 'idle' || issue.status === 'loading') {
    return <Muted>Resolving…</Muted>;
  }
  if (issue.status === 'error') {
    return <Muted>Unavailable</Muted>;
  }
  const value = issue.value;
  if (!value.available) {
    return <Muted>{value.reason ?? 'Not available for this environment'}</Muted>;
  }
  if (!value.branch) {
    return <Muted>No branch checked out</Muted>;
  }
  const issueLine = value.issueNumber
    ? `#${String(value.issueNumber)}${value.issueTitle ? ` · ${value.issueTitle}` : ''}`
    : null;
  return (
    <div className={HOVER_CARD_VALUE_STACK_CLASS}>
      <span className={HOVER_CARD_TRUNCATE_CLASS} title={value.branch}>
        {value.branch}
      </span>
      {issueLine ? (
        <span className={HOVER_CARD_TRUNCATE_CLASS} title={issueLine}>
          {issueLine}
        </span>
      ) : null}
    </div>
  );
}

type WorkingIssueState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'loaded'; value: UIWorkingIssue }
  | { status: 'error' };

// useWorkingIssue re-resolves per card open (not once per row lifetime) so a
// remote env's "open it to see its in-pod work" answer flips to the real
// branch the moment the env opens; the prior value keeps rendering during a
// refetch to avoid a "Resolving…" flash over known data.
//
// The fetch is guarded by an in-flight ref, not the state status: adding the
// status to the effect deps would re-run on setState('loading'), whose
// cleanup cancels the just-started fetch and strands the card on "Resolving…".
// Selection is read through a ref so its per-render identity churn doesn't
// retrigger the effect.
function useWorkingIssue(selection: UISelection, open: boolean): WorkingIssueState {
  const [state, setState] = React.useState<WorkingIssueState>({ status: 'idle' });
  const selectionRef = React.useRef(selection);
  selectionRef.current = selection;
  const inFlight = React.useRef(false);
  const mounted = React.useRef(true);
  React.useEffect(
    () => () => {
      mounted.current = false;
    },
    [],
  );
  const { tenant, environment } = selection;
  React.useEffect(() => {
    if (!open || inFlight.current) {
      return;
    }
    // Result is guarded by mount, not by the card's open state: closing the
    // card mid-fetch must not drop the result.
    inFlight.current = true;
    setState((prev) => (prev.status === 'loaded' ? prev : { status: 'loading' }));
    void EnvironmentWorkingIssue(selectionRef.current)
      .then((value) => {
        if (mounted.current) {
          setState({ status: 'loaded', value: value });
        }
      })
      .catch(() => {
        if (mounted.current) {
          setState((prev) => (prev.status === 'loaded' ? prev : { status: 'error' }));
        }
      })
      .finally(() => {
        inFlight.current = false;
      });
  }, [open, tenant, environment]);
  return state;
}
