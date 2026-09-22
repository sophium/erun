import assert from 'node:assert/strict';

import * as React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { Provider } from 'react-redux';
import { test } from 'vitest';

import type { ActivityQueueEntry } from '@/app/activityQueueState';
import { store } from '@/app/store';

import { ActivityCard } from './ActivityCard';

// A card's own error used to render as a hand-rolled red line:
//
//   <p className="mt-2 break-words rounded-sm border border-destructive/40
//      bg-destructive/10 px-2 py-1 text-xs text-destructive">{entry.error}</p>
//
// It kept the destructive colour and dropped the other half of the shared
// primitive's contract — the glyph, without which the failure is signalled by
// colour alone (WCAG 1.4.1), and the wrapping guarantee that stops a long
// upstream error widening the surface it sits in. That copy-paste is the shape
// the primitive was written to end, and it had re-diverged at dozens of sites
// across the frontend.
//
// This pins the primitive's contract at the call site: the alert carries a
// glyph. It is the property a bare element cannot satisfy, so re-hand-rolling
// the line fails here rather than shipping.

function entry(overrides: Partial<ActivityQueueEntry>): ActivityQueueEntry {
  return {
    id: 'activity-1',
    command: 'deploy',
    status: 'running',
    startedAt: 0,
    tenant: 'frs',
    environment: 'prod',
    ...overrides,
  } as ActivityQueueEntry;
}

function render(entryValue: ActivityQueueEntry): string {
  return renderToStaticMarkup(
    React.createElement(Provider, {
      store,
      children: React.createElement(ActivityCard, { entry: entryValue }),
    }),
  );
}

test('a card error renders through the alert primitive, glyph included', () => {
  const markup = render(
    entry({ error: 'helm upgrade failed: timed out waiting for the condition' }),
  );
  const alert = /<div role="alert"[^>]*>.*?<\/div>/s.exec(markup)?.[0] ?? '';
  assert.notEqual(alert, '', `the error must be announced as an alert: ${markup}`);
  assert.ok(
    alert.includes('helm upgrade failed: timed out waiting for the condition'),
    'the alert carries the message itself',
  );
  assert.ok(
    alert.includes('<svg'),
    `the failure must not be signalled by colour alone (WCAG 1.4.1): ${alert}`,
  );
});

// The positive control: a card with no error renders no alert at all, so the
// assertion above is about the error path rather than any alert the card
// always renders.
test('a card with no error renders no alert', () => {
  const markup = render(entry({ status: 'running' }));
  assert.ok(!markup.includes('role="alert"'), markup);
});
