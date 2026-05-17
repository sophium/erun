import {
  AlertCircle,
  Blocks,
  CheckCircle2,
  Code2,
  Copy,
  Info,
  ListTree,
  LoaderCircle,
  PanelLeftClose,
  PanelLeftOpen,
  PanelRightClose,
  PanelRightOpen,
  Play,
  Power,
  X,
} from 'lucide-react';
import * as React from 'react';

import { toggleIdleCloudContext } from '@/app/globalConfigThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { displayableIdleStatus } from '@/app/idleStatusEligibility';
import { setFilesOpen, titlebarDoubleClick, toggleReview, toggleSidebar } from '@/app/layoutThunks';
import {
  copyTerminalOutput,
  dismissNotification,
  dismissTerminalStatus,
  waitLongerForTerminalStatus,
} from '@/app/notificationThunks';
import { openIDE } from '@/app/sessionThunks';
import type { AppState } from '@/app/state';
import type { AppDispatch } from '@/app/store';
import { Button } from '@/components/ui/button';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';

import { IconTooltip } from './IconTooltip';

const titlebarButtonClassName =
  'absolute top-3 left-[88px] z-[1] size-7 flex-none cursor-pointer rounded-[var(--radius)] border-0 bg-transparent text-muted-foreground [--wails-draggable:no-drag] hover:bg-accent hover:text-accent-foreground [&_svg]:size-[18px] max-[980px]:left-[76px]';

const activeTitlebarButtonClassName =
  'bg-primary text-primary-foreground hover:bg-primary hover:text-primary-foreground';

export function Titlebar(): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <header
      className="relative box-border select-none border-b bg-[color-mix(in_oklch,var(--background)_94%,transparent)] [--wails-draggable:drag]"
      data-wails-drag
      onDoubleClick={(event) => {
        dispatch(titlebarDoubleClick(event));
      }}
    >
      <TitlebarControls />
      <IdleStatusWidget />
      <TitlebarStatus />
      <div className="absolute inset-0" data-wails-drag />
    </header>
  );
}

function TitlebarControls(): React.ReactElement {
  const dispatch = useAppDispatch();
  const sidebarHidden = useAppSelector((state) => state.layout.sidebarHidden);
  const reviewOpen = useAppSelector((state) => state.layout.reviewOpen);
  const filesOpen = useAppSelector((state) => state.layout.filesOpen);
  const selected = useAppSelector((state) => state.selection.selected);
  const tenants = useAppSelector((state) => state.tenants.tenants);
  const SidebarIcon = sidebarHidden ? PanelLeftOpen : PanelLeftClose;
  const ReviewIcon = reviewOpen ? PanelRightClose : PanelRightOpen;
  const selectedEnvironment = selected
    ? tenants
        .find((tenant) => tenant.name === selected.tenant)
        ?.environments.find((environment) => environment.name === selected.environment)
    : undefined;
  const ideDisabled =
    !selected ||
    (selectedEnvironment?.remote !== false && selectedEnvironment?.sshdEnabled !== true);
  const vscodeTooltip = ideTooltipLabel('VS Code', selected, ideDisabled);
  const intellijTooltip = ideTooltipLabel('IntelliJ IDEA', selected, ideDisabled);

  return (
    <>
      <IconTooltip label="Toggle sidebar">
        <Button
          className={titlebarButtonClassName}
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Toggle sidebar"
          aria-pressed={!sidebarHidden}
          onClick={() => {
            dispatch(toggleSidebar());
          }}
        >
          <SidebarIcon />
        </Button>
      </IconTooltip>
      <IconTooltip label="Toggle diff panel">
        <Button
          className={cn(
            titlebarButtonClassName,
            'left-auto right-[58px] max-[980px]:left-auto max-[980px]:right-12',
            reviewOpen && activeTitlebarButtonClassName,
          )}
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Toggle diff panel"
          aria-pressed={reviewOpen}
          onClick={() => {
            dispatch(toggleReview());
          }}
        >
          <ReviewIcon />
        </Button>
      </IconTooltip>
      <IconTooltip label={ideTooltipLabel('VS Code', selected, ideDisabled)}>
        <span
          className={cn(
            titlebarButtonClassName,
            'left-auto right-[122px] max-[980px]:left-auto max-[980px]:right-[108px]',
          )}
        >
          <Button
            className="size-full border-0 bg-transparent text-inherit hover:bg-accent hover:text-accent-foreground disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-[18px]"
            type="button"
            variant="ghost"
            size="icon"
            aria-label={vscodeTooltip}
            disabled={ideDisabled}
            onClick={() => {
              void dispatch(openIDE(selected ?? null, 'vscode'));
            }}
          >
            <Code2 />
          </Button>
        </span>
      </IconTooltip>
      <IconTooltip label={ideTooltipLabel('IntelliJ IDEA', selected, ideDisabled)}>
        <span
          className={cn(
            titlebarButtonClassName,
            'left-auto right-[90px] max-[980px]:left-auto max-[980px]:right-[78px]',
          )}
        >
          <Button
            className="size-full border-0 bg-transparent text-inherit hover:bg-accent hover:text-accent-foreground disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-[18px]"
            type="button"
            variant="ghost"
            size="icon"
            aria-label={intellijTooltip}
            disabled={ideDisabled}
            onClick={() => {
              void dispatch(openIDE(selected ?? null, 'intellij'));
            }}
          >
            <Blocks />
          </Button>
        </span>
      </IconTooltip>
      <IconTooltip label="Toggle changed files list">
        <Button
          className={cn(
            titlebarButtonClassName,
            'left-auto right-6 max-[980px]:left-auto max-[980px]:right-3.5',
            !reviewOpen && 'hidden',
            filesOpen && activeTitlebarButtonClassName,
          )}
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Toggle changed files list"
          aria-pressed={filesOpen}
          onClick={() => {
            dispatch(setFilesOpen(!filesOpen));
          }}
        >
          <ListTree />
        </Button>
      </IconTooltip>
    </>
  );
}

function IdleStatusWidget(): React.ReactElement | null {
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
        <div
          className={cn(
            'flex h-full min-w-[64px] items-center justify-center rounded-l-md px-2 font-mono text-[12px] leading-none outline-none hover:bg-accent hover:text-accent-foreground focus-visible:ring-2 focus-visible:ring-ring',
            hasAction && 'border-r',
            idleBadge.className,
          )}
          // Focusable read-only badge: tabIndex={0} is what triggers the
          // tooltip via keyboard. Element has no click handler by design.
          // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex
          tabIndex={0}
          aria-label={idleStatusAccessibleLabel(idleStatus)}
        >
          {idleBadge.label}
        </div>
      </TooltipTrigger>
      <TooltipContent
        side="bottom"
        align="end"
        className="max-w-[360px] whitespace-normal text-left leading-5"
      >
        <div className="space-y-1">
          {idleStatusTooltipLines(idleStatus).map((line, index) => (
            <div key={`${index}-${line}`} className={line.startsWith('- ') ? 'pl-2' : undefined}>
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

function TitlebarStatus(): React.ReactElement | null {
  const notification = useAppSelector((state) => state.notification.notification);
  const terminalStatus = useAppSelector((state) => state.terminalStatus);
  const rawIdleStatus = useAppSelector((state) => state.idle.idleStatus);
  const idleCloudContextBusy = useAppSelector((state) => state.idle.idleCloudContextBusy);
  const status = computeTitlebarStatus(notification, terminalStatus);
  if (!status) {
    return null;
  }
  const idleStatus = displayableIdleStatus(rawIdleStatus);
  const idleAction = rawIdleStatus ? idleCloudAction(rawIdleStatus, idleCloudContextBusy) : null;

  return (
    <div
      className={statusPositionClassName(idleStatus, Boolean(idleAction))}
      role={status.kind === 'error' ? 'alert' : 'status'}
      aria-live={status.kind === 'error' ? 'assertive' : 'polite'}
    >
      <div
        className={cn(
          'pointer-events-auto flex h-8 max-w-full items-center gap-2 rounded-md border bg-background px-2.5 text-[13px] leading-none shadow-sm',
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

function statusPositionClassName(
  idleStatus: AppState['idleStatus'],
  hasIdleAction: boolean,
): string {
  if (!idleStatus) {
    if (hasIdleAction) {
      return 'pointer-events-none absolute top-2.5 left-32 right-[204px] z-20 flex justify-center [--wails-draggable:no-drag] max-[980px]:left-[112px] max-[980px]:right-[182px]';
    }
    return 'pointer-events-none absolute top-2.5 left-32 right-[168px] z-20 flex justify-center [--wails-draggable:no-drag] max-[980px]:left-[112px] max-[980px]:right-[146px]';
  }
  if (hasIdleAction) {
    return 'pointer-events-none absolute top-2.5 left-32 right-[268px] z-20 flex justify-center [--wails-draggable:no-drag] max-[980px]:left-[112px] max-[980px]:right-[246px]';
  }
  return 'pointer-events-none absolute top-2.5 left-32 right-[236px] z-20 flex justify-center [--wails-draggable:no-drag] max-[980px]:left-[112px] max-[980px]:right-[214px]';
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
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          className="min-w-0 truncate outline-none focus-visible:ring-2 focus-visible:ring-ring rounded-sm"
          // Focusable status text so the keyboard user can read the tooltip
          // with the full message. Element has no click handler.
          // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex
          tabIndex={0}
          aria-label={fullText}
        >
          {status.message}
          {status.detail && <span className="text-muted-foreground"> - {status.detail}</span>}
        </span>
      </TooltipTrigger>
      <TooltipContent side="bottom" className="max-w-[360px] whitespace-normal text-left leading-5">
        {fullText}
      </TooltipContent>
    </Tooltip>
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

function ideTooltipLabel(ide: string, selected: AppState['selected'], disabled: boolean): string {
  if (!selected) {
    return `Select an environment to open in ${ide}`;
  }
  if (disabled) {
    return `Enable SSHD in environment settings to open ${ide}`;
  }
  return `Open in ${ide}`;
}

type IdleStatus = NonNullable<AppState['idleStatus']>;

function idleCloudAction(
  idleStatus: IdleStatus,
  busy: boolean,
): { action: 'start' | 'stop'; label: string; busy: boolean } | null {
  const name = idleStatus.cloudContextName?.trim();
  if (!idleStatus.managedCloud || !name) {
    return null;
  }
  const displayName = idleStatus.cloudContextLabel?.trim() || name;
  const running = idleStatus.cloudContextStatus?.trim().toLowerCase() === 'running';
  if (running) {
    return {
      action: 'stop',
      label: busy ? `Stopping ${displayName}` : `Stop ${displayName}`,
      busy,
    };
  }
  return {
    action: 'start',
    label: busy ? `Starting ${displayName}` : `Start ${displayName}`,
    busy,
  };
}

function idleStatusBadge(idleStatus: IdleStatus): { label: string; className: string } {
  if (idleStatus.stopError) {
    return {
      label: 'stop failed',
      className: 'border-destructive/60 text-destructive',
    };
  }
  if (idleStatus.stopEligible) {
    if (idleStatus.outsideWorkingHours) {
      return {
        label: 'outside hours',
        className: 'border-[oklch(0.72_0.12_150)] text-[oklch(0.42_0.13_150)]',
      };
    }
    return {
      label: 'idle ready',
      className: 'border-[oklch(0.72_0.12_150)] text-[oklch(0.42_0.13_150)]',
    };
  }
  if (
    idleStatus.stopBlockedReason &&
    (idleStatus.secondsUntilStop <= 0 || isPersistentIdleBlocker(idleStatus.stopBlockedReason))
  ) {
    return {
      label: 'idle blocked',
      className: 'border-[oklch(0.76_0.16_65)] text-[oklch(0.48_0.13_65)]',
    };
  }
  return {
    label: `idle ${idleStatus.secondsUntilStop}s`,
    className: 'border-border text-muted-foreground',
  };
}

function isPersistentIdleBlocker(reason: string): boolean {
  return reason.includes('working-hours') || reason.includes('not cloud-managed');
}

function idleStatusTooltipLines(idleStatus: IdleStatus): string[] {
  const lines = idleStatusSummaryLines(idleStatus);
  appendIdleBlockerLine(lines, idleStatus);
  appendIdleCloudContextLine(lines, idleStatus);
  lines.push(...idleStatusActiveMarkerLines(idleStatus));
  appendIdleStopOutcomeLines(lines, idleStatus);
  return lines;
}

function idleStatusSummaryLines(idleStatus: IdleStatus): string[] {
  return [
    `Idle timeout: ${idleStatus.timeoutSeconds}s`,
    `Seconds until stop: ${idleStatus.secondsUntilStop}s`,
    `Stop eligible: ${idleStatus.stopEligible ? 'yes' : 'no'}`,
    `Working hours: ${idleStatus.outsideWorkingHours ? 'outside; autostop overrides activity' : 'inside; idle timeout applies'}`,
  ];
}

function appendIdleBlockerLine(lines: string[], idleStatus: IdleStatus): void {
  if (idleStatus.stopBlockedReason) {
    lines.push(`Blocked: ${idleStatus.stopBlockedReason}`);
  } else if (!idleStatus.managedCloud) {
    lines.push('Blocked: environment is not cloud-managed');
  }
}

function appendIdleCloudContextLine(lines: string[], idleStatus: IdleStatus): void {
  if (idleStatus.cloudContextName) {
    const label = idleStatus.cloudContextLabel || idleStatus.cloudContextName;
    lines.push(
      `Cloud environment: ${label}${idleStatus.cloudContextStatus ? ` (${idleStatus.cloudContextStatus})` : ''}`,
    );
  }
}

function idleStatusActiveMarkerLines(idleStatus: IdleStatus): string[] {
  const activeMarkers = (idleStatus.markers || []).filter(
    (marker) => marker.name !== 'working-hours' && !marker.idle,
  );
  if (activeMarkers.length === 0) {
    return [];
  }
  return ['Active markers:', ...activeMarkers.map(idleStatusActiveMarkerLine)];
}

function idleStatusActiveMarkerLine(marker: NonNullable<IdleStatus['markers']>[number]): string {
  const remaining =
    marker.secondsRemaining && marker.secondsRemaining > 0
      ? `, ${marker.secondsRemaining}s remaining`
      : '';
  return `- ${marker.name}${marker.reason ? `: ${marker.reason}` : ''}${remaining}`;
}

function appendIdleStopOutcomeLines(lines: string[], idleStatus: IdleStatus): void {
  if (idleStatus.stopEligible) {
    lines.push('Autostop is ready.');
  }
  if (idleStatus.stopError) {
    lines.push('Stop error:', idleStatus.stopError);
  }
}

function idleStatusAccessibleLabel(idleStatus: IdleStatus): string {
  const parts = [
    `Idle timeout ${idleStatus.timeoutSeconds} seconds`,
    `seconds until stop ${idleStatus.secondsUntilStop}`,
    `stop eligible ${idleStatus.stopEligible ? 'yes' : 'no'}`,
    idleStatus.outsideWorkingHours ? 'outside working hours' : 'inside working hours',
  ];
  if (idleStatus.stopBlockedReason) {
    parts.push(`blocked: ${idleStatus.stopBlockedReason}`);
  }
  if (idleStatus.stopError) {
    parts.push(`stop error: ${idleStatus.stopError}`);
  }
  if (idleStatus.cloudContextName) {
    parts.push(
      `cloud environment ${idleStatus.cloudContextLabel || idleStatus.cloudContextName}${idleStatus.cloudContextStatus ? ` ${idleStatus.cloudContextStatus}` : ''}`,
    );
  }
  return parts.join(', ');
}
