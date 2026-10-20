import type { StatusBadgeTone } from 'erun-kit';

import type { UIEnvironmentConfig } from '@/types';

export function isClaudeOverridden(claude: UIEnvironmentConfig['claude']): boolean {
  return hasClaudeTriStateOverride(claude) || hasClaudeTypedOverride(claude);
}

// The tri-state overrides carry their own "inherit" value, so any of them being
// set at all is an override.
function hasClaudeTriStateOverride(claude: UIEnvironmentConfig['claude']): boolean {
  return (
    claude.useMantle !== undefined ||
    claude.useBedrock !== undefined ||
    claude.useGateway !== undefined ||
    claude.maxOutputTokens !== undefined ||
    claude.effort !== undefined ||
    claude.defaultModel !== undefined ||
    claude.verboseDebug === true
  );
}

// A list has no "inherit" spelling: empty means inherit.
function hasClaudeTypedOverride(claude: UIEnvironmentConfig['claude']): boolean {
  return (claude.models?.length ?? 0) > 0;
}

// effectiveClaudeModelDefaults resolves which model set the AI tab offers,
// mirroring erun-common EffectiveGateway and resolveClaudeLaunchModel: a
// configured catalog applies unless the environment opted out, and then the
// catalog's models (with its default) are what a launch can select; otherwise
// the environment's own models are, with the first available as the default.
// It reads the draft, so flipping "Use the gateway" changes the choices before
// the save rather than after it.
export function effectiveClaudeModelDefaults(
  claude: UIEnvironmentConfig['claude'],
  defaults: UIEnvironmentConfig['claudeDefaults'],
): {
  viaGateway: boolean;
  available: string[];
  defaultLaunchModel: string;
} {
  const viaGateway = defaults.gatewayConfigured && claude.useGateway !== false;
  if (viaGateway) {
    return {
      viaGateway,
      available: defaults.gatewayModels,
      defaultLaunchModel: defaults.gatewayDefaultModel,
    };
  }
  const available = (claude.models?.length ?? 0) > 0 ? (claude.models ?? []) : defaults.models;
  return { viaGateway, available, defaultLaunchModel: available[0] ?? '' };
}

export function isValidClaudeTokens(
  text: string,
  defaults: UIEnvironmentConfig['claudeDefaults'],
): boolean {
  const trimmed = text.trim();
  if (!/^\d+$/.test(trimmed)) {
    return false;
  }
  const value = Number(trimmed);
  return Number.isFinite(value) && value >= defaults.minTokens && value <= defaults.maxTokens;
}

export function portRangeValue(rangeStart: number, rangeEnd: number): string {
  if (rangeStart <= 0 || rangeEnd <= 0) {
    return '';
  }
  return `${String(rangeStart)}-${String(rangeEnd)}`;
}

export function parseIdleTrafficBytes(value: string): number {
  const parsed = Number(value.trim() || 0);
  return Number.isFinite(parsed) && parsed > 0 ? Math.floor(parsed) : 0;
}

// Keys are the workspaceSyncStatus values set by App.setWorkspaceSyncStatus (erun-ui/workspace_sync.go).
const workspaceSyncStatusTones: Record<string, StatusBadgeTone> = {
  error: 'destructive',
  stopped: 'muted',
  starting: 'in-progress',
  syncing: 'in-progress',
};

export function workspaceSyncStatusTone(status: string): StatusBadgeTone {
  return workspaceSyncStatusTones[status.trim()] ?? 'warning';
}

export function relativeTimeFromNow(timestamp: number): string {
  const seconds = Math.max(0, Math.floor((Date.now() - timestamp) / 1000));
  if (seconds < 60) {
    return 'just now';
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return `${String(minutes)} min ago`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${String(hours)} h ago`;
  }
  const days = Math.floor(hours / 24);
  return `${String(days)} d ago`;
}
