import { X } from 'lucide-react';
import * as React from 'react';

import {
  type ActivityQueueEntry,
  type ActivityRecoveryResult,
  useActivityQueue,
} from '@/app/activityQueueState';
import { ActivityCard } from '@/components/app/ActivityCard';
import { isHistoryStatus } from '@/components/app/ActivityQueueDrawer.helpers';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

interface ActivityQueueDrawerProps {
  open: boolean;
  onClose: () => void;
}

const drawerSurfaceClassName =
  'fixed top-[52px] right-0 bottom-0 z-30 flex w-[420px] flex-col border-l bg-background shadow-2xl transition-transform duration-150 ease-out';

const drawerHiddenClassName = 'translate-x-full';

const drawerVisibleClassName = 'translate-x-0';

// ActivityQueueDrawer renders the right-side activity queue as a slide-in
// sheet split into three sections:
//
//   - Now: entries currently running plus observational running
//     entries (helm-pending deploys, stale shells).
//   - Next: action entries waiting in the per-env runner queue. The
//     user can Cancel a waiting entry before it starts.
//   - Recent: finished entries (succeeded / failed / skipped /
//     cancelled), capped at 50 newest.
//
// The queue is rebuilt on every desktop launch from real cluster + host
// objects plus desktop actions enqueued in this session: helm releases
// drive observational deploy entries, live PTY sessions drive shell
// entries, and Wails-exported actions register through the action
// runner. There is no cross-restart persistence, so failed activities
// from previous sessions don't reappear.
export function ActivityQueueDrawer({
  open,
  onClose,
}: ActivityQueueDrawerProps): React.ReactElement {
  const { entries, dismiss, forceDismiss, recoverPendingHelm, killSession, cancelWaiting } =
    useActivityQueue();
  const nowEntries = entries.filter((entry) => entry.status === 'running');
  const nextEntries = entries.filter((entry) => entry.status === 'waiting');
  const historyEntries = entries.filter((entry) => isHistoryStatus(entry.status));
  const [recoveryFeedback, setRecoveryFeedback] = React.useState<ActivityRecoveryResult | null>(
    null,
  );

  const dismissAllNow = React.useCallback(async () => {
    await Promise.all(nowEntries.map((entry) => forceDismiss(entry.id)));
  }, [nowEntries, forceDismiss]);
  const cancelAllNext = React.useCallback(async () => {
    await Promise.all(nextEntries.map((entry) => cancelWaiting(entry.id)));
  }, [nextEntries, cancelWaiting]);
  const dismissAllHistory = React.useCallback(async () => {
    await Promise.all(historyEntries.map((entry) => dismiss(entry.id)));
  }, [historyEntries, dismiss]);
  const onRecoverPendingHelm = React.useCallback(
    async (id: string) => {
      const result = await recoverPendingHelm(id);
      setRecoveryFeedback(result);
    },
    [recoverPendingHelm],
  );

  return (
    <>
      {open && (
        <div
          role="presentation"
          className="fixed inset-0 top-[52px] z-20 bg-foreground/10"
          onClick={onClose}
        />
      )}
      <aside
        className={cn(
          drawerSurfaceClassName,
          open ? drawerVisibleClassName : drawerHiddenClassName,
        )}
        role="dialog"
        aria-label="Activity queue"
        aria-hidden={!open}
      >
        <ActivityQueueHeader
          nowCount={nowEntries.length}
          nextCount={nextEntries.length}
          onClose={onClose}
        />
        <ActivityQueueSections
          nowEntries={nowEntries}
          nextEntries={nextEntries}
          historyEntries={historyEntries}
          recoveryFeedback={recoveryFeedback}
          setRecoveryFeedback={setRecoveryFeedback}
          dismiss={dismiss}
          forceDismiss={forceDismiss}
          cancelWaiting={cancelWaiting}
          killSession={killSession}
          onRecoverPendingHelm={onRecoverPendingHelm}
          dismissAllNow={dismissAllNow}
          cancelAllNext={cancelAllNext}
          dismissAllHistory={dismissAllHistory}
        />
      </aside>
    </>
  );
}

function ActivityQueueHeader({
  nowCount,
  nextCount,
  onClose,
}: {
  nowCount: number;
  nextCount: number;
  onClose: () => void;
}): React.ReactElement {
  return (
    <header className="flex items-center justify-between border-b px-4 py-3">
      <div className="flex items-center gap-2">
        <h2 className="text-sm font-semibold">Activities</h2>
        <span
          className="rounded-md bg-muted px-2 py-0.5 text-xs text-muted-foreground"
          aria-live="polite"
        >
          {nowCount} now · {nextCount} next
        </span>
      </div>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        aria-label="Close activity queue"
        onClick={onClose}
      >
        <X aria-hidden="true" className="size-4" />
      </Button>
    </header>
  );
}

interface ActivityQueueSectionsProps {
  nowEntries: ActivityQueueEntry[];
  nextEntries: ActivityQueueEntry[];
  historyEntries: ActivityQueueEntry[];
  recoveryFeedback: ActivityRecoveryResult | null;
  setRecoveryFeedback: (value: ActivityRecoveryResult | null) => void;
  dismiss: (id: string) => Promise<void>;
  forceDismiss: (id: string) => Promise<void>;
  cancelWaiting: (id: string) => Promise<boolean>;
  killSession: (sessionId: number) => Promise<boolean>;
  onRecoverPendingHelm: (id: string) => Promise<void>;
  dismissAllNow: () => Promise<void>;
  cancelAllNext: () => Promise<void>;
  dismissAllHistory: () => Promise<void>;
}

function ActivityQueueSections(props: ActivityQueueSectionsProps): React.ReactElement {
  return (
    <div className="flex flex-1 flex-col gap-4 overflow-y-auto p-3" aria-live="polite">
      {props.recoveryFeedback && (
        <RecoveryFeedback
          result={props.recoveryFeedback}
          onDismiss={() => {
            props.setRecoveryFeedback(null);
          }}
        />
      )}
      <ActivitySection
        title="Now"
        entries={props.nowEntries}
        emptyText="Nothing running."
        onForceDismiss={props.forceDismiss}
        onRecoverPendingHelm={props.onRecoverPendingHelm}
        onKillSession={props.killSession}
        onClearAll={props.nowEntries.length > 1 ? props.dismissAllNow : undefined}
        clearAllLabel="Force dismiss all"
        clearAllHint="Removes every running entry from the queue. Underlying processes are not killed unless you also use Kill on a shell entry."
      />
      <ActivitySection
        title="Next"
        entries={props.nextEntries}
        emptyText="Queue is empty."
        onCancelWaiting={props.cancelWaiting}
        onClearAll={props.nextEntries.length > 1 ? props.cancelAllNext : undefined}
        clearAllLabel="Cancel all"
        clearAllHint="Cancels every queued action that hasn't started yet."
      />
      <ActivitySection
        title="Recent"
        entries={props.historyEntries}
        emptyText="No recent activities."
        onDismiss={props.dismiss}
        onClearAll={props.historyEntries.length > 1 ? props.dismissAllHistory : undefined}
        clearAllLabel="Clear history"
      />
    </div>
  );
}

function RecoveryFeedback({
  result,
  onDismiss,
}: {
  result: ActivityRecoveryResult;
  onDismiss: () => void;
}): React.ReactElement {
  return (
    <section
      role="status"
      className={cn(
        'rounded-md border px-3 py-2 text-xs',
        result.ok
          ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-900'
          : 'border-destructive/40 bg-destructive/10 text-destructive',
      )}
    >
      <div className="flex items-start justify-between gap-2">
        <p className="font-medium">{result.ok ? 'Recovery succeeded' : 'Recovery failed'}</p>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Dismiss recovery message"
          onClick={onDismiss}
        >
          <X aria-hidden="true" className="size-3.5" />
        </Button>
      </div>
      {result.error && <p className="mt-1 break-words font-mono text-[10.5px]">{result.error}</p>}
      {result.output && (
        <pre className="mt-1 max-h-32 overflow-auto whitespace-pre-wrap break-words rounded-sm bg-background/40 p-2 font-mono text-[10.5px] text-foreground">
          {result.output}
        </pre>
      )}
    </section>
  );
}

interface ActivitySectionProps {
  title: string;
  entries: ActivityQueueEntry[];
  emptyText: string;
  onDismiss?: (id: string) => Promise<void>;
  onForceDismiss?: (id: string) => Promise<void>;
  onCancelWaiting?: (id: string) => Promise<boolean>;
  onRecoverPendingHelm?: (id: string) => Promise<void>;
  onKillSession?: (sessionId: number) => Promise<boolean>;
  onClearAll?: () => Promise<void>;
  clearAllLabel?: string;
  clearAllHint?: string;
}

function ActivitySection({
  title,
  entries,
  emptyText,
  onDismiss,
  onForceDismiss,
  onCancelWaiting,
  onRecoverPendingHelm,
  onKillSession,
  onClearAll,
  clearAllLabel,
  clearAllHint,
}: ActivitySectionProps): React.ReactElement {
  return (
    <section aria-labelledby={`activity-section-${title.toLowerCase()}`}>
      <div className="flex items-center justify-between px-1 pb-1.5">
        <h3
          id={`activity-section-${title.toLowerCase()}`}
          className="text-[11px] uppercase tracking-wider text-muted-foreground"
        >
          {title}
        </h3>
        {onClearAll && (
          <Button
            type="button"
            variant="ghost"
            size="xs"
            className="h-5 text-[10px] uppercase tracking-wider text-muted-foreground hover:text-foreground"
            title={clearAllHint}
            onClick={() => {
              void onClearAll();
            }}
          >
            {clearAllLabel ?? 'Clear'}
          </Button>
        )}
      </div>
      {entries.length === 0 ? (
        <p className="rounded-md border border-dashed bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
          {emptyText}
        </p>
      ) : (
        <ul className="space-y-2">
          {entries.map((entry) => (
            <li key={entry.id}>
              <ActivityCard
                entry={entry}
                onDismiss={onDismiss}
                onForceDismiss={onForceDismiss}
                onCancelWaiting={onCancelWaiting}
                onRecoverPendingHelm={onRecoverPendingHelm}
                onKillSession={onKillSession}
              />
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
