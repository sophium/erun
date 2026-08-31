import { Popover, PopoverAnchor, PopoverContent } from 'erun-kit';
import { TriangleAlert } from 'lucide-react';
import * as React from 'react';

import { summarizeEnvironmentUsage } from '@/app/environmentUsageSummary';
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
import type { UIEnvironmentUsageSnapshot } from '@/uiEnvironmentUsageTypes';

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
  activityLabel,
  indicator,
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
  activityLabel: string;
  indicator: EnvironmentIndicator;
  usage: UIEnvironmentUsageSnapshot | undefined;
  children: React.ReactNode;
}): React.ReactElement {
  const [open, setOpen] = React.useState(false);
  const closeTimer = React.useRef(0);
  const openNow = React.useCallback(() => {
    window.clearTimeout(closeTimer.current);
    setOpen(true);
  }, []);
  const closeSoon = React.useCallback(() => {
    window.clearTimeout(closeTimer.current);
    // Small grace so moving the pointer from the row onto the card doesn't close it.
    closeTimer.current = window.setTimeout(() => {
      setOpen(false);
    }, 120);
  }, []);
  React.useEffect(
    () => () => {
      window.clearTimeout(closeTimer.current);
    },
    [],
  );

  const issue = useWorkingIssue(selection, open);

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
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] items-baseline gap-x-3 gap-y-1.5 px-3 py-2.5">
          <HoverCardRow label="Version">
            {runtimeVersion ? (
              <span className="font-mono tabular-nums">{runtimeVersion}</span>
            ) : (
              <Muted>Not set</Muted>
            )}
          </HoverCardRow>
          <HoverCardRow label="Working on">
            <WorkingOn issue={issue} />
          </HoverCardRow>
          <HoverCardRow label="Activity">
            <ActivityState activityLabel={activityLabel} indicator={indicator} />
          </HoverCardRow>
          <HoverCardRow label="Usage">
            <UsageState usage={usage} />
          </HoverCardRow>
        </dl>
      </PopoverContent>
    </Popover>
  );
}

function Muted({ children }: { children: React.ReactNode }): React.ReactElement {
  return <HoverCardMuted>{children}</HoverCardMuted>;
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
