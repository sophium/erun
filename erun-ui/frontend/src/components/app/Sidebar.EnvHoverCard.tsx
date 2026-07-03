import * as React from 'react';

import { Popover, PopoverAnchor, PopoverContent } from '@/components/ui/popover';
import type { UISelection, UIWorkingIssue } from '@/types';

import { EnvironmentWorkingIssue } from '../../../wailsjs/go/main/App';

// EnvHoverCard wraps a sidebar environment row and shows a richer hover card:
// the env's runtime version, the issue it's working on (current
// branch + linked issue title), and its current activity. It replaces the
// plain tenant/env tooltip — a multi-field card belongs in a Popover, not a
// tooltip (erun-ui/AGENTS.md). The card opens on hover or keyboard focus of
// the row and is positioned beside it via PopoverAnchor, so the row's own
// click handler (open env) and edit button stay intact.
//
// The working-issue section is resolved lazily on first open via the
// EnvironmentWorkingIssue binding (git + gh, cached backend-side) so the
// branch/title lookup never runs until the user actually hovers.
export function EnvHoverCard({
  className,
  tenantName,
  environmentName,
  selection,
  isLocal,
  runtimeVersion,
  activityLabel,
  isOpen,
  envState,
  children,
}: {
  className?: string;
  tenantName: string;
  environmentName: string;
  selection: UISelection;
  isLocal: boolean;
  runtimeVersion: string;
  activityLabel: string;
  isOpen: boolean;
  envState: string;
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
    // Small grace so moving the pointer from the row onto the card doesn't
    // close it (the card has the same enter/leave handlers).
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
            <span className="min-w-0 truncate font-medium">
              {tenantName} / {environmentName}
            </span>
            {isLocal && (
              <span className="flex-none rounded-[calc(var(--radius)-4px)] border border-border px-1 py-px text-[10px] font-medium uppercase leading-none tracking-wide text-muted-foreground">
                Local
              </span>
            )}
          </div>
        </div>
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5 px-3 py-2.5">
          <HoverRow label="Version">
            {runtimeVersion ? (
              <span className="font-mono text-[12px]">{runtimeVersion}</span>
            ) : (
              <Muted>Not set</Muted>
            )}
          </HoverRow>
          <HoverRow label="Working on">
            <WorkingOn issue={issue} />
          </HoverRow>
          <HoverRow label="Activity">
            <ActivityState activityLabel={activityLabel} isOpen={isOpen} envState={envState} />
          </HoverRow>
        </dl>
      </PopoverContent>
    </Popover>
  );
}

function HoverRow({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}): React.ReactElement {
  return (
    <>
      <dt className="text-[12px] text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words text-foreground">{children}</dd>
    </>
  );
}

function Muted({ children }: { children: React.ReactNode }): React.ReactElement {
  return <span className="text-muted-foreground">{children}</span>;
}

// ActivityState renders the card's Activity row from the env's real state:
// a desktop in-flight operation wins, then the env-status flag
// (stopped / failed), then open-and-quiet ("Idle"), and a never-opened env
// says so instead of claiming "Idle" — there is no pod to be idle.
function ActivityState({
  activityLabel,
  isOpen,
  envState,
}: {
  activityLabel: string;
  isOpen: boolean;
  envState: string;
}): React.ReactElement {
  if (activityLabel) {
    return <span>{activityLabel}</span>;
  }
  if (envState === 'stopped') {
    return <Muted>Stopped — start it from the titlebar</Muted>;
  }
  if (envState === 'failed') {
    return <Muted>Deploy failed — recover from Activities</Muted>;
  }
  if (!isOpen) {
    return <Muted>Not open</Muted>;
  }
  return <Muted>Idle</Muted>;
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
      <span className="font-mono text-[12px]">{value.branch}</span>
      {value.issueNumber ? (
        <span className="text-[12px]">
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

// useWorkingIssue resolves the env's working issue lazily, on each card
// open. Re-resolving per open (instead of once per row lifetime) is what
// lets a remote env's answer flip from "open this environment to see its
// in-pod work" to the actual in-pod branch as soon as the env opens; the
// backend caches resolved work for a short TTL, so repeat hovers
// stay cheap. While a refetch is in flight the previous value keeps
// rendering — no "Resolving…" flash over known data.
//
// The fetch is guarded by an in-flight ref rather than the state status:
// putting the status in the effect deps would re-run the effect when
// setState('loading') lands, whose cleanup cancels the just-started fetch —
// leaving the card stuck on "Resolving…". The selection is read through a
// ref so its per-render identity churn doesn't retrigger the effect; only
// the open flag and env keys are deps.
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
