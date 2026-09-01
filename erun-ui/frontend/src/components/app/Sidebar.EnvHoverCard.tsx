import { Popover, PopoverAnchor, PopoverContent } from 'erun-kit';
import { TriangleAlert } from 'lucide-react';
import * as React from 'react';

import { type EnvironmentNodeIndicator, environmentNodeLabel } from '@/app/environmentNodeState';
import { summarizeEnvironmentUsage } from '@/app/environmentUsageSummary';
import {
  type ErunVersionSummary,
  summarizeErunVersion,
  summarizeRuntimeVersionLine,
} from '@/app/environmentVersionLines';
import { useHoverCardOpenState } from '@/app/useHoverCardOpenState';
import type { EnvironmentIndicator } from '@/components/app/Sidebar.helpers';
import {
  HOVER_CARD_CAPTION_CLASS,
  HOVER_CARD_CAPTION_SIZE_CLASS,
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
        className="w-72 p-0 text-sm"
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
  node: UIEnvironmentNodeSnapshot | undefined;
  nodeIndicator: EnvironmentNodeIndicator;
}): React.ReactElement {
  return (
    <dl className="grid grid-cols-[auto_minmax(0,1fr)] items-baseline gap-x-3 gap-y-1.5 px-3 py-2.5">
      {/* wide: a version is the card's longest literal identifier, and the
          narrow value column shared with the label breaks it mid-token. */}
      <HoverCardRow label="Version" wide>
        <RuntimeVersionState runtimeVersion={runtimeVersion} line={runtimeVersionLine} />
      </HoverCardRow>
      {erunVersionSummary && (
        <HoverCardRow label="Erun version" wide>
          <ErunVersionState summary={erunVersionSummary} />
        </HoverCardRow>
      )}
      {runtimeImageLineMismatch && (
        <HoverCardRow label="Line mismatch" wide>
          <LineMismatchWarning mismatch={runtimeImageLineMismatch} />
        </HoverCardRow>
      )}
      <HoverCardRow label="Working on" wide>
        <WorkingOn issue={issue} />
      </HoverCardRow>
      <HoverCardRow label="Activity">
        <ActivityState activityLabel={activityLabel} indicator={indicator} />
      </HoverCardRow>
      <HoverCardRow label="Usage">
        <UsageState usage={usage} />
      </HoverCardRow>
      <HoverCardRow label="Cloud node">
        <NodeState node={node} nodeIndicator={nodeIndicator} />
      </HoverCardRow>
    </dl>
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
    <div className="grid gap-0.5">
      <span className="font-mono tabular-nums">{summary.version}</span>
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
  return <span className="font-mono tabular-nums">{summary.version}</span>;
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
    <span className="flex items-start gap-1.5 text-amber-700 dark:text-amber-400">
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
// state it was last observed in. It renders on EVERY hover, including for a
// running node the row itself stays silent about: "the node is fine, it is the
// environment that could not be determined" is the answer a blank row cannot
// give, and this is where it is available without new row vocabulary.
function NodeState({
  node,
  nodeIndicator,
}: {
  node: UIEnvironmentNodeSnapshot | undefined;
  nodeIndicator: EnvironmentNodeIndicator;
}): React.ReactElement {
  if (!node) {
    // A definite answer, not an unread one: nothing erun power-manages backs
    // this environment, so there is no node to be up or down.
    return <Muted>No cloud node — this cluster is not power-managed by erun</Muted>;
  }
  const label = environmentNodeLabel(node);
  if (nodeIndicator.state === 'stopped') {
    return (
      <span className="grid gap-0.5">
        <span className="font-mono">{label}</span>
        <span className="flex items-center gap-1.5 text-amber-700 dark:text-amber-400">
          <TriangleAlert aria-hidden="true" className="size-3 shrink-0" />
          Stopped — start it from the titlebar
        </span>
      </span>
    );
  }
  return (
    <span className="grid gap-0.5">
      <span className="font-mono">{label}</span>
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

// UsageState renders the environment-usage sweep's cached reading
// (environment_usage.go): a comparable CPU/memory figure with its age, a
// stated reason when there is nothing measurable (never a bare 0%, which
// would read as idle-and-healthy rather than "unmeasured"), and a visible
// staleness flag when the reading has outlived the sweep interval that
// produced it — an unlabelled stale number is worse than none.
function UsageState({
  usage,
}: {
  usage: UIEnvironmentUsageSnapshot | undefined;
}): React.ReactElement {
  const summary = summarizeEnvironmentUsage(usage, Date.now());
  if (!summary.hasReading) {
    return <Muted>{summary.detail}</Muted>;
  }
  if (!summary.headline) {
    return (
      <span className="flex items-start gap-1.5 text-amber-700 dark:text-amber-400">
        <TriangleAlert aria-hidden="true" className="mt-px size-3 shrink-0" />
        <span>{summary.detail}</span>
      </span>
    );
  }
  return (
    <span className="grid gap-0.5">
      <span className="tabular-nums">{summary.headline}</span>
      <span
        className={
          summary.stale
            ? `flex items-center gap-1 ${HOVER_CARD_CAPTION_SIZE_CLASS} text-amber-700 dark:text-amber-400`
            : HOVER_CARD_CAPTION_CLASS
        }
      >
        {summary.stale && <TriangleAlert aria-hidden="true" className="size-3 shrink-0" />}
        {summary.stale ? `Stale — as of ${summary.ageLabel} ago` : `As of ${summary.ageLabel} ago`}
      </span>
    </span>
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
  return (
    <div className="grid gap-0.5">
      <span className="font-mono">{value.branch}</span>
      {value.issueNumber ? (
        <span>
          #{value.issueNumber}
          {value.issueTitle ? ` · ${value.issueTitle}` : ''}
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
