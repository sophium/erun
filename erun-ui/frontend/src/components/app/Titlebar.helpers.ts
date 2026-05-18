import type { AppState } from '@/app/state';
import type { UISelection } from '@/types';

export type IdleStatus = NonNullable<AppState['idleStatus']>;

export function ideTooltipLabel(
  ide: string,
  selected: AppState['selected'],
  disabled: boolean,
): string {
  if (!selected) {
    return `Select an environment to open in ${ide}`;
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
  return env?.remote !== false && env?.sshdEnabled !== true;
}

export function idleCloudDisplayName(idleStatus: IdleStatus, fallback: string): string {
  const trimmedLabel = idleStatus.cloudContextLabel?.trim() ?? '';
  return trimmedLabel !== '' ? trimmedLabel : fallback;
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
  return ['Active markers:', ...activeMarkers.map(idleStatusActiveMarkerLine)];
}

function idleStatusActiveMarkerLine(marker: NonNullable<IdleStatus['markers']>[number]): string {
  const remaining =
    marker.secondsRemaining && marker.secondsRemaining > 0
      ? `, ${String(marker.secondsRemaining)}s remaining`
      : '';
  return `- ${marker.name}${marker.reason ? `: ${marker.reason}` : ''}${remaining}`;
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
