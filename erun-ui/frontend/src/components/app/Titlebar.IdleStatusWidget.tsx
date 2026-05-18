import { LoaderCircle, Play, Power } from 'lucide-react';
import * as React from 'react';

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

export function IdleStatusWidget(): React.ReactElement | null {
  const rawIdleStatus = useAppSelector((state) => state.idle.idleStatus);
  const idleCloudContextBusy = useAppSelector((state) => state.idle.idleCloudContextBusy);
  const idleStatus = displayableIdleStatus(rawIdleStatus);
  const idleAction = rawIdleStatus ? idleCloudAction(rawIdleStatus, idleCloudContextBusy) : null;
  if (!idleStatus && !idleAction) {
    return null;
  }
  const idleBadge = idleStatus ? idleStatusBadge(idleStatus) : null;

  return (
    <div
      className={cn(
        'absolute top-3 right-[168px] z-[1] flex h-7 items-center rounded-md border bg-background [--wails-draggable:no-drag] max-[980px]:right-[146px]',
        idleBadge?.className,
      )}
    >
      {idleStatus && idleBadge && (
        <IdleStatusBadge
          idleStatus={idleStatus}
          idleBadge={idleBadge}
          hasAction={Boolean(idleAction)}
        />
      )}
      {idleAction && <IdleStatusAction idleAction={idleAction} hasBadge={Boolean(idleStatus)} />}
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
            <div
              key={`${String(index)}-${line}`}
              className={line.startsWith('- ') ? 'pl-2' : undefined}
            >
              {line}
            </div>
          ))}
        </div>
      </TooltipContent>
    </Tooltip>
  );
}

function IdleStatusAction({
  idleAction,
  hasBadge,
}: {
  idleAction: { action: 'start' | 'stop'; label: string; busy: boolean };
  hasBadge: boolean;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  const IdleActionIcon = idleAction.busy
    ? LoaderCircle
    : idleAction.action === 'start'
      ? Play
      : Power;
  return (
    <IconTooltip label={idleAction.label}>
      <Button
        className={cn(
          'h-full w-7 border-0 bg-transparent text-muted-foreground hover:bg-accent hover:text-accent-foreground disabled:pointer-events-none disabled:opacity-60 [&_svg]:size-3.5',
          hasBadge ? 'rounded-l-none rounded-r-md' : 'rounded-md',
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
        <IdleActionIcon className={cn(idleAction.busy && 'animate-spin')} aria-hidden="true" />
      </Button>
    </IconTooltip>
  );
}
