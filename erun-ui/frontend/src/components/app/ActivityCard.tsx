import { Button, cn, IconTooltip } from 'erun-kit';
import {
  AlertOctagon,
  Ban,
  Check,
  CheckCircle2,
  ChevronRight,
  Clipboard,
  Clock,
  LoaderCircle,
  Sparkles,
  Stethoscope,
  Trash2,
} from 'lucide-react';
import * as React from 'react';

import type { ActivityQueueEntry } from '@/app/activityQueueState';
import { useAppDispatch } from '@/app/hooks';
import { investigateFailure } from '@/app/orchestratorThunks';
import { startDoctorSelection, startForceDeploySelection } from '@/app/recoveryThunks';
import { ContainerStatusList } from '@/components/app/ActivityCard.ContainerStatus';
import {
  activityElapsedLabel,
  activityStatusLabel,
  activityTargetLabel,
  buildFailureReport,
  cardBorderClassName,
  commandBadgeClassName,
  commandSubtitle,
  copyToClipboard,
  deployUiSelection,
  dismissAffordance,
  shellSessionIdFromEntry,
  shouldShowHelmRecovery,
} from '@/components/app/ActivityQueueDrawer.helpers';

export interface ActivityCardProps {
  entry: ActivityQueueEntry;
  onDismiss?: (id: string) => Promise<void>;
  onForceDismiss?: (id: string) => Promise<void>;
  onCancelWaiting?: (id: string) => Promise<boolean>;
  onRecoverPendingHelm?: (id: string) => Promise<void>;
  onKillSession?: (sessionId: number) => Promise<boolean>;
}

// Per-card ticking clock so a running card re-renders once a second on its own,
// rather than a parent tick cascading a re-render into every card each second.
function useTickingNow(active: boolean): number {
  const [now, setNow] = React.useState(() => Date.now());
  React.useEffect(() => {
    if (!active) return undefined;
    const id = window.setInterval(() => {
      setNow(Date.now());
    }, 1_000);
    return () => {
      window.clearInterval(id);
    };
  }, [active]);
  return now;
}

export const ActivityCard = React.memo(function ActivityCard({
  entry,
  onDismiss,
  onForceDismiss,
  onCancelWaiting,
  onRecoverPendingHelm,
  onKillSession,
}: ActivityCardProps): React.ReactElement {
  const isRunning = entry.status === 'running';
  const isWaiting = entry.status === 'waiting';
  const now = useTickingNow(isRunning || isWaiting);
  const elapsed = activityElapsedLabel(entry, now);
  const handleDismiss = React.useCallback(() => {
    if (isWaiting && onCancelWaiting) {
      void onCancelWaiting(entry.id);
      return;
    }
    if (isRunning && onForceDismiss) {
      void onForceDismiss(entry.id);
      return;
    }
    if (onDismiss) {
      void onDismiss(entry.id);
    }
  }, [entry.id, isRunning, isWaiting, onCancelWaiting, onDismiss, onForceDismiss]);
  const { available: dismissAvailable, label: dismissLabel } = dismissAffordance(
    entry.status,
    onDismiss,
    onForceDismiss,
    onCancelWaiting,
  );
  return (
    <article
      className={cn('rounded-md border bg-card p-3 shadow-sm', cardBorderClassName(entry.status))}
    >
      <header className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-medium">
            <ActivityStatusIndicator status={entry.status} />
            <CommandBadge command={entry.command} />
            <span className="truncate">{activityTargetLabel(entry)}</span>
          </div>
          <div className="text-xs text-muted-foreground truncate">{commandSubtitle(entry)}</div>
        </div>
        <div className="flex flex-none items-center gap-2 text-xs text-muted-foreground">
          <span className="font-mono tabular-nums">{elapsed}</span>
          {dismissAvailable && (
            <IconTooltip label={dismissLabel}>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={dismissLabel}
                onClick={handleDismiss}
              >
                <Trash2 aria-hidden="true" className="size-3.5" />
              </Button>
            </IconTooltip>
          )}
        </div>
      </header>
      {entry.error && (
        <p className="mt-2 break-words rounded-sm border border-destructive/40 bg-destructive/10 px-2 py-1 text-xs text-destructive">
          {entry.error}
        </p>
      )}
      {entry.status === 'failed' && <FailureDetails entry={entry} />}
      {entry.status === 'failed' && (
        <FailedDeployActions entry={entry} onRecoverPendingHelm={onRecoverPendingHelm} />
      )}
      <RecoveryActionRow
        entry={entry}
        onRecoverPendingHelm={onRecoverPendingHelm}
        onKillSession={onKillSession}
      />
      {entry.command === 'deploy' && entry.containers && entry.containers.length > 0 && (
        <ContainerStatusList containers={entry.containers} deploy={entry} />
      )}
    </article>
  );
});

// The copy-report button stays available even on a fast failure that captured
// no output, because the report carries context the user would otherwise retype.
function FailureDetails({ entry }: { entry: ActivityQueueEntry }): React.ReactElement {
  const dispatch = useAppDispatch();
  const [expanded, setExpanded] = React.useState<boolean>(false);
  const [copied, setCopied] = React.useState<boolean>(false);
  const copiedTimer = React.useRef<number | undefined>(undefined);
  React.useEffect(() => {
    return () => {
      if (copiedTimer.current !== undefined) window.clearTimeout(copiedTimer.current);
    };
  }, []);
  const handleCopy = React.useCallback(() => {
    void copyToClipboard(buildFailureReport(entry));
    setCopied(true);
    if (copiedTimer.current !== undefined) window.clearTimeout(copiedTimer.current);
    copiedTimer.current = window.setTimeout(() => {
      setCopied(false);
    }, 1500);
  }, [entry]);
  const detailId = `activity-detail-${entry.id}`;
  const hasOutput = Boolean(entry.detail);
  return (
    <div className="mt-2 space-y-1">
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          className="flex items-center gap-1 rounded-sm text-[11px] font-medium text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
          aria-expanded={expanded}
          aria-controls={detailId}
          onClick={() => {
            setExpanded((value) => !value);
          }}
        >
          <ChevronRight
            aria-hidden="true"
            className={cn('size-3 transition-transform', expanded && 'rotate-90')}
          />
          {expanded ? 'Hide output' : 'Show output'}
        </button>
        <Button
          type="button"
          variant="ghost"
          size="xs"
          className="h-6 gap-1 text-[11px]"
          onClick={handleCopy}
        >
          {copied ? (
            <Check aria-hidden="true" className="size-3 text-emerald-600" />
          ) : (
            <Clipboard aria-hidden="true" className="size-3" />
          )}
          {copied ? 'Copied' : 'Copy failure report'}
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="xs"
          className="h-6 gap-1 text-[11px]"
          aria-label="Investigate this failure with an AI orchestrator"
          onClick={() => {
            void dispatch(
              investigateFailure(buildFailureReport(entry), entry.tenant, entry.environment),
            );
          }}
        >
          <Sparkles aria-hidden="true" className="size-3" />
          Investigate
        </Button>
      </div>
      {expanded &&
        (hasOutput ? (
          <pre
            id={detailId}
            className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-sm border border-border bg-muted/40 px-2 py-1 font-mono text-[10.5px] text-foreground"
          >
            {entry.detail}
          </pre>
        ) : (
          <p id={detailId} className="px-1 text-[11px] text-muted-foreground">
            No command output was captured for this failure. Copy the report for the failure
            context.
          </p>
        ))}
    </div>
  );
}

// "Run doctor" is the primary action because troubleshooting is the safe first
// step before the heavier rebuild/redeploy; all three are explicit, never auto-run.
function FailedDeployActions({
  entry,
  onRecoverPendingHelm,
}: {
  entry: ActivityQueueEntry;
  onRecoverPendingHelm?: (id: string) => Promise<void>;
}): React.ReactElement | null {
  const dispatch = useAppDispatch();
  const [running, setRunning] = React.useState<boolean>(false);
  // Each action pipes `erun …` into the shared Local shell; without this guard
  // repeated clicks would pile commands into that one shell.
  const runAction = React.useCallback(
    (action: () => Promise<unknown>) => {
      if (running) return;
      setRunning(true);
      void Promise.resolve(action()).finally(() => {
        setRunning(false);
      });
    },
    [running],
  );
  if (entry.command !== 'deploy' && entry.command !== 'open') return null;
  const selection = deployUiSelection(entry);
  const showClearPendingHelm = Boolean(onRecoverPendingHelm && entry.release && entry.namespace);
  return (
    <div className="mt-2 flex flex-wrap gap-2">
      <Button
        type="button"
        variant="default"
        size="xs"
        className="h-6 gap-1 text-[11px]"
        disabled={running}
        onClick={() => {
          runAction(() => dispatch(startDoctorSelection(selection)));
        }}
      >
        <Stethoscope aria-hidden="true" className="size-3" />
        Run doctor
      </Button>
      <Button
        type="button"
        variant="secondary"
        size="xs"
        className="h-6 text-[11px]"
        disabled={running}
        onClick={() => {
          runAction(() => dispatch(startForceDeploySelection(selection)));
        }}
      >
        Rebuild &amp; redeploy
      </Button>
      {showClearPendingHelm && onRecoverPendingHelm && (
        <Button
          type="button"
          variant="secondary"
          size="xs"
          className="h-6 text-[11px]"
          disabled={running}
          onClick={() => {
            runAction(() => onRecoverPendingHelm(entry.id));
          }}
        >
          Clear pending helm release
        </Button>
      )}
    </div>
  );
}

function RecoveryActionRow({
  entry,
  onRecoverPendingHelm,
  onKillSession,
}: {
  entry: ActivityQueueEntry;
  onRecoverPendingHelm?: (id: string) => Promise<void>;
  onKillSession?: (sessionId: number) => Promise<boolean>;
}): React.ReactElement | null {
  if (entry.status !== 'running') return null;
  const showHelm = shouldShowHelmRecovery(entry, onRecoverPendingHelm);
  const sessionId = onKillSession ? shellSessionIdFromEntry(entry) : 0;
  const showShell = entry.source === 'shell' && sessionId > 0;
  if (!showHelm && !showShell) return null;
  return (
    <div className="mt-2 flex flex-wrap gap-2">
      {showHelm && onRecoverPendingHelm && (
        <Button
          type="button"
          variant="default"
          size="xs"
          className="h-6 text-[11px]"
          onClick={() => {
            void onRecoverPendingHelm(entry.id);
          }}
        >
          Clear pending helm release
        </Button>
      )}
      {showShell && onKillSession && (
        <Button
          type="button"
          variant="destructive"
          size="xs"
          className="h-6 text-[11px]"
          onClick={() => {
            void onKillSession(sessionId);
          }}
        >
          Kill shell
        </Button>
      )}
    </div>
  );
}

function CommandBadge({ command }: { command: string }): React.ReactElement | null {
  if (!command) return null;
  return (
    <span
      className={cn(
        'rounded-sm px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wider',
        commandBadgeClassName(command),
      )}
    >
      {command}
    </span>
  );
}

function ActivityStatusIcon({
  status,
}: {
  status: ActivityQueueEntry['status'];
}): React.ReactElement {
  switch (status) {
    case 'waiting':
      return <Clock aria-hidden="true" className="size-3.5 text-muted-foreground" />;
    case 'running':
      return <LoaderCircle aria-hidden="true" className="size-3.5 animate-spin text-blue-500" />;
    case 'succeeded':
      return <CheckCircle2 aria-hidden="true" className="size-3.5 text-emerald-500" />;
    case 'failed':
      return <AlertOctagon aria-hidden="true" className="size-3.5 text-destructive" />;
    case 'skipped':
      return <ChevronRight aria-hidden="true" className="size-3.5 text-muted-foreground" />;
    case 'cancelled':
      return <Ban aria-hidden="true" className="size-3.5 text-muted-foreground" />;
  }
}

// The icon alone is aria-hidden and its color is the only other signal, so
// screen-reader and low-vision users saw nothing distinguishing a card's
// status (WCAG 1.4.1). This adds the same text visibly for everyone.
function ActivityStatusIndicator({
  status,
}: {
  status: ActivityQueueEntry['status'];
}): React.ReactElement {
  return (
    <span className="inline-flex flex-none items-center gap-1">
      <ActivityStatusIcon status={status} />
      <span className="text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
        {activityStatusLabel(status)}
      </span>
    </span>
  );
}
