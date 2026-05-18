import {
  type ActivityQueueContainerStatus,
  type ActivityQueueEntry,
  formatElapsed,
} from '@/app/activityQueueState';

export function isHistoryStatus(status: ActivityQueueEntry['status']): boolean {
  return (
    status === 'succeeded' || status === 'failed' || status === 'skipped' || status === 'cancelled'
  );
}

export function activityElapsedLabel(entry: ActivityQueueEntry, now: number): string {
  const isRunning = entry.status === 'running';
  const isWaiting = entry.status === 'waiting';
  if (isRunning || isWaiting) {
    const elapsedAnchor = isWaiting && entry.enqueuedAt ? entry.enqueuedAt : entry.startedAt;
    return formatElapsed(elapsedAnchor, now);
  }
  if (entry.endedAt) {
    return formatElapsed(entry.startedAt, Date.parse(entry.endedAt));
  }
  return formatElapsed(entry.startedAt, Date.parse(entry.lastUpdated));
}

export function dismissAffordance(
  status: ActivityQueueEntry['status'],
  onDismiss: ((id: string) => Promise<void>) | undefined,
  onForceDismiss: ((id: string) => Promise<void>) | undefined,
  onCancelWaiting: ((id: string) => Promise<boolean>) | undefined,
): { available: boolean; label: string } {
  if (status === 'waiting') {
    return {
      available: Boolean(onCancelWaiting),
      label: 'Cancel — removes this entry from the queue before it starts.',
    };
  }
  if (status === 'running') {
    return {
      available: Boolean(onForceDismiss),
      label: 'Force dismiss (entry only — does not stop the underlying process)',
    };
  }
  return { available: Boolean(onDismiss), label: 'Dismiss' };
}

export function shouldShowHelmRecovery(
  entry: ActivityQueueEntry,
  onRecover?: (id: string) => Promise<void>,
): boolean {
  if (!onRecover) return false;
  if (entry.source !== 'helm') return false;
  return Boolean(entry.release && entry.namespace);
}

export function shellSessionIdFromEntry(entry: ActivityQueueEntry): number {
  const parsed = entry.sessionId ? Number(entry.sessionId) : NaN;
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

export function commandBadgeClassName(command: string): string {
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

function deploySubtitleParts(entry: ActivityQueueEntry): string[] {
  return [entry.release, entry.namespace, entry.kubernetesContext].filter(
    (value): value is string => Boolean(value),
  );
}

function buildSubtitleParts(entry: ActivityQueueEntry): string[] {
  return [entry.component, entry.image, entry.summary].filter((value): value is string =>
    Boolean(value),
  );
}

function releaseSubtitleParts(entry: ActivityQueueEntry): string[] {
  return [entry.version, entry.summary].filter((value): value is string => Boolean(value));
}

function commandSubtitleParts(entry: ActivityQueueEntry): string[] {
  switch (entry.command) {
    case 'deploy':
      return deploySubtitleParts(entry);
    case 'build':
      return buildSubtitleParts(entry);
    case 'release':
      return releaseSubtitleParts(entry);
    default:
      return entry.summary ? [entry.summary] : [];
  }
}

export function commandSubtitle(entry: ActivityQueueEntry): string {
  const parts = commandSubtitleParts(entry);
  return parts.length > 0 ? parts.join(' · ') : '—';
}

export function activityTargetLabel(entry: ActivityQueueEntry): string {
  const target = `${entry.tenant}/${entry.environment}`.trim();
  if (entry.version) {
    return `${target} ${entry.version}`;
  }
  return target;
}

export function cardBorderClassName(status: ActivityQueueEntry['status']): string {
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

function reasonOrFallback(reason: string | undefined, fallback: string): string {
  return reason?.trim() ? reason : fallback;
}

export function containerPhaseLabel(container: ActivityQueueContainerStatus): string {
  switch (container.phase) {
    case 'Running':
      return container.ready ? 'Ready' : 'Running';
    case 'Waiting':
      return reasonOrFallback(container.reason, 'Waiting');
    case 'Terminated':
      return reasonOrFallback(container.reason, 'Terminated');
    default:
      return container.phase || 'Pending';
  }
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

export function containerIsFailing(container: ActivityQueueContainerStatus): boolean {
  if (container.phase === 'Terminated') return true;
  return failingContainerReasons.has((container.reason ?? '').trim());
}

export function containerPhaseClassName(container: ActivityQueueContainerStatus): string {
  if (containerIsFailing(container)) return 'text-destructive';
  if (container.phase === 'Running' && container.ready) return 'text-emerald-700';
  if (container.phase === 'Waiting') return 'text-amber-700';
  return 'text-muted-foreground';
}

export function containerPhaseDotClassName(container: ActivityQueueContainerStatus): string {
  if (containerIsFailing(container)) return 'bg-destructive';
  if (container.phase === 'Running' && container.ready) return 'bg-emerald-500';
  if (container.phase === 'Waiting') return 'bg-amber-500';
  return 'bg-muted-foreground';
}

function isMissingImageContainer(container: ActivityQueueContainerStatus): boolean {
  const message = (container.message ?? '').toLowerCase();
  const reason = (container.reason ?? '').toLowerCase();
  return (
    reason === 'imagepullbackoff' ||
    reason === 'errimagepull' ||
    message.includes('not found') ||
    message.includes('manifest unknown')
  );
}

export function isRecoverableContainerFailure(container: ActivityQueueContainerStatus): boolean {
  if (!containerIsFailing(container)) return false;
  return isMissingImageContainer(container);
}

export function kubectlDescribeCommand(
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

export async function copyToClipboard(text: string): Promise<void> {
  try {
    // The Clipboard API may not be wired in some Wails embeddings; the cast
    // makes the property optional so the runtime guard below stays honest.
    const nav = navigator as Omit<Navigator, 'clipboard'> & { clipboard?: Clipboard };
    if (nav.clipboard !== undefined) {
      await nav.clipboard.writeText(text);
    }
  } catch {
    /* clipboard API may not be available in some Wails wraps; ignore */
  }
}
