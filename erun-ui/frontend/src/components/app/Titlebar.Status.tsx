import {
  Button,
  cn,
  IconTooltip,
  Popover,
  PopoverContent,
  PopoverTrigger,
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from 'erun-kit';
import { AlertCircle, CheckCircle2, Copy, Info, LoaderCircle, X } from 'lucide-react';
import * as React from 'react';

import { useAppDispatch, useAppSelector } from '@/app/hooks';
import {
  copyTerminalOutput,
  dismissTerminalStatus,
  waitLongerForTerminalStatus,
} from '@/app/notificationThunks';
import type { AppState } from '@/app/state';
import { TitlebarMessageCenter } from '@/components/app/Titlebar.MessageCenter';

// Long error text can't be read or copied from a hover tooltip, so past
// this length the message escalates to a selectable popover the operator
// can copy into a bug report.
const LONG_STATUS_THRESHOLD = 160;

type TitlebarStatusKind = AppState['terminalStatusKind'];

// TitlebarStatusValue is the currently-running/just-finished CLI command's
// own status -- distinct from the classified message centre
// (Titlebar.MessageCenter.tsx), which now owns every notification-queue
// message. This pill is scoped to the terminal action just taken (piped
// commands like `erun init`/`erun deploy`, or a dedicated PTY's own result),
// never a global toast, so the two render side by side rather than
// competing for one slot.
interface TitlebarStatusValue {
  kind: TitlebarStatusKind;
  message: string;
  detail: string;
  busy: boolean;
  copyOutput: string;
  copyStatus: string;
  action: AppState['terminalStatusAction'];
}

const statusBorderClassNames: Record<TitlebarStatusKind, string> = {
  warning: 'border-[oklch(0.76_0.16_65)] text-foreground',
  error: 'border-destructive/60 text-foreground',
  info: 'border-border text-foreground',
};

const statusIconClassNames: Record<TitlebarStatusKind, string> = {
  warning: 'text-[oklch(0.58_0.15_65)]',
  error: 'text-destructive',
  info: 'text-muted-foreground',
};

export function TitlebarStatus(): React.ReactElement {
  const terminalStatus = useAppSelector((state) => state.terminalStatus);
  const status = computeTitlebarStatus(terminalStatus);
  return (
    <div className="flex min-w-0 max-w-full items-center gap-2 [--wails-draggable:no-drag]">
      {status && (
        // Cap width so the status pill never crowds the right-cluster buttons
        // on narrow viewports.
        <div
          className="pointer-events-none flex min-w-0 max-w-full"
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
            <StatusDismissAction />
          </div>
        </div>
      )}
      <TitlebarMessageCenter />
    </div>
  );
}

function computeTitlebarStatus(terminal: {
  terminalMessage: string;
  terminalStatusKind: AppState['terminalStatusKind'];
  terminalStatusDetail: string;
  terminalStatusAction: AppState['terminalStatusAction'];
  terminalBusy: boolean;
  terminalCopyOutput: string;
  terminalCopyStatus: string;
}): TitlebarStatusValue | null {
  if (terminal.terminalBusy && terminal.terminalMessage) {
    return null;
  }
  if (!terminal.terminalMessage) {
    return null;
  }
  return {
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

function StatusDismissAction(): React.ReactElement {
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
          dispatch(dismissTerminalStatus());
        }}
      >
        <X aria-hidden="true" />
      </Button>
    </IconTooltip>
  );
}
