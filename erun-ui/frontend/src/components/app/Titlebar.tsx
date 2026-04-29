import * as React from 'react';
import { AlertCircle, Blocks, CheckCircle2, Code2, Copy, Info, ListTree, LoaderCircle, PanelLeftClose, PanelLeftOpen, PanelRightClose, PanelRightOpen, Play, Power, X } from 'lucide-react';

import type { ERunUIController } from '@/app/ERunUIController';
import type { AppState } from '@/app/state';
import { Button } from '@/components/ui/button';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';
import { IconTooltip } from './IconTooltip';

const titlebarButtonClassName =
  'absolute top-3 left-[88px] z-[1] size-7 flex-none cursor-pointer rounded-[var(--radius)] border-0 bg-transparent text-muted-foreground [--wails-draggable:no-drag] hover:bg-accent hover:text-accent-foreground [&_svg]:size-[18px] max-[980px]:left-[76px]';

const activeTitlebarButtonClassName = 'bg-primary text-primary-foreground hover:bg-primary hover:text-primary-foreground';

export function Titlebar({ controller, state }: { controller: ERunUIController; state: AppState }): React.ReactElement {
  const SidebarIcon = state.sidebarHidden ? PanelLeftOpen : PanelLeftClose;
  const ReviewIcon = state.reviewOpen ? PanelRightClose : PanelRightOpen;
  const notification = state.notification;
  const selected = state.selected;
  const selectedEnvironment = selected ? state.tenants.find((tenant) => tenant.name === selected.tenant)?.environments.find((environment) => environment.name === selected.environment) : undefined;
  const ideDisabled = !selected || selectedEnvironment?.sshdEnabled !== true;
  const vscodeTooltip = ideTooltipLabel('VS Code', selected, ideDisabled);
  const intellijTooltip = ideTooltipLabel('IntelliJ IDEA', selected, ideDisabled);
  const terminalOverlayVisible = state.terminalBusy && Boolean(state.terminalMessage);
  const terminalStatus = !notification && !terminalOverlayVisible && state.terminalMessage
    ? {
        kind: state.terminalStatusKind,
        message: state.terminalMessage,
        detail: state.terminalStatusDetail,
        busy: state.terminalBusy,
        copyOutput: state.terminalCopyOutput,
        copyStatus: state.terminalCopyStatus,
        action: state.terminalStatusAction,
      }
    : null;
  const status = notification ? { ...notification, detail: '', busy: false, copyOutput: '', copyStatus: '', action: '' } : terminalStatus;
  const NotificationIcon = status?.busy ? LoaderCircle : status?.kind === 'success' ? CheckCircle2 : status?.kind === 'warning' || status?.kind === 'error' ? AlertCircle : Info;
  const idleStatus = state.idleStatus;
  const idleBadge = idleStatus ? idleStatusBadge(idleStatus) : null;
  const idleAction = idleStatus ? idleCloudAction(idleStatus, state.idleCloudContextBusy) : null;
  const IdleActionIcon = idleAction?.busy ? LoaderCircle : idleAction?.action === 'start' ? Play : Power;

  return (
    <header
      className="relative box-border select-none border-b bg-[color-mix(in_oklch,var(--background)_94%,transparent)] [--wails-draggable:drag]"
      data-wails-drag
      onDoubleClick={(event) => controller.titlebarDoubleClick(event)}
    >
      <IconTooltip label="Toggle sidebar">
        <Button
          className={titlebarButtonClassName}
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Toggle sidebar"
          aria-pressed={!state.sidebarHidden}
          onClick={() => controller.toggleSidebar()}
        >
          <SidebarIcon />
        </Button>
      </IconTooltip>
      <IconTooltip label="Toggle diff panel">
        <Button
          className={cn(
            titlebarButtonClassName,
            'left-auto right-[58px] max-[980px]:left-auto max-[980px]:right-12',
            state.reviewOpen && activeTitlebarButtonClassName,
          )}
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Toggle diff panel"
          aria-pressed={state.reviewOpen}
          onClick={() => controller.toggleReview()}
        >
          <ReviewIcon />
        </Button>
      </IconTooltip>
      <IconTooltip label={vscodeTooltip}>
        <span className={cn(titlebarButtonClassName, 'left-auto right-[122px] max-[980px]:left-auto max-[980px]:right-[108px]')}>
          <Button
            className="size-full border-0 bg-transparent text-inherit hover:bg-accent hover:text-accent-foreground disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-[18px]"
            type="button"
            variant="ghost"
            size="icon"
            aria-label={vscodeTooltip}
            disabled={ideDisabled}
            onClick={() => {
              void controller.openIDE(selected ?? null, 'vscode');
            }}
          >
            <Code2 />
          </Button>
        </span>
      </IconTooltip>
      <IconTooltip label={intellijTooltip}>
        <span className={cn(titlebarButtonClassName, 'left-auto right-[90px] max-[980px]:left-auto max-[980px]:right-[78px]')}>
          <Button
            className="size-full border-0 bg-transparent text-inherit hover:bg-accent hover:text-accent-foreground disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-[18px]"
            type="button"
            variant="ghost"
            size="icon"
            aria-label={intellijTooltip}
            disabled={ideDisabled}
            onClick={() => {
              void controller.openIDE(selected ?? null, 'intellij');
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
            !state.reviewOpen && 'hidden',
            state.filesOpen && activeTitlebarButtonClassName,
          )}
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Toggle changed files list"
          aria-pressed={state.filesOpen}
          onClick={() => controller.setFilesOpen(!state.filesOpen)}
        >
          <ListTree />
        </Button>
      </IconTooltip>
      {idleStatus && (
        <div
          className={cn(
            'absolute top-3 right-[168px] z-[1] flex h-7 items-center rounded-md border bg-background [--wails-draggable:no-drag] max-[980px]:right-[146px]',
            idleBadge?.className,
          )}
        >
          <Tooltip>
            <TooltipTrigger asChild>
              <div
                className={cn(
                  'flex h-full min-w-[64px] items-center justify-center rounded-l-md px-2 font-mono text-[12px] leading-none outline-none hover:bg-accent hover:text-accent-foreground focus-visible:ring-2 focus-visible:ring-ring',
                  idleAction && 'border-r',
                  idleBadge?.className,
                )}
                tabIndex={0}
                aria-label={idleStatusAccessibleLabel(idleStatus)}
              >
                {idleBadge?.label}
              </div>
            </TooltipTrigger>
            <TooltipContent side="bottom" align="end" className="max-w-[360px] whitespace-normal text-left leading-5">
              <div className="space-y-1">
                {idleStatusTooltipLines(idleStatus).map((line, index) => (
                  <div key={`${index}-${line}`} className={line.startsWith('- ') ? 'pl-2' : undefined}>
                    {line}
                  </div>
                ))}
              </div>
            </TooltipContent>
          </Tooltip>
          {idleAction && (
            <IconTooltip label={idleAction.label}>
              <Button
                className="h-full w-7 rounded-l-none rounded-r-md border-0 bg-transparent text-muted-foreground hover:bg-accent hover:text-accent-foreground disabled:pointer-events-none disabled:opacity-60 [&_svg]:size-3.5"
                type="button"
                variant="ghost"
                size="icon"
                aria-label={idleAction.label}
                disabled={idleAction.busy}
                onClick={() => {
                  void controller.toggleIdleCloudContext();
                }}
              >
                <IdleActionIcon className={cn(idleAction.busy && 'animate-spin')} aria-hidden="true" />
              </Button>
            </IconTooltip>
          )}
        </div>
      )}
      {status && (
        <div
          className={cn(
            'pointer-events-none absolute top-2.5 left-32 z-20 flex justify-center [--wails-draggable:no-drag] max-[980px]:left-[112px]',
            idleStatus ? (idleAction ? 'right-[268px] max-[980px]:right-[246px]' : 'right-[236px] max-[980px]:right-[214px]') : 'right-[168px] max-[980px]:right-[146px]',
          )}
          role={status.kind === 'error' ? 'alert' : 'status'}
          aria-live={status.kind === 'error' ? 'assertive' : 'polite'}
        >
          <div
            className={cn(
              'pointer-events-auto flex h-8 max-w-full items-center gap-2 rounded-md border bg-background px-2.5 text-[13px] leading-none shadow-sm',
              status.kind === 'success' && 'border-[oklch(0.72_0.12_150)] text-foreground',
              status.kind === 'warning' && 'border-[oklch(0.76_0.16_65)] text-foreground',
              status.kind === 'error' && 'border-destructive/60 text-foreground',
              status.kind === 'info' && 'border-border text-foreground',
            )}
          >
            <NotificationIcon
              className={cn(
                'size-4 flex-none',
                status.busy && 'animate-spin text-muted-foreground',
                status.kind === 'success' && 'text-[oklch(0.52_0.15_150)]',
                status.kind === 'warning' && 'text-[oklch(0.58_0.15_65)]',
                status.kind === 'error' && 'text-destructive',
                status.kind === 'info' && 'text-muted-foreground',
              )}
              aria-hidden="true"
            />
            <span className="min-w-0 truncate" title={status.detail ? `${status.message}. ${status.detail}` : status.message}>
              {status.message}
              {status.detail && <span className="text-muted-foreground"> - {status.detail}</span>}
            </span>
            {status.action === 'wait-longer' && (
              <Button
                className="h-6 flex-none rounded-md px-2 text-[12px] text-foreground hover:bg-accent hover:text-accent-foreground"
                type="button"
                variant="ghost"
                size="xs"
                onClick={() => {
                  void controller.waitLongerForTerminalStatus();
                }}
              >
                Wait longer
              </Button>
            )}
            {status.copyOutput && (
              <IconTooltip label="Copy terminal output">
                <Button
                  className="h-6 flex-none gap-1 rounded-md px-2 text-[12px] text-foreground hover:bg-accent hover:text-accent-foreground [&_svg]:size-3.5"
                  type="button"
                  variant="ghost"
                  size="xs"
                  onClick={() => {
                    void controller.copyTerminalOutput();
                  }}
                >
                  {status.copyStatus === 'Copied' ? <CheckCircle2 aria-hidden="true" /> : <Copy aria-hidden="true" />}
                  {status.copyStatus || 'Copy output'}
                </Button>
              </IconTooltip>
            )}
            <IconTooltip label="Dismiss status">
              <Button
                className="-mr-1 size-6 flex-none text-muted-foreground hover:bg-accent hover:text-accent-foreground [&_svg]:size-3.5"
                type="button"
                variant="ghost"
                size="icon-xs"
                aria-label="Dismiss status"
                onClick={() => {
                  if (notification) {
                    controller.dismissNotification();
                    return;
                  }
                  controller.dismissTerminalStatus();
                }}
              >
                <X />
              </Button>
            </IconTooltip>
          </div>
        </div>
      )}
      <div className="absolute inset-0" data-wails-drag />
    </header>
  );
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

function idleCloudAction(idleStatus: IdleStatus, busy: boolean): { action: 'start' | 'stop'; label: string; busy: boolean } | null {
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
  if (idleStatus.stopBlockedReason && (idleStatus.secondsUntilStop <= 0 || isPersistentIdleBlocker(idleStatus.stopBlockedReason))) {
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
  const lines = [
    `Idle timeout: ${idleStatus.timeoutSeconds}s`,
    `Seconds until stop: ${idleStatus.secondsUntilStop}s`,
    `Stop eligible: ${idleStatus.stopEligible ? 'yes' : 'no'}`,
    `Working hours: ${idleStatus.outsideWorkingHours ? 'outside; autostop overrides activity' : 'inside; idle timeout applies'}`,
  ];
  if (idleStatus.stopBlockedReason) {
    lines.push(`Blocked: ${idleStatus.stopBlockedReason}`);
  } else if (!idleStatus.managedCloud) {
    lines.push('Blocked: environment is not cloud-managed');
  }
  if (idleStatus.cloudContextName) {
    const label = idleStatus.cloudContextLabel || idleStatus.cloudContextName;
    lines.push(`Cloud environment: ${label}${idleStatus.cloudContextStatus ? ` (${idleStatus.cloudContextStatus})` : ''}`);
  }
  const activeMarkers = (idleStatus.markers || []).filter((marker) => marker.name !== 'working-hours' && !marker.idle);
  if (activeMarkers.length > 0) {
    lines.push('Active markers:');
    for (const marker of activeMarkers) {
      const remaining = marker.secondsRemaining && marker.secondsRemaining > 0 ? `, ${marker.secondsRemaining}s remaining` : '';
      lines.push(`- ${marker.name}${marker.reason ? `: ${marker.reason}` : ''}${remaining}`);
    }
  }
  if (idleStatus.stopEligible) {
    lines.push('Autostop is ready.');
  }
  if (idleStatus.stopError) {
    lines.push('Stop error:', idleStatus.stopError);
  }
  return lines;
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
    parts.push(`cloud environment ${idleStatus.cloudContextLabel || idleStatus.cloudContextName}${idleStatus.cloudContextStatus ? ` ${idleStatus.cloudContextStatus}` : ''}`);
  }
  return parts.join(', ');
}
