import { AlertCircle, CheckCircle2, Copy, Info, LoaderCircle, X } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  copyTerminalOutput,
  dismissNotification,
  dismissTerminalStatus,
  waitLongerForTerminalStatus,
} from '@/app/notificationThunks';
import type { AppState } from '@/app/state';
import type { AppDispatch } from '@/app/store';
import { IconTooltip } from '@/components/app/IconTooltip';
import { Button } from '@/components/ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';

// Long error text (AWS/IAM/Helm/etc. messages) routinely runs over the
// titlebar pill width. Anything past this threshold escalates the trigger
// from a hover tooltip to a click-to-open popover with selectable content
// — that's the only path that lets the user copy/select the error to
// paste into a bug report (Nielsen #9: recovery from errors).
const LONG_STATUS_THRESHOLD = 160;

type TitlebarStatusKind =
  | NonNullable<AppState['notification']>['kind']
  | AppState['terminalStatusKind'];

interface TitlebarStatusValue {
  source: 'notification' | 'terminal';
  kind: TitlebarStatusKind;
  message: string;
  detail: string;
  busy: boolean;
  copyOutput: string;
  copyStatus: string;
  action: AppState['terminalStatusAction'];
}

const statusBorderClassNames: Record<TitlebarStatusKind, string> = {
  success: 'border-[oklch(0.72_0.12_150)] text-foreground',
  warning: 'border-[oklch(0.76_0.16_65)] text-foreground',
  error: 'border-destructive/60 text-foreground',
  info: 'border-border text-foreground',
};

const statusIconClassNames: Record<TitlebarStatusKind, string> = {
  success: 'text-[oklch(0.52_0.15_150)]',
  warning: 'text-[oklch(0.58_0.15_65)]',
  error: 'text-destructive',
  info: 'text-muted-foreground',
};

export function TitlebarStatus(): React.ReactElement | null {
  const notification = useAppSelector((state) => state.notification.notification);
  const terminalStatus = useAppSelector((state) => state.terminalStatus);
  const status = computeTitlebarStatus(notification, terminalStatus);
  if (!status) {
    return null;
  }

  // Width: cap so the status pill never crowds the right-cluster buttons
  // even on narrow viewports. Long messages still escalate to a popover
  // via StatusMessagePopover when they exceed LONG_STATUS_THRESHOLD.
  return (
    <div
      className="pointer-events-none flex min-w-0 max-w-full [--wails-draggable:no-drag]"
      role={status.kind === 'error' ? 'alert' : 'status'}
      aria-live={status.kind === 'error' ? 'assertive' : 'polite'}
    >
      <div
        className={cn(
          'pointer-events-auto flex h-8 min-w-0 max-w-full items-center gap-2 rounded-md border bg-background px-2.5 text-[13px] leading-none shadow-sm',
          statusBorderClassNames[status.kind],
        )}
      >
        <StatusIcon status={status} />
        <StatusMessage status={status} />
        {status.action === 'wait-longer' && <StatusWaitAction />}
        {status.copyOutput && <StatusCopyAction status={status} />}
        <StatusDismissAction status={status} />
      </div>
    </div>
  );
}

function computeTitlebarStatus(
  notification: AppState['notification'],
  terminal: {
    terminalMessage: string;
    terminalStatusKind: AppState['terminalStatusKind'];
    terminalStatusDetail: string;
    terminalStatusAction: AppState['terminalStatusAction'];
    terminalBusy: boolean;
    terminalCopyOutput: string;
    terminalCopyStatus: string;
  },
): TitlebarStatusValue | null {
  if (notification) {
    return {
      ...notification,
      source: 'notification',
      detail: '',
      busy: false,
      copyOutput: '',
      copyStatus: '',
      action: '',
    };
  }
  if (terminal.terminalBusy && terminal.terminalMessage) {
    return null;
  }
  if (!terminal.terminalMessage) {
    return null;
  }
  return {
    source: 'terminal',
    kind: terminal.terminalStatusKind,
    message: terminal.terminalMessage,
    detail: terminal.terminalStatusDetail,
    busy: terminal.terminalBusy,
    copyOutput: terminal.terminalCopyOutput,
    copyStatus: terminal.terminalCopyStatus,
    action: terminal.terminalStatusAction,
  };
}

function StatusIcon({ status }: { status: TitlebarStatusValue }): React.ReactElement {
  const NotificationIcon = statusIcon(status);
  return (
    <NotificationIcon
      className={cn(
        'size-4 flex-none',
        status.busy && 'animate-spin text-muted-foreground',
        statusIconClassNames[status.kind],
      )}
      aria-hidden="true"
    />
  );
}

function statusIcon(status: TitlebarStatusValue): typeof LoaderCircle {
  if (status.busy) {
    return LoaderCircle;
  }
  if (status.kind === 'success') {
    return CheckCircle2;
  }
  if (status.kind === 'warning' || status.kind === 'error') {
    return AlertCircle;
  }
  return Info;
}

function StatusMessage({ status }: { status: TitlebarStatusValue }): React.ReactElement {
  const fullText = status.detail ? `${status.message}. ${status.detail}` : status.message;
  if (fullText.length > LONG_STATUS_THRESHOLD) {
    return <StatusMessagePopover status={status} fullText={fullText} />;
  }
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          className="min-w-0 cursor-default truncate rounded-sm border-0 bg-transparent p-0 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
          aria-label={fullText}
        >
          {status.message}
          {status.detail && <span className="text-muted-foreground"> - {status.detail}</span>}
        </button>
      </TooltipTrigger>
      <TooltipContent side="bottom" className="max-w-[360px] whitespace-normal text-left leading-5">
        {fullText}
      </TooltipContent>
    </Tooltip>
  );
}

function StatusMessagePopover({
  status,
  fullText,
}: {
  status: TitlebarStatusValue;
  fullText: string;
}): React.ReactElement {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className="min-w-0 truncate rounded-sm border-0 bg-transparent p-0 text-left outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring"
          aria-label={`${fullText} (click to read full message)`}
          data-testid="titlebar-status-message"
        >
          {status.message}
          {status.detail && <span className="text-muted-foreground"> - {status.detail}</span>}
        </button>
      </PopoverTrigger>
      <PopoverContent
        side="bottom"
        align="start"
        className="w-[480px] max-w-[calc(100vw-2rem)] space-y-2 p-3"
      >
        <p
          className="max-h-[40vh] overflow-auto select-text text-left text-[13px] leading-5 whitespace-pre-wrap"
          data-testid="titlebar-status-full-text"
        >
          {fullText}
        </p>
      </PopoverContent>
    </Popover>
  );
}

function StatusWaitAction(): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <Button
      className="h-6 flex-none rounded-md px-2 text-[12px] text-foreground hover:bg-accent hover:text-accent-foreground"
      type="button"
      variant="ghost"
      size="xs"
      onClick={() => {
        void dispatch(waitLongerForTerminalStatus());
      }}
    >
      Wait longer
    </Button>
  );
}

function StatusCopyAction({ status }: { status: TitlebarStatusValue }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <IconTooltip label="Copy terminal output">
      <Button
        className="h-6 flex-none gap-1 rounded-md px-2 text-[12px] text-foreground hover:bg-accent hover:text-accent-foreground [&_svg]:size-3.5"
        type="button"
        variant="ghost"
        size="xs"
        onClick={() => {
          void dispatch(copyTerminalOutput());
        }}
      >
        {status.copyStatus === 'Copied' ? (
          <CheckCircle2 aria-hidden="true" />
        ) : (
          <Copy aria-hidden="true" />
        )}
        {status.copyStatus || 'Copy output'}
      </Button>
    </IconTooltip>
  );
}

function StatusDismissAction({ status }: { status: TitlebarStatusValue }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <IconTooltip label="Dismiss status">
      <Button
        className="-mr-1 size-6 flex-none text-muted-foreground hover:bg-accent hover:text-accent-foreground [&_svg]:size-3.5"
        type="button"
        variant="ghost"
        size="icon-xs"
        aria-label="Dismiss status"
        onClick={() => {
          dismissTitlebarStatus(dispatch, status);
        }}
      >
        <X />
      </Button>
    </IconTooltip>
  );
}

function dismissTitlebarStatus(dispatch: AppDispatch, status: TitlebarStatusValue): void {
  if (status.source === 'notification') {
    dispatch(dismissNotification());
    return;
  }
  dispatch(dismissTerminalStatus());
}
