import * as React from 'react';
import { CheckCircle2, ChevronRight, LoaderCircle, Trash2, Wrench, X } from 'lucide-react';

import { activeActivityForSelection, type ActivityQueueContainerStatus, type ActivityQueueEntry, formatElapsed, useActivityQueue } from '@/app/activityQueueState';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

type ActivityQueueDrawerProps = {
  open: boolean;
  onClose: () => void;
};

const drawerSurfaceClassName =
  'fixed top-[52px] right-0 bottom-0 z-30 flex w-[420px] flex-col border-l bg-background shadow-2xl transition-transform duration-150 ease-out';

const drawerHiddenClassName = 'translate-x-full';

const drawerVisibleClassName = 'translate-x-0';

// ActivityQueueDrawer renders the right-side deploy queue as a slide-in sheet.
// Active deploys group at the top with live container status pills; finished
// entries form the history below with a dismiss action per card.
export function ActivityQueueDrawer({ open, onClose }: ActivityQueueDrawerProps): React.ReactElement {
  const { entries, dismiss } = useActivityQueue();
  const [now, setNow] = React.useState(() => Date.now());
  React.useEffect(() => {
    if (!open) return undefined;
    const id = window.setInterval(() => setNow(Date.now()), 1_000);
    return () => window.clearInterval(id);
  }, [open]);

  const activeEntries = entries.filter((entry) => entry.status === 'running');
  const historyEntries = entries.filter((entry) => entry.status !== 'running');

  return (
    <>
      {open && <div role="presentation" className="fixed inset-0 top-[52px] z-20 bg-foreground/10" onClick={onClose} />}
      <aside
        className={cn(drawerSurfaceClassName, open ? drawerVisibleClassName : drawerHiddenClassName)}
        role="dialog"
        aria-label="Activity queue"
        aria-hidden={!open}
      >
        <header className="flex items-center justify-between border-b px-4 py-3">
          <div className="flex items-center gap-2">
            <h2 className="text-sm font-semibold">Activities</h2>
            <span className="rounded-md bg-muted px-2 py-0.5 text-xs text-muted-foreground" aria-live="polite">
              {activeEntries.length} active
            </span>
          </div>
          <Button type="button" variant="ghost" size="icon" aria-label="Close activity queue" onClick={onClose}>
            <X aria-hidden="true" className="size-4" />
          </Button>
        </header>
        <div className="flex flex-1 flex-col gap-4 overflow-y-auto p-3" aria-live="polite">
          <ActivitySection title="Active" entries={activeEntries} now={now} emptyText="No activities in progress." />
          <ActivitySection
            title="Recent"
            entries={historyEntries}
            now={now}
            emptyText="No recent activities."
            onDismiss={dismiss}
          />
        </div>
      </aside>
    </>
  );
}

type ActivitySectionProps = {
  title: string;
  entries: ActivityQueueEntry[];
  now: number;
  emptyText: string;
  onDismiss?: (id: string) => Promise<void>;
};

function ActivitySection({ title, entries, now, emptyText, onDismiss }: ActivitySectionProps): React.ReactElement {
  return (
    <section aria-labelledby={`deploy-section-${title.toLowerCase()}`}>
      <h3 id={`deploy-section-${title.toLowerCase()}`} className="px-1 pb-1.5 text-[11px] uppercase tracking-wider text-muted-foreground">
        {title}
      </h3>
      {entries.length === 0 ? (
        <p className="rounded-md border border-dashed bg-muted/40 px-3 py-2 text-xs text-muted-foreground">{emptyText}</p>
      ) : (
        <ul className="space-y-2">
          {entries.map((entry) => (
            <li key={entry.id}>
              <ActivityCard entry={entry} now={now} onDismiss={onDismiss} />
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

type ActivityCardProps = {
  entry: ActivityQueueEntry;
  now: number;
  onDismiss?: (id: string) => Promise<void>;
};

function ActivityCard({ entry, now, onDismiss }: ActivityCardProps): React.ReactElement {
  const elapsed = entry.status === 'running'
    ? formatElapsed(entry.startedAt, now)
    : entry.endedAt
      ? formatElapsed(entry.startedAt, Date.parse(entry.endedAt))
      : formatElapsed(entry.startedAt, now);
  return (
    <article className={cn('rounded-md border bg-card p-3 shadow-sm', cardBorderClassName(entry.status))}>
      <header className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-medium">
            <ActivityStatusIcon status={entry.status} />
            <CommandBadge command={entry.command} />
            <span className="truncate">{activityTargetLabel(entry)}</span>
          </div>
          <div className="text-xs text-muted-foreground truncate">
            {commandSubtitle(entry)}
          </div>
        </div>
        <div className="flex flex-none items-center gap-2 text-xs text-muted-foreground">
          <span>{elapsed}</span>
          {onDismiss && entry.status !== 'running' && (
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label="Dismiss"
              onClick={() => {
                void onDismiss(entry.id);
              }}
            >
              <Trash2 aria-hidden="true" className="size-3.5" />
            </Button>
          )}
        </div>
      </header>
      {entry.error && (
        <p className="mt-2 break-words rounded-sm border border-destructive/30 bg-destructive/10 px-2 py-1 text-xs text-destructive-foreground">
          {entry.error}
        </p>
      )}
      {entry.command === 'deploy' && entry.containers && entry.containers.length > 0 && (
        <ContainerStatusList containers={entry.containers} />
      )}
    </article>
  );
}

function CommandBadge({ command }: { command: string }): React.ReactElement | null {
  if (!command) return null;
  return (
    <span className={cn('rounded-sm px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wider', commandBadgeClassName(command))}>
      {command}
    </span>
  );
}

function commandBadgeClassName(command: string): string {
  switch (command) {
    case 'deploy':
      return 'bg-blue-500/15 text-blue-700';
    case 'build':
      return 'bg-amber-500/15 text-amber-700';
    case 'release':
      return 'bg-purple-500/15 text-purple-700';
    case 'open':
      return 'bg-emerald-500/15 text-emerald-700';
    case 'init':
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

function ContainerStatusList({ containers }: { containers: ActivityQueueContainerStatus[] }): React.ReactElement {
  return (
    <ul className="mt-2 space-y-1">
      {containers.map((container) => (
        <li key={container.name} className="flex items-center justify-between gap-2 rounded-sm bg-muted/40 px-2 py-1 text-[11px]">
          <span className="truncate font-medium">{container.name}</span>
          <span className={cn('flex items-center gap-1 text-[10px] uppercase tracking-wider', containerPhaseClassName(container))}>
            <span aria-hidden="true" className={cn('inline-block size-1.5 rounded-full', containerPhaseDotClassName(container))} />
            {containerPhaseLabel(container)}
            {container.restarts > 0 && <span className="text-muted-foreground">· {container.restarts} restart{container.restarts > 1 ? 's' : ''}</span>}
          </span>
        </li>
      ))}
    </ul>
  );
}

function ActivityStatusIcon({ status }: { status: ActivityQueueEntry['status'] }): React.ReactElement {
  switch (status) {
    case 'running':
      return <LoaderCircle aria-hidden="true" className="size-3.5 animate-spin text-blue-500" />;
    case 'succeeded':
      return <CheckCircle2 aria-hidden="true" className="size-3.5 text-emerald-500" />;
    case 'failed':
      return <Wrench aria-hidden="true" className="size-3.5 text-destructive" />;
    case 'skipped':
      return <ChevronRight aria-hidden="true" className="size-3.5 text-muted-foreground" />;
  }
}

function cardBorderClassName(status: ActivityQueueEntry['status']): string {
  switch (status) {
    case 'running':
      return 'border-blue-500/40';
    case 'succeeded':
      return 'border-emerald-500/40';
    case 'failed':
      return 'border-destructive/50';
    case 'skipped':
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

function containerPhaseClassName(container: ActivityQueueContainerStatus): string {
  if (container.phase === 'Running' && container.ready) return 'text-emerald-700';
  if (container.phase === 'Terminated') return 'text-destructive';
  if (container.phase === 'Waiting') return 'text-amber-700';
  return 'text-muted-foreground';
}

function containerPhaseDotClassName(container: ActivityQueueContainerStatus): string {
  if (container.phase === 'Running' && container.ready) return 'bg-emerald-500';
  if (container.phase === 'Terminated') return 'bg-destructive';
  if (container.phase === 'Waiting') return 'bg-amber-500';
  return 'bg-muted-foreground';
}

// useDeployButtonGate evaluates whether the deploy button should be enabled
// for the given selection and returns a tooltip explaining any block. The
// gate looks at active deploy activities only — a build or release running
// for the same selection does not block a deploy.
export function useDeployButtonGate(tenant: string, environment: string, version: string): { disabled: boolean; tooltip: string; activeEntry: ActivityQueueEntry | null } {
  const { entries } = useActivityQueue();
  const active = entries.find((entry) => entry.status === 'running' && entry.command === 'deploy' && entry.tenant === tenant && entry.environment === environment) ?? null;
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
