import assert from 'node:assert/strict';

import { renderToStaticMarkup } from 'react-dom/server';
import { test } from 'vitest';

import type { JobView } from '@/components/app/ManageDialogJobs.helpers';
import { OutcomeBadge } from '@/components/app/ManageDialogJobsTab';

// A job row's badge, rendered through the component rather than asserted on the
// classifier alone. `erun job status --help` states the contract the row has to
// honour: unknown, abandoned, and gate-incomplete are exactly as terminal as
// exited, and never a success whatever the raw exit code says. Only the rendered
// class list, icon and words show what an operator actually saw, and two of
// those states are precisely the ones whose recorded exit code is a clean zero.

function job(overrides: Partial<JobView>): JobView {
  return { id: 'job-1', name: 'gate', state: 'exited', exitCode: 0, ...overrides };
}

// The badge's own class list, where its styling lives.
function badgeClass(markup: string): string {
  const classes = /^<span class="([^"]*)"/.exec(markup)?.[1];
  assert.ok(classes !== undefined, `no badge span in ${markup}`);
  return classes;
}

// The label an operator reads, with the icon markup stripped.
function badgeText(markup: string): string {
  return markup.replace(/<[^>]*>/g, '');
}

// The icon's own lucide name, so "each state has its own icon" is asserted
// rather than assumed from the markup merely containing an <svg>.
function badgeIcon(markup: string): string {
  const icon = /class="lucide (lucide-[a-z-]+)/.exec(markup)?.[1];
  assert.ok(icon !== undefined, `no lucide icon in ${markup}`);
  return icon;
}

function renderBadge(overrides: Partial<JobView>): string {
  return renderToStaticMarkup(<OutcomeBadge job={job(overrides)} />);
}

test('an abandoned job renders as abandoned, not as a green success', () => {
  // The reported failure. An abandoned job left processes running in its own
  // process group, and its own process is the one that exited -- cleanly, by
  // definition -- so the exit code on the record is a zero. The badge read that
  // zero as success, which is the one outcome that most needs an operator to
  // clean up after it.
  const markup = renderBadge({ state: 'abandoned', exitCode: 0 });

  assert.equal(badgeText(markup), 'Abandoned (work still running)');
  assert.ok(!markup.includes('Succeeded'), `abandoned rendered as success: ${markup}`);
  assert.ok(!markup.includes('text-green'), `abandoned wore the success styling: ${markup}`);
  // Its own icon, not the failed state's XCircle: the two share the destructive
  // colour and are told apart by glyph and words alone.
  assert.equal(badgeIcon(markup), 'lucide-circle-slash');
  assert.ok(badgeClass(markup).includes('text-destructive'), badgeClass(markup));
});

test('a gate-incomplete job renders as gate-incomplete, not as a green success', () => {
  // The sibling case, and the same clean zero: this job's own process ended
  // while a job it started had not reached a verdict, so nothing about its own
  // exit reports that unresolved work.
  const markup = renderBadge({ state: 'gate-incomplete', exitCode: 0 });

  assert.equal(badgeText(markup), 'Gate incomplete (no verdict)');
  assert.ok(!markup.includes('Succeeded'), `gate-incomplete rendered as success: ${markup}`);
  assert.ok(!markup.includes('text-green'), `gate-incomplete wore the success styling: ${markup}`);
  assert.equal(badgeIcon(markup), 'lucide-hourglass');
  // The unresolved family's existing styling, matching "Outcome unknown".
  assert.ok(badgeClass(markup).includes('text-amber-700'), badgeClass(markup));
  assert.ok(badgeClass(markup).includes('dark:text-amber-400'), badgeClass(markup));
});

test('an abandoned job is never rendered as unknown', () => {
  // Task and agent jobs carry no exit code until they end, so a null is the
  // shape the classifier already reserves for "the record outlived whatever
  // was meant to finish it". Abandoned is not that: the outcome was recorded,
  // and it is the one that says work is still running.
  const markup = renderBadge({ state: 'abandoned', exitCode: null });

  assert.equal(badgeText(markup), 'Abandoned (work still running)');
  assert.equal(badgeIcon(markup), 'lucide-circle-slash');
});

test('a running job renders as running', () => {
  const markup = renderBadge({ state: 'running', exitCode: null });

  assert.equal(badgeText(markup), 'Running');
  assert.equal(badgeIcon(markup), 'lucide-loader-circle');
});

test('an unknown job renders as unknown', () => {
  const markup = renderBadge({ state: 'unknown', exitCode: null });

  assert.equal(badgeText(markup), 'Outcome unknown');
  assert.equal(badgeIcon(markup), 'lucide-triangle-alert');
  assert.ok(badgeClass(markup).includes('text-amber-700'), badgeClass(markup));
});

test('a succeeded job renders as succeeded', () => {
  const markup = renderBadge({ state: 'exited', exitCode: 0 });

  assert.equal(badgeText(markup), 'Succeeded');
  assert.equal(badgeIcon(markup), 'lucide-circle-check');
  assert.ok(badgeClass(markup).includes('text-green-700'), badgeClass(markup));
});

test('a signalled job renders as Cancelled', () => {
  // The supervisor records -1 when work was terminated by a signal, which is
  // what a cancel produces.
  const markup = renderBadge({ state: 'exited', exitCode: -1 });

  assert.equal(badgeText(markup), 'Cancelled');
  assert.equal(badgeIcon(markup), 'lucide-ban');
});

test('a failed job names the exit code it failed with', () => {
  const markup = renderBadge({ state: 'exited', exitCode: 2 });

  assert.equal(badgeText(markup), 'Failed (exit 2)');
  assert.equal(badgeIcon(markup), 'lucide-circle-x');
  assert.ok(badgeClass(markup).includes('text-destructive'), badgeClass(markup));
});

test('every outcome the badge can show gets its own icon and its own words', () => {
  // The badge contract the component's own comment states: colour never carries
  // the outcome on its own, so no two states may share a glyph or a label --
  // which is exactly what a fall-through to the exit-code arms produced.
  const states: Partial<JobView>[] = [
    { state: 'running', exitCode: null },
    { state: 'exited', exitCode: 0 },
    { state: 'exited', exitCode: -1 },
    { state: 'exited', exitCode: 2 },
    { state: 'unknown', exitCode: null },
    { state: 'abandoned', exitCode: 0 },
    { state: 'gate-incomplete', exitCode: 0 },
  ];
  const markup = states.map((overrides) => renderBadge(overrides));

  const icons = markup.map(badgeIcon);
  const labels = markup.map(badgeText);
  assert.equal(new Set(icons).size, states.length, `shared icons: ${icons.join(', ')}`);
  assert.equal(new Set(labels).size, states.length, `shared labels: ${labels.join(', ')}`);
});
