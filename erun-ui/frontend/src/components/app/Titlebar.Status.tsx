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
import { AlertCircle, Bug, CheckCircle2, Copy, Info, LoaderCircle, X } from 'lucide-react';
import * as React from 'react';

import { openInstallDocs } from '@/app/documentationThunks';
import { useAppDispatch, useAppSelector } from '@/app/hooks';
import { openManageDialog, setManageTab } from '@/app/manageDialogThunks';
import {
  copyTerminalOutput,
  dismissNotification,
  dismissTerminalStatus,
  waitLongerForTerminalStatus,
} from '@/app/notificationThunks';
import { reportFailure, restartOrchestrator } from '@/app/orchestratorThunks';
import type { AppNotification, AppState } from '@/app/state';
import type { AppDispatch } from '@/app/store';
import { openTenantDashboard } from '@/app/tenantDialogThunks';
import { buildTitlebarFailureReport } from '@/app/titlebarFailureReport';

import { ClipboardSetText } from '../../../wailsjs/runtime/runtime';

// Long error text can't be read or copied from a hover tooltip, so past
// this length the message escalates to a selectable popover the operator
// can copy into a bug report.
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
  // Set only for source: 'notification' — identifies which queued entry this
  // is, so dismissing it removes exactly this one even if another has been
  // queued behind it since render.
  notificationId?: string;
  // envAction/envTenant/envEnvironment carry a notification's own remedy
  // action (currently only 'deploy'), set only when the notification named
  // one and an unambiguous env to target — see AppNotification['action']
  // (#1390).
  envAction?: AppNotification['action'];
  envTenant?: string;
  envEnvironment?: string;
  // Set only when the notification's action operates on a specific
  // orchestrator (restart-orchestrator / install-and-restart-orchestrator)
  // rather than a specific env.
  orchestratorId?: string;
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
  // The queue can hold several concurrent failures; the titlebar has room for
  // one pill, so it always shows (and dismisses) the oldest — the others stay
  // queued behind it rather than being silently overwritten.
  const notification = useAppSelector((state) => state.notification.notifications[0]);
  const terminalStatus = useAppSelector((state) => state.terminalStatus);
  const status = computeTitlebarStatus(notification, terminalStatus);
  if (!status) {
    return null;
  }

  // Cap width so the status pill never crowds the right-cluster buttons on
  // narrow viewports.
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
        <StatusEnvAction status={status} />
        {status.kind === 'error' && <StatusReportBugAction status={status} />}
        {status.copyOutput &&
          (status.source === 'notification' ? (
            <NotificationCopyAction text={status.copyOutput} />
          ) : (
            <StatusCopyAction status={status} />
          ))}
        <StatusDismissAction status={status} />
      </div>
    </div>
  );
}

function computeTitlebarStatus(
  notification: AppNotification | undefined,
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
      kind: notification.kind,
      message: notification.message,
      notificationId: notification.id,
      source: 'notification',
      detail: '',
      busy: false,
      // Only error/warning notifications carry a message the operator needs to
      // copy into a bug report; transient success/info toasts don't get one.
      copyOutput:
        notification.kind === 'error' || notification.kind === 'warning'
          ? notification.message
          : '',
      copyStatus: '',
      action: '',
      envAction: notification.action,
      envTenant: notification.tenant,
      envEnvironment: notification.environment,
      orchestratorId: notification.orchestratorId,
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

// StatusEnvAction dispatches to whichever named remedy the notification
// carries — kept as one switch here rather than inline branches in
// TitlebarStatus so that component's own complexity stays bounded as more
// remedies get named actions.
function StatusEnvAction({ status }: { status: TitlebarStatusValue }): React.ReactElement | null {
  switch (status.envAction) {
    case 'deploy':
      return status.envTenant && status.envEnvironment ? (
        <StatusDeployAction
          status={status}
          tenant={status.envTenant}
          environment={status.envEnvironment}
        />
      ) : null;
    case 'restart-orchestrator':
      return status.orchestratorId ? (
        <StatusRestartOrchestratorAction status={status} orchestratorId={status.orchestratorId} />
      ) : null;
    case 'install-and-restart-orchestrator':
      return status.orchestratorId ? (
        <>
          <StatusInstallDocsAction />
          <StatusRestartOrchestratorAction status={status} orchestratorId={status.orchestratorId} />
        </>
      ) : null;
    case 'invite-approved':
      return status.envTenant ? (
        <StatusInviteApprovedAction status={status} tenant={status.envTenant} />
      ) : null;
    default:
      return null;
  }
}

// StatusInviteApprovedAction navigates to the tenant dashboard rather than
// acting inline -- mirroring StatusDeployAction's "navigate to the real
// recovery surface" pattern. There is nothing to do here beyond looking at
// the now-enrolled tenant; the invite link itself (the fallback/transferable
// artefact) is copyable from the durable Activity Queue entry the same
// approval pushed.
function StatusInviteApprovedAction({
  status,
  tenant,
}: {
  status: TitlebarStatusValue;
  tenant: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <Button
      className="h-6 flex-none rounded-md px-2 text-[12px] text-foreground hover:bg-accent hover:text-accent-foreground"
      type="button"
      variant="ghost"
      size="xs"
      onClick={() => {
        dispatch(openTenantDashboard(tenant));
        if (status.notificationId) {
          dispatch(dismissNotification(status.notificationId));
        }
      }}
    >
      Open dashboard
    </Button>
  );
}

// StatusDeployAction is the toast's own version of #1390's rule: a runtime-
// unreachable notification names "deploy" as its remedy, and the app already
// has a control for that — the Manage dialog's Runtime tab, where the
// operator picks a version and clicks Deploy. Opening straight to that tab
// (rather than guessing a version and deploying blind) mirrors #1389's
// navigate-to-the-control pattern. Dismisses the notification on click since
// the operator is now looking at its actual recovery surface.
function StatusDeployAction({
  status,
  tenant,
  environment,
}: {
  status: TitlebarStatusValue;
  tenant: string;
  environment: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <Button
      className="h-6 flex-none rounded-md px-2 text-[12px] text-foreground hover:bg-accent hover:text-accent-foreground"
      type="button"
      variant="ghost"
      size="xs"
      onClick={() => {
        dispatch(openManageDialog({ tenant, environment }));
        dispatch(setManageTab('runtime'));
        if (status.notificationId) {
          dispatch(dismissNotification(status.notificationId));
        }
      }}
    >
      Deploy
    </Button>
  );
}

// StatusInstallDocsAction links the CLI install page for the notice's
// "erun executable could not be resolved" cause — naming the fix without
// giving a way to act on it is the dead end this exists to close.
function StatusInstallDocsAction(): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <Button
      className="h-6 flex-none rounded-md px-2 text-[12px] text-foreground hover:bg-accent hover:text-accent-foreground"
      type="button"
      variant="ghost"
      size="xs"
      onClick={() => {
        dispatch(openInstallDocs());
      }}
    >
      Install docs
    </Button>
  );
}

// StatusRestartOrchestratorAction performs the notice's other named remedy —
// restarting the orchestrator — directly, rather than leaving it as an
// instruction with no control behind it.
function StatusRestartOrchestratorAction({
  status,
  orchestratorId,
}: {
  status: TitlebarStatusValue;
  orchestratorId: string;
}): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <Button
      className="h-6 flex-none rounded-md px-2 text-[12px] text-foreground hover:bg-accent hover:text-accent-foreground"
      type="button"
      variant="ghost"
      size="xs"
      onClick={() => {
        void dispatch(restartOrchestrator(orchestratorId));
        if (status.notificationId) {
          dispatch(dismissNotification(status.notificationId));
        }
      }}
    >
      Restart
    </Button>
  );
}

// StatusReportBugAction is the standing action every error status carries —
// unlike StatusEnvAction, which only renders when a remedy was resolved, this
// is unconditional (root AGENTS.md "Smooth, Seamless, No Dead Ends": the
// operator must never read an error pill with nothing to do about it). It
// renders after any named remedy so a known fix still leads, and hands the
// failure to an agent that drafts the report (orchestratorThunks.reportFailure)
// rather than opening a form — the click itself is the only synchronous piece
// this button can show progress for, so it disables and spins for that
// admission round-trip; the draft itself continues in its own focused
// terminal, reachable rather than modal. Dismissing this status once the
// outcome settles mirrors StatusDeployAction/StatusRestartOrchestratorAction:
// the operator's recovery surface is now elsewhere (the new draft's terminal,
// an already-running draft that was focused, or the fallback browser tab), so
// clearing the queue advances it to whatever comes next rather than leaving a
// handled error sitting in front of it.
function StatusReportBugAction({ status }: { status: TitlebarStatusValue }): React.ReactElement {
  const dispatch = useAppDispatch();
  const [reporting, setReporting] = React.useState(false);
  return (
    <Button
      className="h-6 flex-none gap-1 rounded-md px-2 text-[12px] text-foreground hover:bg-accent hover:text-accent-foreground [&_svg]:size-3.5"
      type="button"
      variant="ghost"
      size="xs"
      disabled={reporting}
      onClick={() => {
        setReporting(true);
        const report = buildTitlebarFailureReport(status);
        void dispatch(
          reportFailure(
            report,
            status.message,
            status.envTenant ?? '',
            status.envEnvironment ?? '',
          ),
        ).finally(() => {
          setReporting(false);
          dismissTitlebarStatus(dispatch, status);
        });
      }}
    >
      <Bug aria-hidden="true" />
      {reporting ? 'Reporting…' : 'Report a bug'}
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

function NotificationCopyAction({ text }: { text: string }): React.ReactElement {
  const [copied, setCopied] = React.useState(false);
  const onCopy = React.useCallback(() => {
    void ClipboardSetText(text).then(() => {
      setCopied(true);
      window.setTimeout(() => {
        setCopied(false);
      }, 1400);
    });
  }, [text]);
  return (
    <IconTooltip label="Copy message">
      <Button
        className="h-6 flex-none gap-1 rounded-md px-2 text-[12px] text-foreground hover:bg-accent hover:text-accent-foreground [&_svg]:size-3.5"
        type="button"
        variant="ghost"
        size="xs"
        onClick={onCopy}
      >
        {copied ? <CheckCircle2 aria-hidden="true" /> : <Copy aria-hidden="true" />}
        {copied ? 'Copied' : 'Copy'}
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
    dispatch(dismissNotification(status.notificationId));
    return;
  }
  dispatch(dismissTerminalStatus());
}
