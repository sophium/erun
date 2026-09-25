import type { StatusBadgeTone } from '../components/StatusBadge.helpers';

// The pipeline view's rung vocabulary, shared by the two surfaces that render
// it: the console's Pipeline section and the desktop's Pipeline tab. Both read
// the same platform read (GET /v1/pipeline) and must label the rungs the same
// way -- two surfaces that name one rung differently are worse than one
// surface, because an operator comparing them cannot tell whether they are
// looking at the same work. The vocabulary is the platform's, so it lives
// here once rather than in either app.

// A rung is whatever the platform labelled the work with, including outcomes
// this client has never heard of: the platform being ahead of the client is
// not a licence to describe work the client cannot see.
export type PipelineRung = string;

// PIPELINE_LADDER is the six rungs a piece of work climbs on its way from an
// idea to a merged change, lowest first. Every other value is an outcome
// (FAILED, ABANDONED, CLOSED, SUPERSEDED, SUCCEEDED), reported as itself.
export const PIPELINE_LADDER = [
  'PLANNED',
  'IN_PROGRESS',
  'REVIEW_OPEN',
  'READY',
  'MERGING',
  'MERGED',
] as const;

// RUNG_LABELS is the operator-facing name for each rung. A rung absent from
// this map renders under its own spelling rather than being renamed: a client
// that invented a friendlier name for a value it does not know would be
// describing work it cannot see.
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

// RUNG_TONES separates the ladder from its outcomes: the steps on the way to
// merged are in-progress or success, and the ways work stops -- FAILED,
// ABANDONED, CLOSED, SUPERSEDED -- never render as a step, because reading
// "abandoned" as "still moving" is the mistake this view exists to prevent.
// WCAG 1.4.1: a tone never carries the state alone, every badge still shows
// the rung word as its label.
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

// OUTCOME_TONE is what a rung neither the ladder nor the outcome list names
// is toned. It states the one thing that is certainly true -- this is not a
// step the client can place on the ladder -- rather than colouring it as
// progress or as a failure.
const OUTCOME_TONE: StatusBadgeTone = 'muted';

export function rungTone(rung: PipelineRung): StatusBadgeTone {
  return RUNG_TONES[rung] ?? OUTCOME_TONE;
}
