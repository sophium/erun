import { Popover, PopoverAnchor, PopoverContent } from 'erun-kit';
import { TriangleAlert } from 'lucide-react';
import * as React from 'react';

import { type EnvironmentNodeIndicator, environmentNodeLabel } from '@/app/environmentNodeState';
import {
  type ErunVersionSummary,
  summarizeErunVersion,
  summarizeRuntimeVersionLine,
} from '@/app/environmentVersionLines';
import { useHoverCardOpenState } from '@/app/useHoverCardOpenState';
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
import { UsageRows } from '@/components/app/Sidebar.UsageRows';
import type { UISelection, UIWorkingIssue } from '@/types';
import type { UIEnvironmentNodeSnapshot } from '@/uiEnvironmentNodeTypes';
import type { UIEnvironmentUsageSnapshot } from '@/uiEnvironmentUsageTypes';
import type {
  UIErunVersion,
  UIRuntimeImageLineMismatch,
  UIRuntimeVersionLine,
} from '@/uiRuntimeVersionLineTypes';

import { EnvironmentWorkingIssue } from '../../../wailsjs/go/main/App';

// EnvTypeBadge: Host, Local, or nothing for remote/runtime.
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

// EnvHoverCard is a Popover (a multi-field card, see erun-ui/AGENTS.md) that keeps the row's click and edit.
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
  const { open, setOpen, hoverEnter, hoverLeave, focusEnter, focusLeave, refuseDismissWhileHeld } =
    useHoverCardOpenState();

  const issue = useWorkingIssue(selection, open);
  const erunVersionSummary = summarizeErunVersion(runtimeVersion.trim() !== '', erunVersion);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverAnchor asChild>
        {/* Pointer and focus hold `open` separately; see useHoverCardOpenState. */}
        <div
          className={className}
          onMouseEnter={hoverEnter}
          onMouseLeave={hoverLeave}
          onFocusCapture={focusEnter}
          onBlurCapture={focusLeave}
        >
          {children}
        </div>
      </PopoverAnchor>
      <PopoverContent
        side="right"
        align="start"
        sideOffset={8}
        // Not a focus trap, and outside pointer-down must reach the row.
        onOpenAutoFocus={(event) => {
          event.preventDefault();
        }}
        // Must not outrank a holder still holding the card.
        onFocusOutside={refuseDismissWhileHeld}
        onMouseEnter={hoverEnter}
        onMouseLeave={hoverLeave}
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
    // Identity above the hairline, live state below, so conditional rows only resize their zone.
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
        {/* Omitted when there is no node: nothing actionable to say. */}
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

// RuntimeVersionState names the release line, as `erun list` does.
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

// ErunVersionState is the runtime chart's erun version, which differs when the image rides a tenant line.
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

// LineMismatchWarning: a redeploy would pull a different line than what runs.
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

// ActivityState prefers the desktop's in-flight command over the env's reported condition.
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

// NodeState renders whenever a node exists, even a running one; the caller omits the no-node row.
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

// useWorkingIssue re-resolves per open, keeping the prior value during a refetch.
// An in-flight ref, not status, guards the fetch: status in deps would cancel it. Selection is read via a ref.
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
    // Guarded by mount, not open state, so closing mid-fetch keeps the result.
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
