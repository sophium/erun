import {
  AlertOctagon,
  Ban,
  CheckCircle2,
  ChevronRight,
  Clock,
  LoaderCircle,
  Trash2,
  X,
} from 'lucide-react';
import * as React from 'react';

import {
  type ActivityQueueContainerStatus,
  type ActivityQueueEntry,
  type ActivityRecoveryResult,
  formatElapsed,
  useActivityQueue,
} from '@/app/activityQueueState';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

import { StartForceDeploySession } from '../../../wailsjs/go/main/App';
import type { main as wailsMain } from '../../../wailsjs/go/models';

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
  const historyEntries = entries.filter(
    (entry) =>
      entry.status === 'succeeded' ||
      entry.status === 'failed' ||
      entry.status === 'skipped' ||
      entry.status === 'cancelled',
  );
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
        <header className="flex items-center justify-between border-b px-4 py-3">
          <div className="flex items-center gap-2">
            <h2 className="text-sm font-semibold">Activities</h2>
            <span
              className="rounded-md bg-muted px-2 py-0.5 text-xs text-muted-foreground"
              aria-live="polite"
            >
              {nowEntries.length} now · {nextEntries.length} next
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
        <div className="flex flex-1 flex-col gap-4 overflow-y-auto p-3" aria-live="polite">
          {recoveryFeedback && (
            <RecoveryFeedback
              result={recoveryFeedback}
              onDismiss={() => {
                setRecoveryFeedback(null);
              }}
            />
          )}
          <ActivitySection
            title="Now"
            entries={nowEntries}
            emptyText="Nothing running."
            onForceDismiss={forceDismiss}
            onRecoverPendingHelm={onRecoverPendingHelm}
            onKillSession={killSession}
            onClearAll={nowEntries.length > 1 ? dismissAllNow : undefined}
            clearAllLabel="Force dismiss all"
            clearAllHint="Removes every running entry from the queue. Underlying processes are not killed unless you also use Kill on a shell entry."
          />
          <ActivitySection
            title="Next"
            entries={nextEntries}
            emptyText="Queue is empty."
            onCancelWaiting={cancelWaiting}
            onClearAll={nextEntries.length > 1 ? cancelAllNext : undefined}
            clearAllLabel="Cancel all"
            clearAllHint="Cancels every queued action that hasn't started yet."
          />
          <ActivitySection
            title="Recent"
            entries={historyEntries}
            emptyText="No recent activities."
            onDismiss={dismiss}
            onClearAll={historyEntries.length > 1 ? dismissAllHistory : undefined}
            clearAllLabel="Clear history"
          />
        </div>
      </aside>
    </>
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

interface ActivityCardProps {
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

const ActivityCard = React.memo(function ActivityCard({
  entry,
  onDismiss,
  onForceDismiss,
  onCancelWaiting,
  onRecoverPendingHelm,
  onKillSession,
}: ActivityCardProps): React.ReactElement {
  const isRunning = entry.status === 'running';
  const isWaiting = entry.status === 'waiting';
  const tickingActive = isRunning || isWaiting;
  const now = useTickingNow(tickingActive);
  const elapsedAnchor = isWaiting && entry.enqueuedAt ? entry.enqueuedAt : entry.startedAt;
  const elapsed = tickingActive
    ? formatElapsed(elapsedAnchor, now)
    : entry.endedAt
      ? formatElapsed(entry.startedAt, Date.parse(entry.endedAt))
      : formatElapsed(entry.startedAt, Date.parse(entry.lastUpdated));
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
  let dismissAvailable: boolean;
  let dismissLabel: string;
  if (isWaiting) {
    dismissAvailable = Boolean(onCancelWaiting);
    dismissLabel = 'Cancel — removes this entry from the queue before it starts.';
  } else if (isRunning) {
    dismissAvailable = Boolean(onForceDismiss);
    dismissLabel = 'Force dismiss (entry only — does not stop the underlying process)';
  } else {
    dismissAvailable = Boolean(onDismiss);
    dismissLabel = 'Dismiss';
  }
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

function shouldShowHelmRecovery(
  entry: ActivityQueueEntry,
  onRecover?: (id: string) => Promise<void>,
): boolean {
  if (!onRecover) return false;
  if (entry.source !== 'helm') return false;
  return Boolean(entry.release && entry.namespace);
}

function shellSessionIdFromEntry(entry: ActivityQueueEntry): number {
  const parsed = entry.sessionId ? Number(entry.sessionId) : NaN;
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

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

function commandBadgeClassName(command: string): string {
  switch (command) {
    case 'deploy':
      // Primary mutating operation — saturated blue stands out as the
      // most consequential card type.
      return 'bg-blue-500/15 text-blue-700';
    case 'build':
      // Long-running prep — amber differentiates from deploy without
      // implying status (success/failure has its own border + icon).
      return 'bg-amber-500/15 text-amber-700';
    case 'release':
      // Rare and high-stakes — distinct purple keeps it visually
      // separate from the routine commands.
      return 'bg-purple-500/15 text-purple-700';
    case 'open':
      // Long-running session, not a success — sky avoids the green/READY
      // collision that made open shells look like succeeded deploys.
      return 'bg-sky-500/15 text-sky-700';
    case 'init':
      // One-shot bootstrap — neutral slate.
      return 'bg-slate-500/15 text-slate-700';
    default:
      return 'bg-muted text-muted-foreground';
  }
}

function commandSubtitle(entry: ActivityQueueEntry): string {
  const parts: string[] = [];
  if (entry.command === 'deploy') {
    if (entry.release) parts.push(entry.release);
    if (entry.namespace) parts.push(entry.namespace);
    if (entry.kubernetesContext) parts.push(entry.kubernetesContext);
  } else if (entry.command === 'build') {
    if (entry.component) parts.push(entry.component);
    if (entry.image) parts.push(entry.image);
    if (entry.summary) parts.push(entry.summary);
  } else if (entry.command === 'release') {
    if (entry.version) parts.push(entry.version);
    if (entry.summary) parts.push(entry.summary);
  } else if (entry.summary) {
    parts.push(entry.summary);
  }
  return parts.length > 0 ? parts.join(' · ') : '—';
}

function ContainerStatusList({
  containers,
  deploy,
}: {
  containers: ActivityQueueContainerStatus[];
  deploy: ActivityQueueEntry;
}): React.ReactElement {
  return (
    <ul className="mt-2 space-y-1">
      {containers.map((container) => (
        <li key={container.name}>
          <ContainerStatusRow container={container} deploy={deploy} />
        </li>
      ))}
    </ul>
  );
}

function ContainerStatusRow({
  container,
  deploy,
}: {
  container: ActivityQueueContainerStatus;
  deploy: ActivityQueueEntry;
}): React.ReactElement {
  const failing = containerIsFailing(container);
  const [expanded, setExpanded] = React.useState<boolean>(failing);
  // Failing containers default to expanded so the user sees the cause
  // without an extra click; user can still collapse and re-expand.
  React.useEffect(() => {
    setExpanded(failing);
  }, [failing]);

  const hasDetails = Boolean(container.image || container.message || container.reason);
  return (
    <div
      className={cn(
        'rounded-sm border border-transparent bg-muted/40 px-2 py-1',
        failing && 'border-destructive/30',
      )}
    >
      <button
        type="button"
        className="flex w-full items-center justify-between gap-2 text-left text-[11px] outline-none focus-visible:ring-2 focus-visible:ring-ring rounded-sm"
        aria-expanded={expanded}
        aria-controls={`container-detail-${container.name}`}
        disabled={!hasDetails}
        onClick={() => {
          setExpanded((value) => !value);
        }}
      >
        <span className="flex items-center gap-1 truncate font-medium">
          {hasDetails && (
            <ChevronRight
              aria-hidden="true"
              className={cn(
                'size-3 transition-transform text-muted-foreground',
                expanded && 'rotate-90',
              )}
            />
          )}
          <span className="truncate">{container.name}</span>
        </span>
        <span
          className={cn(
            'flex flex-none items-center gap-1 text-[10px] uppercase tracking-wider',
            containerPhaseClassName(container),
          )}
        >
          <span
            aria-hidden="true"
            className={cn(
              'inline-block size-1.5 rounded-full',
              containerPhaseDotClassName(container),
            )}
          />
          {containerPhaseLabel(container)}
          {container.restarts > 0 && (
            <span className="text-muted-foreground">
              · {container.restarts} restart{container.restarts > 1 ? 's' : ''}
            </span>
          )}
        </span>
      </button>
      {expanded && hasDetails && (
        <ContainerStatusDetail
          id={`container-detail-${container.name}`}
          container={container}
          deploy={deploy}
        />
      )}
    </div>
  );
}

function ContainerStatusDetail({
  id,
  container,
  deploy,
}: {
  id: string;
  container: ActivityQueueContainerStatus;
  deploy: ActivityQueueEntry;
}): React.ReactElement {
  const recovery = recoveryActionForContainer(container, deploy);
  return (
    <dl
      id={id}
      className="mt-1 grid grid-cols-[max-content_1fr] gap-x-2 gap-y-0.5 text-[11px] text-muted-foreground"
    >
      {container.image && (
        <>
          <dt className="font-medium text-foreground">Image</dt>
          <dd className="break-all font-mono text-[10.5px]">{container.image}</dd>
        </>
      )}
      {container.reason && (
        <>
          <dt className="font-medium text-foreground">Reason</dt>
          <dd
            className={cn(
              'font-mono text-[10.5px]',
              containerIsFailing(container) && 'text-destructive',
            )}
          >
            {container.reason}
          </dd>
        </>
      )}
      {container.message && (
        <>
          <dt className="font-medium text-foreground">Message</dt>
          <dd className="whitespace-pre-wrap break-words">{container.message}</dd>
        </>
      )}
      <dt className="font-medium text-foreground">Inspect</dt>
      <dd className="break-all font-mono text-[10px]">
        <code className="text-foreground">{kubectlDescribeCommand(container, deploy)}</code>
        <Button
          type="button"
          variant="ghost"
          size="xs"
          className="ml-1 h-5 px-1 text-[10px]"
          onClick={() => {
            void copyToClipboard(kubectlDescribeCommand(container, deploy));
          }}
        >
          Copy
        </Button>
      </dd>
      {recovery && (
        <>
          <dt className="font-medium text-foreground">Recover</dt>
          <dd className="space-y-1">
            <p className="text-[11px] text-muted-foreground">{recovery.hint}</p>
            <Button
              type="button"
              variant="default"
              size="xs"
              className="h-6 text-[11px]"
              onClick={() => {
                void recovery.action();
              }}
            >
              {recovery.label}
            </Button>
          </dd>
        </>
      )}
    </dl>
  );
}

interface recoveryAction {
  hint: string;
  label: string;
  action: () => Promise<void>;
}

// recoveryActionForContainer returns a one-click recovery affordance when
// the container's failure mode has a known mitigation. The most common
// case is a registry miss: the kubelet message contains "not found"
// against an image tag the chart references, which usually means the
// chart bumped the tag without publishing it. `erun deploy --force`
// rebuilds every image bypassing the fingerprint cache and pushes them
// to the registry, so the missing tag becomes available.
function recoveryActionForContainer(
  container: ActivityQueueContainerStatus,
  deploy: ActivityQueueEntry,
): recoveryAction | null {
  if (!containerIsFailing(container)) return null;
  const message = (container.message ?? '').toLowerCase();
  const reason = (container.reason ?? '').toLowerCase();
  const looksLikeMissingImage =
    reason === 'imagepullbackoff' ||
    reason === 'errimagepull' ||
    message.includes('not found') ||
    message.includes('manifest unknown');
  if (!looksLikeMissingImage) return null;
  const selection: wailsMain.uiSelection = {
    tenant: deploy.tenant,
    environment: deploy.environment,
    version: deploy.version ?? '',
    runtimeImage: '',
    runtimeCpu: '',
    runtimeMemory: '',
    kubernetesContext: deploy.kubernetesContext ?? '',
    containerRegistry: '',
    noGit: false,
    bootstrap: false,
    setDefaultTenant: false,
    action: 'deploy',
    debug: false,
  };
  return {
    hint: `${container.image || 'The image referenced by the chart'} is not in the registry. Rebuild every image bypassing the fingerprint cache and push them.`,
    label: 'Rebuild & redeploy',
    action: async () => {
      await StartForceDeploySession(selection, 120, 34);
    },
  };
}

function kubectlDescribeCommand(
  container: ActivityQueueContainerStatus,
  deploy: ActivityQueueEntry,
): string {
  const parts = ['kubectl'];
  const ctx = deploy.kubernetesContext?.trim();
  if (ctx) {
    parts.push('--context', ctx);
  }
  const ns = deploy.namespace?.trim();
  if (ns) {
    parts.push('-n', ns);
  }
  const release = deploy.release?.trim();
  if (release) {
    parts.push('describe', 'pod', '-l', `app=${release}`);
  } else {
    parts.push('describe', 'pod');
  }
  parts.push(`# container ${container.name}`);
  return parts.join(' ');
}

async function copyToClipboard(text: string): Promise<void> {
  try {
    await navigator.clipboard?.writeText(text);
  } catch {
    /* clipboard API may not be available in some Wails wraps; ignore */
  }
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

function cardBorderClassName(status: ActivityQueueEntry['status']): string {
  switch (status) {
    case 'waiting':
      return 'border-muted-foreground/30';
    case 'running':
      return 'border-blue-500/40';
    case 'succeeded':
      return 'border-emerald-500/40';
    case 'failed':
      return 'border-destructive/50';
    case 'skipped':
      return 'border-muted';
    case 'cancelled':
      return 'border-muted';
  }
}

function activityTargetLabel(entry: ActivityQueueEntry): string {
  const target = `${entry.tenant}/${entry.environment}`.trim();
  if (entry.version) {
    return `${target} ${entry.version}`;
  }
  return target;
}

function containerPhaseLabel(container: ActivityQueueContainerStatus): string {
  if (container.phase === 'Running' && container.ready) return 'Ready';
  if (container.phase === 'Running' && !container.ready) return 'Running';
  if (container.phase === 'Waiting') return container.reason || 'Waiting';
  if (container.phase === 'Terminated') return container.reason || 'Terminated';
  return container.phase || 'Pending';
}

// failingContainerReasons are kubelet-reported reasons that mean the
// container is stuck in a failure mode rather than legitimately waiting
// (e.g. ContainerCreating). They render red so the user spots the bad
// container at a glance instead of mistaking IMAGEPULLBACKOFF for normal
// progress.
const failingContainerReasons = new Set([
  'ImagePullBackOff',
  'ErrImagePull',
  'CrashLoopBackOff',
  'CreateContainerConfigError',
  'CreateContainerError',
  'InvalidImageName',
  'OOMKilled',
  'Error',
  'RunContainerError',
]);

function containerIsFailing(container: ActivityQueueContainerStatus): boolean {
  if (container.phase === 'Terminated') return true;
  return failingContainerReasons.has((container.reason ?? '').trim());
}

function containerPhaseClassName(container: ActivityQueueContainerStatus): string {
  if (containerIsFailing(container)) return 'text-destructive';
  if (container.phase === 'Running' && container.ready) return 'text-emerald-700';
  if (container.phase === 'Waiting') return 'text-amber-700';
  return 'text-muted-foreground';
}

function containerPhaseDotClassName(container: ActivityQueueContainerStatus): string {
  if (containerIsFailing(container)) return 'bg-destructive';
  if (container.phase === 'Running' && container.ready) return 'bg-emerald-500';
  if (container.phase === 'Waiting') return 'bg-amber-500';
  return 'bg-muted-foreground';
}

// useDeployButtonGate evaluates whether the deploy button should be enabled
// for the given selection and returns a tooltip explaining any block. The
// gate looks at active deploy activities only — a build or release running
// for the same selection does not block a deploy.
export function useDeployButtonGate(
  tenant: string,
  environment: string,
  version: string,
): { disabled: boolean; tooltip: string; activeEntry: ActivityQueueEntry | null } {
  const { entries } = useActivityQueue();
  const active =
    entries.find(
      (entry) =>
        entry.status === 'running' &&
        entry.command === 'deploy' &&
        entry.tenant === tenant &&
        entry.environment === environment,
    ) ?? null;
  if (!active) {
    return { disabled: false, tooltip: '', activeEntry: null };
  }
  if (active.version === version) {
    return {
      disabled: true,
      tooltip: `Already deploying ${activityTargetLabel(active)}`,
      activeEntry: active,
    };
  }
  return {
    disabled: true,
    tooltip: `${activityTargetLabel(active)} is rolling out — wait for it to finish before deploying ${version || 'a different version'}`,
    activeEntry: active,
  };
}
