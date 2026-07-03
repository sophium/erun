import * as React from 'react';

import { Popover, PopoverAnchor, PopoverContent } from '@/components/ui/popover';
import type { UISelection, UIWorkingIssue } from '@/types';

import { EnvironmentWorkingIssue } from '../../../wailsjs/go/main/App';

// EnvHoverCard shows an env row's details in a Popover rather than a tooltip
// (a multi-field card doesn't belong in a tooltip; see erun-ui/AGENTS.md),
// without swallowing the row's own click-to-open and edit affordances.
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

// ActivityState distinguishes a never-opened env ("Not open") from an
// open-but-quiet one ("Idle") — a never-opened env has no pod to be idle.
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
