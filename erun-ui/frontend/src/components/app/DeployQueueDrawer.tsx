import * as React from 'react';
import { CheckCircle2, ChevronRight, LoaderCircle, Trash2, Wrench, X } from 'lucide-react';

import { activeDeployForSelection, type DeployQueueContainerStatus, type DeployQueueEntry, formatElapsed, useDeployQueue } from '@/app/deployQueueState';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

type DeployQueueDrawerProps = {
  open: boolean;
  onClose: () => void;
};

const drawerSurfaceClassName =
  'fixed top-[52px] right-0 bottom-0 z-30 flex w-[420px] flex-col border-l bg-background shadow-2xl transition-transform duration-150 ease-out';

const drawerHiddenClassName = 'translate-x-full';

const drawerVisibleClassName = 'translate-x-0';

// DeployQueueDrawer renders the right-side deploy queue as a slide-in sheet.
// Active deploys group at the top with live container status pills; finished
// entries form the history below with a dismiss action per card.
export function DeployQueueDrawer({ open, onClose }: DeployQueueDrawerProps): React.ReactElement {
  const { entries, dismiss } = useDeployQueue();
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
        aria-label="Deploy queue"
        aria-hidden={!open}
      >
        <header className="flex items-center justify-between border-b px-4 py-3">
          <div className="flex items-center gap-2">
            <h2 className="text-sm font-semibold">Deploys</h2>
            <span className="rounded-md bg-muted px-2 py-0.5 text-xs text-muted-foreground" aria-live="polite">
              {activeEntries.length} active
            </span>
          </div>
          <Button type="button" variant="ghost" size="icon" aria-label="Close deploy queue" onClick={onClose}>
            <X aria-hidden="true" className="size-4" />
          </Button>
        </header>
        <div className="flex flex-1 flex-col gap-4 overflow-y-auto p-3" aria-live="polite">
          <DeploySection title="Active" entries={activeEntries} now={now} emptyText="No deploys in progress." />
          <DeploySection
            title="Recent"
            entries={historyEntries}
            now={now}
            emptyText="No recent deploys."
            onDismiss={dismiss}
          />
        </div>
      </aside>
    </>
  );
}

type DeploySectionProps = {
  title: string;
  entries: DeployQueueEntry[];
  now: number;
  emptyText: string;
  onDismiss?: (id: string) => Promise<void>;
};

function DeploySection({ title, entries, now, emptyText, onDismiss }: DeploySectionProps): React.ReactElement {
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
              <DeployCard entry={entry} now={now} onDismiss={onDismiss} />
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

type DeployCardProps = {
  entry: DeployQueueEntry;
  now: number;
  onDismiss?: (id: string) => Promise<void>;
};

function DeployCard({ entry, now, onDismiss }: DeployCardProps): React.ReactElement {
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
            <DeployStatusIcon status={entry.status} />
            <span className="truncate">{deployTargetLabel(entry)}</span>
          </div>
          <div className="text-xs text-muted-foreground">
            {entry.release} · {entry.namespace || '-'} · {entry.kubernetesContext || '-'}
          </div>
        </div>
        <div className="flex flex-none items-center gap-2 text-xs text-muted-foreground">
          <span>{elapsed}</span>
          {onDismiss && (
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label="Dismiss deploy"
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
      {entry.containers && entry.containers.length > 0 && (
        <ContainerStatusList containers={entry.containers} />
      )}
    </article>
  );
}

function ContainerStatusList({ containers }: { containers: DeployQueueContainerStatus[] }): React.ReactElement {
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

function DeployStatusIcon({ status }: { status: DeployQueueEntry['status'] }): React.ReactElement {
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

function cardBorderClassName(status: DeployQueueEntry['status']): string {
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

function deployTargetLabel(entry: DeployQueueEntry): string {
  const target = `${entry.tenant}/${entry.environment}`.trim();
  if (entry.version) {
    return `${target} ${entry.version}`;
  }
  return target;
}

function containerPhaseLabel(container: DeployQueueContainerStatus): string {
  if (container.phase === 'Running' && container.ready) return 'Ready';
  if (container.phase === 'Running' && !container.ready) return 'Running';
  if (container.phase === 'Waiting') return container.reason || 'Waiting';
  if (container.phase === 'Terminated') return container.reason || 'Terminated';
  return container.phase || 'Pending';
}

function containerPhaseClassName(container: DeployQueueContainerStatus): string {
  if (container.phase === 'Running' && container.ready) return 'text-emerald-700';
  if (container.phase === 'Terminated') return 'text-destructive';
  if (container.phase === 'Waiting') return 'text-amber-700';
  return 'text-muted-foreground';
}

function containerPhaseDotClassName(container: DeployQueueContainerStatus): string {
  if (container.phase === 'Running' && container.ready) return 'bg-emerald-500';
  if (container.phase === 'Terminated') return 'bg-destructive';
  if (container.phase === 'Waiting') return 'bg-amber-500';
  return 'bg-muted-foreground';
}

// useDeployButtonGate evaluates whether the deploy button should be enabled
// for the given selection and returns a tooltip explaining any block. Use
// this from the component hosting the deploy action.
export function useDeployButtonGate(tenant: string, environment: string, version: string): { disabled: boolean; tooltip: string; activeEntry: DeployQueueEntry | null } {
  const { entries } = useDeployQueue();
  const active = activeDeployForSelection(entries, tenant, environment);
  if (!active) {
    return { disabled: false, tooltip: '', activeEntry: null };
  }
  if (active.version === version) {
    return {
      disabled: true,
      tooltip: `Already deploying ${deployTargetLabel(active)}`,
      activeEntry: active,
    };
  }
  return {
    disabled: true,
    tooltip: `${deployTargetLabel(active)} is rolling out — wait for it to finish before deploying ${version || 'a different version'}`,
    activeEntry: active,
  };
}
