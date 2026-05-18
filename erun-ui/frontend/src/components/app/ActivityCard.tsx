import {
  AlertOctagon,
  Ban,
  CheckCircle2,
  ChevronRight,
  Clock,
  LoaderCircle,
  Trash2,
} from 'lucide-react';
import * as React from 'react';

import type { ActivityQueueEntry } from '@/app/activityQueueState';
import { ContainerStatusList } from '@/components/app/ActivityCard.ContainerStatus';
import {
  activityElapsedLabel,
  activityTargetLabel,
  cardBorderClassName,
  commandBadgeClassName,
  commandSubtitle,
  dismissAffordance,
  shellSessionIdFromEntry,
  shouldShowHelmRecovery,
} from '@/components/app/ActivityQueueDrawer.helpers';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

export interface ActivityCardProps {
  entry: ActivityQueueEntry;
  onDismiss?: (id: string) => Promise<void>;
  onForceDismiss?: (id: string) => Promise<void>;
  onCancelWaiting?: (id: string) => Promise<boolean>;
  onRecoverPendingHelm?: (id: string) => Promise<void>;
  onKillSession?: (sessionId: number) => Promise<boolean>;
}

// useTickingNow provides a per-component ticking timestamp without
// surfacing it as a prop. Parent re-renders no longer cascade into every
// card every second; only this card re-renders when its own elapsed
// label crosses a second boundary. Active entries tick once a second;
// finished entries are static.
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
            <ActivityStatusIcon status={entry.status} />
            <CommandBadge command={entry.command} />
            <span className="truncate">{activityTargetLabel(entry)}</span>
          </div>
          <div className="text-xs text-muted-foreground truncate">{commandSubtitle(entry)}</div>
        </div>
        <div className="flex flex-none items-center gap-2 text-xs text-muted-foreground">
          <span className="font-mono tabular-nums">{elapsed}</span>
          {dismissAvailable && (
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={dismissLabel}
              title={dismissLabel}
              onClick={handleDismiss}
            >
              <Trash2 aria-hidden="true" className="size-3.5" />
            </Button>
          )}
        </div>
      </header>
      {entry.error && (
        <p className="mt-2 break-words rounded-sm border border-destructive/40 bg-destructive/10 px-2 py-1 text-xs text-destructive">
          {entry.error}
        </p>
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

// RecoveryActionRow renders the per-card recovery affordances. Helm-source
// deploys get a "Clear pending helm release" button; shell-source entries
// get a destructive "Kill shell" button. The row collapses to nothing
// when no recovery applies.
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
