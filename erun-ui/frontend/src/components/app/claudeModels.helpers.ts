// Model-id helpers shared by the per-environment AI tab and the erun-level
// gateway catalog. A model id is an opaque token to erun — resolving one to a
// concrete model is Claude Code's concern — so the only rules that live here
// are the ones a launch genuinely depends on.
import type { UIOpenRouterModel } from '@/uiOpenRouterTypes';

// Mirrors claudeModelTokenPattern in erun-common/ai_launch.go. A value outside
// it reaches the shell as syntax rather than a model name, and the launch drops
// it silently, so it is rejected at the point of entry instead of accepted and
// then ignored.
const claudeModelTokenPattern = /^[A-Za-z0-9._:/-]+$/;

export function isClaudeModelToken(value: string): boolean {
  return claudeModelTokenPattern.test(value.trim());
}

// addClaudeModelId returns the list with id appended: the known entries keep
// their existing order, and anything outside that set follows in the order it
// was added. Appending to the caller's base preserves the entries already
// selected rather than replacing the selection with the new id alone.
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

// selectableClaudeModelIds returns the catalog's usable ids, so a default
// model can never be chosen from a blank or malformed row.
//
// A row declared to require reasoning echo is not one of them: it is exactly
// the model a default must not name, since the default is what an environment
// renders as ANTHROPIC_MODEL and what an exec agent job starts on. Offering it
// here would let the operator pick, as the default, the one listing the catalog
// has already recorded cannot be driven.
export function selectableClaudeModelIds(rows: UIOpenRouterModel[]): string[] {
  return rows
    .filter((row) => !row.requiresReasoningEcho)
    .map((row) => row.id.trim())
    .filter((id) => isClaudeModelToken(id));
}
