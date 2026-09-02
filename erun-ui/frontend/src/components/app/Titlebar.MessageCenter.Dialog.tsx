import {
  Button,
  Checkbox,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  IconTooltip,
  Label,
  StatusBadge,
  Tabs,
  TabsList,
  TabsTrigger,
} from 'erun-kit';
import { Bug, CheckCircle2, Copy } from 'lucide-react';
import * as React from 'react';

import { openInstallDocs } from '@/app/documentationThunks';
import { useAppDispatch } from '@/app/hooks';
import { useAppSelector } from '@/app/hooks';
import { openManageDialog, setManageTab } from '@/app/manageDialogThunks';
import {
  DIALOG_FILTER_KINDS,
  filterNotificationHistory,
  type NotificationFilter,
  notificationHistoryNewestFirst,
  notificationIdentityLabel,
  notificationKindLabel,
  notificationKindTone,
} from '@/app/notificationCenter';
import { dismissNotification } from '@/app/notificationThunks';
import { reportFailure, restartOrchestrator } from '@/app/orchestratorThunks';
import type { AppNotification, AppNotificationKind } from '@/app/state';
import { openTenantDashboard } from '@/app/tenantDialogThunks';
import { buildTitlebarFailureReport } from '@/app/titlebarFailureReport';

import { ClipboardSetText } from '../../../wailsjs/runtime/runtime';

interface TitlebarMessageCenterDialogProps {
  initialFilter: AppNotificationKind | 'all' | null;
  onClose: () => void;
}

// TitlebarMessageCenterDialog is the message centre's review surface: the
// session's full notification history, filterable by class, with debug
// hidden until explicitly revealed. Opening always starts from whichever
// class icon (or the history fallback) was clicked; the tab strip lets the
// operator move to a different class without closing.
export function TitlebarMessageCenterDialog({
  initialFilter,
  onClose,
}: TitlebarMessageCenterDialogProps): React.ReactElement {
  const notifications = useAppSelector((state) => state.notification.notifications);
  const [filter, setFilter] = React.useState<NotificationFilter>('all');
  const [showDebug, setShowDebug] = React.useState(false);
  const open = initialFilter !== null;

  React.useEffect(() => {
    if (initialFilter !== null) {
      setFilter(initialFilter);
    }
  }, [initialFilter]);

  const rows = filterNotificationHistory(
    notificationHistoryNewestFirst(notifications),
    filter,
    showDebug,
  );

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          onClose();
        }
      }}
    >
      <DialogContent className="flex max-h-[80vh] flex-col sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Messages</DialogTitle>
          <DialogDescription>
            Every message this session, newest first. Dismissing one only marks it read -- it stays
            here for the rest of the session.
          </DialogDescription>
        </DialogHeader>
        <MessageCenterFilterBar filter={filter} onFilterChange={setFilter} />
        <div className="flex items-center gap-2">
          <Checkbox
            id="message-center-show-debug"
            checked={showDebug}
            onCheckedChange={(checked) => {
              setShowDebug(checked === true);
            }}
          />
          <Label htmlFor="message-center-show-debug" className="text-xs text-muted-foreground">
            Show debug messages
          </Label>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto">
          {rows.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted-foreground">No messages.</p>
          ) : (
            <ul className="space-y-2">
              {rows.map((row) => (
                <li key={row.id}>
                  <MessageCenterRow notification={row} onClose={onClose} />
                </li>
              ))}
            </ul>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function MessageCenterFilterBar({
  filter,
  onFilterChange,
}: {
  filter: NotificationFilter;
  onFilterChange: (filter: NotificationFilter) => void;
}): React.ReactElement {
  return (
    <Tabs
      value={filter}
      onValueChange={(value) => {
        onFilterChange(value as NotificationFilter);
      }}
    >
      <TabsList>
        <TabsTrigger value="all">All</TabsTrigger>
        {DIALOG_FILTER_KINDS.map((kind) => (
          <TabsTrigger key={kind} value={kind}>
            {notificationKindLabel(kind)}
          </TabsTrigger>
        ))}
      </TabsList>
    </Tabs>
  );
}

function MessageCenterRow({
  notification: n,
  onClose,
}: {
  notification: AppNotification;
  onClose: () => void;
}): React.ReactElement {
  const identity = notificationIdentityLabel(n);
  return (
    <div className="space-y-1.5 rounded-md border bg-background p-2.5 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <StatusBadge tone={notificationKindTone(n.kind)} label={notificationKindLabel(n.kind)} />
        {identity && <span className="text-muted-foreground">{identity}</span>}
        <span className="ml-auto text-muted-foreground">
          {new Date(n.timestamp).toLocaleTimeString()}
        </span>
      </div>
      <p className="whitespace-pre-wrap break-words select-text">{n.message}</p>
      <div className="flex flex-wrap items-center gap-1">
        <MessageCenterEnvAction notification={n} onClose={onClose} />
        {n.kind === 'error' && <MessageCenterReportBugAction notification={n} />}
        <MessageCenterCopyAction message={n.message} />
        {!n.dismissed && <MessageCenterDismissAction id={n.id} />}
      </div>
    </div>
  );
}

// MessageCenterEnvAction dispatches to whichever named remedy the
// notification carries -- ported from the old single-pill's StatusEnvAction
// switch, now keyed off the notification directly instead of the pill's own
// derived TitlebarStatusValue. Every branch that navigates to a destination
// surface (the Manage dialog, the tenant dashboard) also closes this dialog:
// left open, it would stack a second modal underneath the one the operator
// just asked for. Report-bug/Copy don't navigate anywhere, so they don't
// close it -- see MessageCenterReportBugAction/MessageCenterCopyAction.
function MessageCenterEnvAction({
  notification: n,
  onClose,
}: {
  notification: AppNotification;
  onClose: () => void;
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  switch (n.action) {
    case 'deploy':
      return n.tenant && n.environment ? (
        <ActionButton
          label="Deploy"
          onClick={() => {
            dispatch(
              openManageDialog({ tenant: n.tenant ?? '', environment: n.environment ?? '' }),
            );
            dispatch(setManageTab('runtime'));
            dispatch(dismissNotification(n.id));
            onClose();
          }}
        />
      ) : null;
    case 'restart-orchestrator':
      return n.orchestratorId ? (
        <ActionButton
          label="Restart"
          onClick={() => {
            void dispatch(restartOrchestrator(n.orchestratorId ?? ''));
            dispatch(dismissNotification(n.id));
          }}
        />
      ) : null;
    case 'install-and-restart-orchestrator':
      return n.orchestratorId ? (
        <>
          <ActionButton
            label="Install docs"
            onClick={() => {
              dispatch(openInstallDocs());
            }}
          />
          <ActionButton
            label="Restart"
            onClick={() => {
              void dispatch(restartOrchestrator(n.orchestratorId ?? ''));
              dispatch(dismissNotification(n.id));
            }}
          />
        </>
      ) : null;
    case 'invite-approved':
      return n.tenant ? (
        <ActionButton
          label="Open dashboard"
          onClick={() => {
            dispatch(openTenantDashboard(n.tenant ?? ''));
            dispatch(dismissNotification(n.id));
            onClose();
          }}
        />
      ) : null;
    default:
      return null;
  }
}

// MessageCenterReportBugAction is the standing action every error message
// carries, ported from the old pill's StatusReportBugAction (root AGENTS.md
// "Smooth, Seamless, No Dead Ends": an error must never have nothing to do
// about it). Hands the failure to an agent that drafts the report rather
// than opening a form; disables and spins for the admission round-trip.
function MessageCenterReportBugAction({
  notification: n,
}: {
  notification: AppNotification;
}): React.ReactElement {
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
        const report = buildTitlebarFailureReport({
          message: n.message,
          detail: '',
          copyOutput: n.message,
          envTenant: n.tenant,
          envEnvironment: n.environment,
        });
        void dispatch(
          reportFailure(report, n.message, n.tenant ?? '', n.environment ?? ''),
        ).finally(() => {
          setReporting(false);
          dispatch(dismissNotification(n.id));
        });
      }}
    >
      <Bug aria-hidden="true" />
      {reporting ? 'Reporting…' : 'Report a bug'}
    </Button>
  );
}

function MessageCenterCopyAction({ message }: { message: string }): React.ReactElement {
  const [copied, setCopied] = React.useState(false);
  const onCopy = React.useCallback(() => {
    void ClipboardSetText(message).then(() => {
      setCopied(true);
      window.setTimeout(() => {
        setCopied(false);
      }, 1400);
    });
  }, [message]);
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

function MessageCenterDismissAction({ id }: { id: string }): React.ReactElement {
  const dispatch = useAppDispatch();
  return (
    <ActionButton
      label="Mark read"
      onClick={() => {
        dispatch(dismissNotification(id));
      }}
    />
  );
}

function ActionButton({
  label,
  onClick,
}: {
  label: string;
  onClick: () => void;
}): React.ReactElement {
  return (
    <Button
      className="h-6 flex-none rounded-md px-2 text-[12px] text-foreground hover:bg-accent hover:text-accent-foreground"
      type="button"
      variant="ghost"
      size="xs"
      onClick={onClick}
    >
      {label}
    </Button>
  );
}
