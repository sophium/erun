import assert from 'node:assert/strict';

import * as React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { Provider } from 'react-redux';
import { test } from 'vitest';

import type { ActivityQueueEntry } from '@/app/activityQueueState';
import { store } from '@/app/store';

import { ActivityCard } from './ActivityCard';

// The alert must carry a glyph so failure is not signalled by colour alone (WCAG 1.4.1).

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

test('a card with no error renders no alert', () => {
  const markup = render(entry({ status: 'running' }));
  assert.ok(!markup.includes('role="alert"'), markup);
});
