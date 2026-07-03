import { environmentTypeIsRemoteWorktree } from '@/app/environmentType';
import type { AppState } from '@/app/state';
import type { UISelection } from '@/types';

export type IdleStatus = NonNullable<AppState['idleStatus']>;

export function ideTooltipLabel(
  ide: string,
  selected: AppState['selected'],
  disabled: boolean,
  notRunning: boolean,
): string {
  if (!selected) {
    return `Select an environment to open in ${ide}`;
  }
  if (notRunning) {
    return `Start the cloud environment before opening ${ide}`;
  }
  if (disabled) {
    return `Enable SSHD in environment settings to open ${ide}`;
  }
  return `Open in ${ide}`;
}

export function isIdeDisabled(selected: UISelection | null, tenants: AppState['tenants']): boolean {
  if (!selected) return true;
  const env = tenants
    .find((tenant) => tenant.name === selected.tenant)
    ?.environments.find((environment) => environment.name === selected.environment);
  if (!env) return true;
  return environmentTypeIsRemoteWorktree(env.type) && env.sshdEnabled !== true;
}

// isEnvOpenedAndRunning reports whether the env behind `selected` is
// in a state that supports remote operations from the titlebar (IDE
// launch, Contribute mode, Contribute app launcher). For local envs
// the answer is always yes — there is no cloud context to gate on.
// For envs with a managed cloud context, the cloud must be in the
// `running` state; `pending`, `stopping`, `stopped`, and unset all
// disable the affordance. Remote envs without a managed cloud context
// stay enabled because the desktop has no signal to gate on and the
// user is responsible for provisioning.
export function isEnvOpenedAndRunning(
  selected: UISelection | null,
  idleStatus: IdleStatus | null,
  tenants: AppState['tenants'],
): boolean {
  if (!selected) return false;
  const env = tenants
    .find((tenant) => tenant.name === selected.tenant)
    ?.environments.find((environment) => environment.name === selected.environment);
  if (!env) return false;
  if (!environmentTypeIsRemoteWorktree(env.type)) return true;
  if (idleStatus?.managedCloud) {
    return (idleStatus.cloudContextStatus ?? '').trim().toLowerCase() === 'running';
  }
  return true;
}

export function idleCloudDisplayName(idleStatus: IdleStatus, fallback: string): string {
  const trimmedLabel = idleStatus.cloudContextLabel?.trim() ?? '';
  return trimmedLabel !== '' ? trimmedLabel : fallback;
}

// idleStopPending reports whether an auto-stop is currently armed for
// the env (the desktop has seen StopEligible=true and started the
// grace-period warning). The frontend uses this to swap the idle
// pill for the amber warning pill.
export function idleStopPending(idleStatus: IdleStatus): boolean {
  return Boolean((idleStatus.stopPendingSince ?? '').trim());
}

// formatGraceCountdown renders the remaining seconds in the
// grace-period warning as a short, glanceable string. Matches the
// style of the existing "idle Xs" pill.
export function formatGraceCountdown(seconds: number): string {
  const remaining = Math.max(0, Math.floor(seconds));
  if (remaining < 60) {
    return `in ${String(remaining)}s`;
  }
  const minutes = Math.floor(remaining / 60);
  const rem = remaining % 60;
  if (rem === 0) {
    return `in ${String(minutes)}m`;
  }
  return `in ${String(minutes)}m ${String(rem)}s`;
}

export function idleCloudAction(
  idleStatus: IdleStatus,
  busy: boolean,
): { action: 'start' | 'stop'; label: string; busy: boolean } | null {
  const name = idleStatus.cloudContextName?.trim();
  if (!idleStatus.managedCloud || !name) {
    return null;
  }
  const displayName = idleCloudDisplayName(idleStatus, name);
  const running = idleStatus.cloudContextStatus?.trim().toLowerCase() === 'running';
  const verbActive = running ? 'Stopping' : 'Starting';
  const verbIdle = running ? 'Stop' : 'Start';
  return {
    action: running ? 'stop' : 'start',
    label: busy ? `${verbActive} ${displayName}` : `${verbIdle} ${displayName}`,
    busy,
  };
}

export function idleStatusBadge(idleStatus: IdleStatus): { label: string; className: string } {
  if (idleStatus.stopError) {
    return {
      label: 'stop failed',
      className: 'border-destructive/60 text-destructive',
    };
  }
  if (idleStatus.stopEligible) {
    if (idleStatus.outsideWorkingHours) {
      return {
        label: 'outside hours',
        className: 'border-[oklch(0.72_0.12_150)] text-[oklch(0.42_0.13_150)]',
      };
    }
    return {
      label: 'idle ready',
      className: 'border-[oklch(0.72_0.12_150)] text-[oklch(0.42_0.13_150)]',
    };
  }
  if (
    idleStatus.stopBlockedReason &&
    (idleStatus.secondsUntilStop <= 0 || isPersistentIdleBlocker(idleStatus.stopBlockedReason))
  ) {
    return {
      label: 'idle blocked',
      className: 'border-[oklch(0.76_0.16_65)] text-[oklch(0.48_0.13_65)]',
    };
  }
  return {
    label: `idle ${String(idleStatus.secondsUntilStop)}s`,
    className: 'border-border text-muted-foreground',
  };
}

function isPersistentIdleBlocker(reason: string): boolean {
  return reason.includes('working-hours') || reason.includes('not cloud-managed');
}

export function idleStatusTooltipLines(idleStatus: IdleStatus): string[] {
  const lines = idleStatusSummaryLines(idleStatus);
  appendIdleBlockerLine(lines, idleStatus);
  appendIdleCloudContextLine(lines, idleStatus);
  lines.push(...idleStatusActiveMarkerLines(idleStatus));
  appendIdleStopOutcomeLines(lines, idleStatus);
  return lines;
}

function idleStatusSummaryLines(idleStatus: IdleStatus): string[] {
  return [
    `Idle timeout: ${String(idleStatus.timeoutSeconds)}s`,
    `Seconds until stop: ${String(idleStatus.secondsUntilStop)}s`,
    `Stop eligible: ${idleStatus.stopEligible ? 'yes' : 'no'}`,
    `Working hours: ${idleStatus.outsideWorkingHours ? 'outside; autostop overrides activity' : 'inside; idle timeout applies'}`,
  ];
}

function appendIdleBlockerLine(lines: string[], idleStatus: IdleStatus): void {
  if (idleStatus.stopBlockedReason) {
    lines.push(`Blocked: ${idleStatus.stopBlockedReason}`);
  } else if (!idleStatus.managedCloud) {
    lines.push('Blocked: environment is not cloud-managed');
  }
}

function appendIdleCloudContextLine(lines: string[], idleStatus: IdleStatus): void {
  if (idleStatus.cloudContextName) {
    const label = idleStatus.cloudContextLabel?.trim()
      ? idleStatus.cloudContextLabel
      : idleStatus.cloudContextName;
    lines.push(
      `Cloud environment: ${label}${idleStatus.cloudContextStatus ? ` (${idleStatus.cloudContextStatus})` : ''}`,
    );
  }
}

function idleStatusActiveMarkerLines(idleStatus: IdleStatus): string[] {
  const activeMarkers = (idleStatus.markers ?? []).filter(
    (marker) => marker.name !== 'working-hours' && !marker.idle,
  );
  if (activeMarkers.length === 0) {
    return [];
  }
  const lines = ['Active markers:'];
  for (const marker of activeMarkers) {
    lines.push(idleStatusActiveMarkerLine(marker));
    lines.push(...idleStatusActiveMarkerClientLines(marker));
  }
  return lines;
}

function idleStatusActiveMarkerLine(marker: NonNullable<IdleStatus['markers']>[number]): string {
  const remaining =
    marker.secondsRemaining && marker.secondsRemaining > 0
      ? `, ${String(marker.secondsRemaining)}s remaining`
      : '';
  return `- ${marker.name}${marker.reason ? `: ${marker.reason}` : ''}${remaining}`;
}

// idleStatusActiveMarkerClientLines emits "  - <address>, <bytes>, <ago>"
// for each per-peer entry attached to the marker. The leading two spaces
// keep these visually nested under their parent "- <name>: ..." line; the
// Titlebar tooltip JSX leaves the prefix alone but already applies the
// `pl-2` indent class to any line beginning with "- ".
function idleStatusActiveMarkerClientLines(
  marker: NonNullable<IdleStatus['markers']>[number],
): string[] {
  if (!marker.clients || marker.clients.length === 0) {
    return [];
  }
  return marker.clients.map((client) => {
    const bytes = client.bytes && client.bytes > 0 ? `, ${formatBytes(client.bytes)}` : '';
    const ago = formatSecondsAgo(client.secondsAgo);
    return `  - ${client.address}${bytes}${ago}`;
  });
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) {
    return `${String(bytes)} B`;
  }
  const kib = bytes / 1024;
  if (kib < 1024) {
    return `${kib.toFixed(kib < 10 ? 1 : 0)} KiB`;
  }
  const mib = kib / 1024;
  if (mib < 1024) {
    return `${mib.toFixed(mib < 10 ? 1 : 0)} MiB`;
  }
  const gib = mib / 1024;
  return `${gib.toFixed(gib < 10 ? 1 : 0)} GiB`;
}

function formatSecondsAgo(seconds: number | undefined): string {
  if (seconds === undefined || seconds < 0) {
    return '';
  }
  if (seconds < 60) {
    return `, ${String(seconds)}s ago`;
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return `, ${String(minutes)}m ago`;
  }
  const hours = Math.floor(minutes / 60);
  return `, ${String(hours)}h ago`;
}

function appendIdleStopOutcomeLines(lines: string[], idleStatus: IdleStatus): void {
  if (idleStatus.stopEligible) {
    lines.push('Autostop is ready.');
  }
  if (idleStatus.stopError) {
    lines.push('Stop error:', idleStatus.stopError);
  }
}

export function idleStatusAccessibleLabel(idleStatus: IdleStatus): string {
  const parts = [
    `Idle timeout ${String(idleStatus.timeoutSeconds)} seconds`,
    `seconds until stop ${String(idleStatus.secondsUntilStop)}`,
    `stop eligible ${idleStatus.stopEligible ? 'yes' : 'no'}`,
    idleStatus.outsideWorkingHours ? 'outside working hours' : 'inside working hours',
  ];
  if (idleStatus.stopBlockedReason) {
    parts.push(`blocked: ${idleStatus.stopBlockedReason}`);
  }
  if (idleStatus.stopError) {
    parts.push(`stop error: ${idleStatus.stopError}`);
  }
  if (idleStatus.cloudContextName) {
    parts.push(
      `cloud environment ${idleStatus.cloudContextLabel?.trim() ? idleStatus.cloudContextLabel : (idleStatus.cloudContextName ?? '')}${idleStatus.cloudContextStatus ? ` ${idleStatus.cloudContextStatus}` : ''}`,
    );
  }
  return parts.join(', ');
}
