import type { StatusBadgeTone } from '../components/StatusBadge.helpers';

// Rung vocabulary shared by the console and desktop pipeline views, which must agree.

// Unknown rungs are kept as the platform spelled them, never renamed.
export type PipelineRung = string;

// PIPELINE_LADDER is the six rungs towards merged, lowest first; other values are outcomes.
export const PIPELINE_LADDER = [
  'PLANNED',
  'IN_PROGRESS',
  'REVIEW_OPEN',
  'READY',
  'MERGING',
  'MERGED',
] as const;

export const RUNG_LABELS: Record<string, string> = {
  PLANNED: 'Planned',
  IN_PROGRESS: 'In progress',
  REVIEW_OPEN: 'Review open',
  READY: 'Ready',
  MERGING: 'Merging',
  MERGED: 'Merged',
  FAILED: 'Failed',
  ABANDONED: 'Abandoned',
  SUPERSEDED: 'Superseded',
  SUCCEEDED: 'Succeeded',
  CLOSED: 'Closed',
};

export function rungLabel(rung: PipelineRung): string {
  return RUNG_LABELS[rung] ?? rung;
}

// Outcomes never share a ladder tone. A tone never carries state alone (WCAG 1.4.1).
const RUNG_TONES: Record<string, StatusBadgeTone> = {
  PLANNED: 'muted',
  IN_PROGRESS: 'in-progress',
  REVIEW_OPEN: 'in-progress',
  READY: 'warning',
  MERGING: 'in-progress',
  MERGED: 'success',
  FAILED: 'destructive',
  ABANDONED: 'destructive',
  SUPERSEDED: 'muted',
  SUCCEEDED: 'success',
  CLOSED: 'muted',
};

// OUTCOME_TONE is for rungs neither the ladder nor the outcome list names.
const OUTCOME_TONE: StatusBadgeTone = 'muted';

export function rungTone(rung: PipelineRung): StatusBadgeTone {
  return RUNG_TONES[rung] ?? OUTCOME_TONE;
}
