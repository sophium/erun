import * as React from 'react';
import { AlertCircle, Blocks, CheckCircle2, Code2, Copy, Info, ListTree, LoaderCircle, PanelLeftClose, PanelLeftOpen, PanelRightClose, PanelRightOpen, X } from 'lucide-react';

import type { ERunUIController } from '@/app/ERunUIController';
import type { AppState } from '@/app/state';
import { Button } from '@/components/ui/button';
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
  const terminalStatus = !notification && state.terminalMessage
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
      <IconTooltip label="Open in VS Code">
        <Button
          className={cn(
            titlebarButtonClassName,
            'left-auto right-[122px] max-[980px]:left-auto max-[980px]:right-[108px]',
          )}
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Open selected environment in VS Code"
          disabled={!selected}
          onClick={() => {
            void controller.openIDE(selected ?? null, 'vscode');
          }}
        >
          <Code2 />
        </Button>
      </IconTooltip>
      <IconTooltip label="Open in IntelliJ IDEA">
        <Button
          className={cn(
            titlebarButtonClassName,
            'left-auto right-[90px] max-[980px]:left-auto max-[980px]:right-[78px]',
          )}
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Open selected environment in IntelliJ IDEA"
          disabled={!selected}
          onClick={() => {
            void controller.openIDE(selected ?? null, 'intellij');
          }}
        >
          <Blocks />
        </Button>
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
      {status && (
        <div
          className="pointer-events-none absolute top-2.5 right-[168px] left-32 z-20 flex justify-center [--wails-draggable:no-drag] max-[980px]:right-[146px] max-[980px]:left-[112px]"
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
