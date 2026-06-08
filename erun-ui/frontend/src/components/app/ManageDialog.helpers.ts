import type { UIEnvironmentConfig } from '@/types';

export const dialogErrorClassName =
  'rounded-[var(--radius)] border border-[color-mix(in_oklch,var(--destructive)_36%,transparent)] bg-[color-mix(in_oklch,var(--destructive)_8%,transparent)] px-[11px] py-[9px] text-[13px] leading-[1.35] text-destructive [overflow-wrap:anywhere]';

export function isClaudeOverridden(claude: UIEnvironmentConfig['claude']): boolean {
  return (
    claude.useMantle !== undefined ||
    claude.useBedrock !== undefined ||
    (claude.models?.length ?? 0) > 0 ||
    claude.maxOutputTokens !== undefined ||
    claude.effort !== undefined
  );
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
