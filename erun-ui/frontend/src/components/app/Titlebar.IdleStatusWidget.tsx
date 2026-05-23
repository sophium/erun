import { LoaderCircle, Lock, Play, Power, Unlock } from 'lucide-react';
import * as React from 'react';

import {
  useDisableCloudContextApiStopMutation,
  useEnableCloudContextApiStopMutation,
  useGetCloudContextApiStopQuery,
} from '@/app/api/cloudApi';
import { toggleIdleCloudContext } from '@/app/globalConfigThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { displayableIdleStatus } from '@/app/idleStatusEligibility';
import { IconTooltip } from '@/components/app/IconTooltip';
import {
  idleCloudAction,
  type IdleStatus,
  idleStatusAccessibleLabel,
  idleStatusBadge,
  idleStatusTooltipLines,
} from '@/components/app/Titlebar.helpers';
import { Button } from '@/components/ui/button';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';

// useIdleWidgetState pulls the redux selectors and derives the
// render-time view of the titlebar. Extracting it keeps the
// IdleStatusWidget component itself thin enough to read top-down and
// shifts the boolean fan-out into a single helper. Returns null when
// the widget should render nothing.
function useIdleWidgetState(): {
  idleStatus: IdleStatus | null;
  idleBadge: { label: string; className: string } | null;
  idleAction: ReturnType<typeof idleCloudAction>;
  cloudContextName: string;
} | null {
  const rawIdleStatus = useAppSelector((state) => state.idle.idleStatus);
  const idleCloudContextBusy = useAppSelector((state) => state.idle.idleCloudContextBusy);
  const idleStatus = displayableIdleStatus(rawIdleStatus);
  const idleAction = rawIdleStatus ? idleCloudAction(rawIdleStatus, idleCloudContextBusy) : null;
  const cloudContextName = rawIdleStatus?.cloudContextName?.trim() ?? '';
  if (!idleStatus && !idleAction) {
    return null;
  }
  const idleBadge = idleStatus ? idleStatusBadge(idleStatus) : null;
  return { idleStatus, idleBadge, idleAction, cloudContextName };
}

export function IdleStatusWidget(): React.ReactElement | null {
  const state = useIdleWidgetState();
  if (!state) {
    return null;
  }
  const { idleStatus, idleBadge, idleAction, cloudContextName } = state;
  const hasAction = Boolean(idleAction);
  const hasFollowOn = Boolean(cloudContextName);
  return (
    <div
      className={cn(
        'absolute top-3 right-[168px] z-[1] flex h-7 items-center rounded-md border bg-background [--wails-draggable:no-drag] max-[980px]:right-[146px]',
        idleBadge?.className,
      )}
    >
      {idleStatus && idleBadge ? (
        <IdleStatusBadge idleStatus={idleStatus} idleBadge={idleBadge} hasAction={hasAction} />
      ) : null}
      {idleAction ? (
        <IdleStatusAction
          idleAction={idleAction}
          hasBadge={Boolean(idleStatus)}
          hasFollowOn={hasFollowOn}
        />
      ) : null}
      {cloudContextName ? <StopProtectionToggle contextName={cloudContextName} /> : null}
    </div>
  );
}

function IdleStatusBadge({
  idleStatus,
  idleBadge,
  hasAction,
}: {
  idleStatus: IdleStatus;
  idleBadge: { label: string; className: string };
  hasAction: boolean;
}): React.ReactElement {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          className={cn(
            'flex h-full min-w-[64px] cursor-default items-center justify-center rounded-l-md border-0 bg-transparent px-2 font-mono text-[12px] leading-none outline-none hover:bg-accent hover:text-accent-foreground focus-visible:ring-2 focus-visible:ring-ring',
            hasAction && 'border-r',
            idleBadge.className,
          )}
          aria-label={idleStatusAccessibleLabel(idleStatus)}
        >
          {idleBadge.label}
        </button>
      </TooltipTrigger>
      <TooltipContent
        side="bottom"
        align="end"
        className="max-w-[360px] whitespace-normal text-left leading-5"
      >
        <div className="space-y-1">
          {idleStatusTooltipLines(idleStatus).map((line, index) => (
            <div key={`${String(index)}-${line}`} className={tooltipLineClassName(line)}>
              {line.replace(/^ +/, '')}
            </div>
          ))}
        </div>
      </TooltipContent>
    </Tooltip>
  );
}

// idleStatusActionShape returns the icon + rounding/border that depend
// on whether the action button has a badge to its left and/or another
// button to its right. Extracted so IdleStatusAction itself stays
// under the eslint complexity ceiling — the layout permutations are
// real and worth naming, but they belong in a lookup, not the JSX.
function idleStatusActionShape(
  busy: boolean,
  action: 'start' | 'stop',
  hasBadge: boolean,
  hasFollowOn: boolean,
): { Icon: typeof LoaderCircle; shape: string } {
  const Icon = busy ? LoaderCircle : action === 'start' ? Play : Power;
  const key = `${hasBadge ? 'b' : ''}${hasFollowOn ? 'f' : ''}`;
  const shapes: Record<string, string> = {
    '': 'rounded-md',
    b: 'rounded-l-none rounded-r-md',
    f: 'rounded-l-md rounded-r-none border-r',
    bf: 'rounded-none border-r',
  };
  return { Icon, shape: shapes[key] ?? 'rounded-md' };
}

function IdleStatusAction({
  idleAction,
  hasBadge,
  hasFollowOn,
}: {
  idleAction: { action: 'start' | 'stop'; label: string; busy: boolean };
  hasBadge: boolean;
  hasFollowOn: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const { Icon, shape } = idleStatusActionShape(
    idleAction.busy,
    idleAction.action,
    hasBadge,
    hasFollowOn,
  );
  return (
    <IconTooltip label={idleAction.label}>
      <Button
        className={cn(
          'h-full w-7 border-0 bg-transparent text-muted-foreground hover:bg-accent hover:text-accent-foreground disabled:pointer-events-none disabled:opacity-60 [&_svg]:size-3.5',
          shape,
        )}
        type="button"
        variant="ghost"
        size="icon"
        aria-label={idleAction.label}
        disabled={idleAction.busy}
        onClick={() => {
          void dispatch(toggleIdleCloudContext());
        }}
      >
        <Icon className={cn(idleAction.busy && 'animate-spin')} aria-hidden="true" />
      </Button>
    </IconTooltip>
  );
}

// confirmAndDisableApiStop wraps the lock confirmation so the
// component itself doesn't carry the prompt's branches in its
// complexity budget. Locking is the destructive direction (also
// blocks the user's own Stop button until cleared), so the
// confirmation lives in the lock direction only — unlocking is a
// one-click restore.
function confirmAndDisableApiStop(contextName: string, run: (name: string) => unknown): void {
  const confirmed = window.confirm(
    `Lock ${contextName}?\n\nWhile locked, AWS rejects every ec2:StopInstances call. The in-pod idle monitor, your own Stop button, and any external script will all fail to stop the instance until you unlock it.\n\nUse this to keep an unhealthy env up while you repair it. Don't forget to unlock when done.`,
  );
  if (confirmed) {
    void run(contextName);
  }
}

// useStopProtectionState centralises the redux-toolkit-query plumbing
// so StopProtectionToggle stays under the eslint complexity ceiling.
// Returns the derived view the icon button renders against.
function useStopProtectionState(contextName: string): {
  Icon: typeof LoaderCircle;
  label: string;
  amber: boolean;
  pressed: boolean;
  busy: boolean;
  onClick: () => void;
} {
  const { data, isFetching } = useGetCloudContextApiStopQuery(contextName, {
    refetchOnMountOrArgChange: true,
  });
  const [disableApiStop, disableMeta] = useDisableCloudContextApiStopMutation();
  const [enableApiStop, enableMeta] = useEnableCloudContextApiStopMutation();
  const known = Boolean(data?.stopProtectionKnown);
  const locked = Boolean(data?.stopProtection);
  const busy = isFetching || disableMeta.isLoading || enableMeta.isLoading;
  const Icon = busy ? LoaderCircle : locked ? Lock : Unlock;
  const label = locked
    ? `Unlock ${contextName}: re-enable auto-stop and manual Stop`
    : `Lock ${contextName}: block every stop until you unlock — including the in-pod idle monitor and your own Stop button`;
  const onClick = () => {
    if (busy) {
      return;
    }
    if (locked) {
      void enableApiStop(contextName);
      return;
    }
    confirmAndDisableApiStop(contextName, disableApiStop);
  };
  return { Icon, label, amber: known && locked, pressed: known && locked, busy, onClick };
}

// StopProtectionToggle renders the lock/unlock icon that flips the AWS
// DisableApiStop attribute for the selected cloud context. Locking is
// the recovery lever the user reaches for when the in-pod idle monitor
// is racing against a repair (e.g. erun-mcp is in ImagePullBackOff so
// every activity marker is idle and the monitor decides to stop the
// instance every 10 min). While locked, every ec2:StopInstances call
// returns OperationNotPermitted — including the user's own desktop
// Stop button — so the lock icon also blocks the adjacent Power
// button's effect until the user clears it.
function StopProtectionToggle({ contextName }: { contextName: string }): React.ReactElement {
  const { Icon, label, amber, pressed, busy, onClick } = useStopProtectionState(contextName);
  return (
    <IconTooltip label={label}>
      <Button
        className={cn(
          'h-full w-7 rounded-l-none rounded-r-md border-0 bg-transparent text-muted-foreground hover:bg-accent hover:text-accent-foreground disabled:pointer-events-none disabled:opacity-60 [&_svg]:size-3.5',
          amber && 'text-amber-600',
        )}
        type="button"
        variant="ghost"
        size="icon"
        aria-label={label}
        aria-pressed={pressed}
        disabled={busy}
        onClick={onClick}
      >
        <Icon className={cn(busy && 'animate-spin')} aria-hidden="true" />
      </Button>
    </IconTooltip>
  );
}

// tooltipLineClassName mirrors the leading-space convention used by
// idleStatusTooltipLines: "- " is a top-level entry (Active markers, etc.),
// "  - " is a nested per-client detail line under its marker. HTML collapses
// the leading spaces in the rendered output, so we encode the nesting as a
// Tailwind padding class instead.
function tooltipLineClassName(line: string): string | undefined {
  if (line.startsWith('  - ')) {
    return 'pl-6';
  }
  if (line.startsWith('- ')) {
    return 'pl-2';
  }
  return undefined;
}
