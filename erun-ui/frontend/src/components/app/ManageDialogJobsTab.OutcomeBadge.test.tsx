import assert from 'node:assert/strict';

import { renderToStaticMarkup } from 'react-dom/server';
import { test } from 'vitest';

import type { JobView } from '@/components/app/ManageDialogJobs.helpers';
import { OutcomeBadge } from '@/components/app/ManageDialogJobsTab';

// Abandoned and gate-incomplete record a clean zero exit, yet must never render as success.

function job(overrides: Partial<JobView>): JobView {
  return { id: 'job-1', name: 'gate', state: 'exited', exitCode: 0, ...overrides };
}

function badgeClass(markup: string): string {
  const classes = /^<span class="([^"]*)"/.exec(markup)?.[1];
  assert.ok(classes !== undefined, `no badge span in ${markup}`);
  return classes;
}

function badgeText(markup: string): string {
  return markup.replace(/<[^>]*>/g, '');
}

function badgeIcon(markup: string): string {
  const icon = /class="lucide (lucide-[a-z-]+)/.exec(markup)?.[1];
  assert.ok(icon !== undefined, `no lucide icon in ${markup}`);
  return icon;
}

function renderBadge(overrides: Partial<JobView>): string {
  return renderToStaticMarkup(<OutcomeBadge job={job(overrides)} />);
}

test('an abandoned job renders as abandoned, not as a green success', () => {
  // An abandoned job's own process exited cleanly, so its exit code is zero.
  const markup = renderBadge({ state: 'abandoned', exitCode: 0 });

  assert.equal(badgeText(markup), 'Abandoned (work still running)');
  assert.ok(!markup.includes('Succeeded'), `abandoned rendered as success: ${markup}`);
  assert.ok(!markup.includes('text-green'), `abandoned wore the success styling: ${markup}`);
  // Shares the destructive colour with failed, so the glyph must differ.
  assert.equal(badgeIcon(markup), 'lucide-circle-slash');
  assert.ok(badgeClass(markup).includes('text-destructive'), badgeClass(markup));
});

test('a gate-incomplete job renders as gate-incomplete, not as a green success', () => {
  const markup = renderBadge({ state: 'gate-incomplete', exitCode: 0 });

  assert.equal(badgeText(markup), 'Gate incomplete (no verdict)');
  assert.ok(!markup.includes('Succeeded'), `gate-incomplete rendered as success: ${markup}`);
  assert.ok(!markup.includes('text-green'), `gate-incomplete wore the success styling: ${markup}`);
  assert.equal(badgeIcon(markup), 'lucide-hourglass');
  assert.ok(badgeClass(markup).includes('text-amber-700'), badgeClass(markup));
  assert.ok(badgeClass(markup).includes('dark:text-amber-400'), badgeClass(markup));
});

test('an abandoned job is never rendered as unknown', () => {
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
  // -1 is a signal termination, i.e. a cancel.
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
  // Colour never carries the outcome alone, so no two states share a glyph or label.
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
