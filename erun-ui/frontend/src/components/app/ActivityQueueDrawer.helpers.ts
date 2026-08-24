import {
  type ActivityQueueContainerStatus,
  type ActivityQueueEntry,
  formatElapsed,
} from '@/app/activityQueueState';
import type { UISelection } from '@/types';

// deployUiSelection builds the uiSelection a deploy recovery action needs from an
// activity entry; fields left empty are resolved by the backend from the env config.
export function deployUiSelection(entry: ActivityQueueEntry): UISelection {
  return {
    tenant: entry.tenant,
    environment: entry.environment,
    version: entry.version ?? '',
    runtimeImage: '',
    runtimeCpu: '',
    runtimeMemory: '',
    kubernetesContext: entry.kubernetesContext ?? '',
    containerRegistry: '',
    noGit: false,
    setDefaultTenant: false,
  };
}

export function isHistoryStatus(status: ActivityQueueEntry['status']): boolean {
  return (
    status === 'succeeded' || status === 'failed' || status === 'skipped' || status === 'cancelled'
  );
}

// A visible, readable-by-assistive-tech label for a status that is otherwise
// conveyed only by an aria-hidden icon and a border color (WCAG 1.4.1).
export function activityStatusLabel(status: ActivityQueueEntry['status']): string {
  switch (status) {
    case 'waiting':
      return 'Waiting';
    case 'running':
      return 'Running';
    case 'succeeded':
      return 'Succeeded';
    case 'failed':
      return 'Failed';
    case 'skipped':
      return 'Skipped';
    case 'cancelled':
      return 'Cancelled';
  }
}

export function activityElapsedLabel(entry: ActivityQueueEntry, now: number): string {
  const isRunning = entry.status === 'running';
  const isWaiting = entry.status === 'waiting';
  if (isRunning || isWaiting) {
    const elapsedAnchor = isWaiting
      ? (entry.enqueuedAt ?? entry.startedAt)
      : (entry.startedRunningAt ?? entry.startedAt);
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
      return 'bg-blue-500/15 text-blue-700';
    case 'build':
      // Amber is build's badge color, not a status warning — status has its own border + icon.
      return 'bg-amber-500/15 text-amber-700';
    case 'release':
      return 'bg-purple-500/15 text-purple-700';
    case 'open':
      // Long-running session, not a success — sky avoids the green/READY
      // collision that made open shells look like succeeded deploys.
      return 'bg-sky-500/15 text-sky-700';
    case 'init':
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

// Kubelet reasons that mean the container is stuck in a failure, not
// legitimately waiting (e.g. ContainerCreating), so the UI can flag them.
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

function failureReportContextLines(entry: ActivityQueueEntry): string[] {
  const lines = [
    `erun ${entry.command || 'command'} failed`,
    `Target: ${entry.tenant}/${entry.environment}`,
  ];
  const optional: [string, string | undefined][] = [
    ['Version', entry.version],
    ['Release', entry.release],
    ['Namespace', entry.namespace],
    ['Kubernetes context', entry.kubernetesContext],
  ];
  for (const [label, value] of optional) {
    if (value) lines.push(`${label}: ${value}`);
  }
  lines.push(`Started: ${entry.startedAt}`);
  if (entry.endedAt) lines.push(`Ended: ${entry.endedAt}`);
  lines.push(`Elapsed: ${activityElapsedLabel(entry, Date.now()).trim()}`);
  return lines;
}

function failureReportContainerLine(container: ActivityQueueContainerStatus): string {
  const parts = [`${container.name}: ${containerPhaseLabel(container)}`];
  if (container.restarts > 0) {
    parts.push(`${String(container.restarts)} restart${container.restarts > 1 ? 's' : ''}`);
  }
  if (container.reason) parts.push(`reason: ${container.reason}`);
  const row = `  - ${parts.join(', ')}`;
  return container.message ? `${row}\n      ${container.message}` : row;
}

function failureReportContainerLines(entry: ActivityQueueEntry): string[] {
  if (!entry.containers || entry.containers.length === 0) return [];
  return ['', 'Containers:', ...entry.containers.map(failureReportContainerLine)];
}

// buildFailureReport assembles a paste-ready report so a user can hand a deploy
// failure to developers/admins without scraping the terminal.
export function buildFailureReport(entry: ActivityQueueEntry): string {
  const lines = [...failureReportContextLines(entry), ...failureReportContainerLines(entry)];
  if (entry.error) lines.push('', `Error: ${entry.error}`);
  if (entry.detail) lines.push('', 'Output:', entry.detail);
  return lines.join('\n');
}

export async function copyToClipboard(text: string): Promise<void> {
  try {
    // navigator.clipboard is typed as always-present but can be missing in Wails embeddings.
    const nav = navigator as Omit<Navigator, 'clipboard'> & { clipboard?: Clipboard };
    if (nav.clipboard !== undefined) {
      await nav.clipboard.writeText(text);
    }
  } catch {
    /* best-effort copy; ignore failures */
  }
}
