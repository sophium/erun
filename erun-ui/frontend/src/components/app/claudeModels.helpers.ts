import type { UIOpenRouterModel } from '@/uiOpenRouterTypes';

// Mirrors claudeModelTokenPattern in erun-common/ai_launch.go; the launch drops
// anything else silently. A leading '-' would reach `--model` as a flag.
const claudeModelTokenPattern = /^[A-Za-z0-9._:/][A-Za-z0-9._:/-]*$/;

export function isClaudeModelToken(value: string): boolean {
  return claudeModelTokenPattern.test(value.trim());
}

// addClaudeModelId appends id while keeping the existing selection and order.
export function addClaudeModelId({
  base,
  known,
  id,
}: {
  base: string[];
  known: string[];
  id: string;
}): string[] {
  const trimmed = id.trim();
  const set = new Set([...base, trimmed]);
  const ordered = known.filter((entry) => set.has(entry));
  for (const entry of [...base, trimmed]) {
    if (!known.includes(entry) && set.has(entry) && !ordered.includes(entry)) {
      ordered.push(entry);
    }
  }
  return ordered;
}

// selectableClaudeModelIds returns usable catalog ids, excluding malformed rows
// and the reasoning-echo listing no default may name.
export function selectableClaudeModelIds(rows: UIOpenRouterModel[]): string[] {
  return rows
    .filter((row) => !row.requiresReasoningEcho)
    .map((row) => row.id.trim())
    .filter((id) => isClaudeModelToken(id));
}
