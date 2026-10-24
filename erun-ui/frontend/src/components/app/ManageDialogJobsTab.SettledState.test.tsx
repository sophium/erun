import assert from 'node:assert/strict';

import { renderToStaticMarkup } from 'react-dom/server';
import { test } from 'vitest';

import { JobsTab } from '@/components/app/ManageDialogJobsTab';

// The first render, before any effect runs, must not report an empty job store.

const SELECTION = { tenant: 'pw', environment: 'alpha' };

test('the tab does not report an empty store before its read has been answered', () => {
  const markup = renderToStaticMarkup(<JobsTab selection={SELECTION} open />);

  assert.ok(
    markup.includes('data-testid="manage-jobs-loading"'),
    `no loading placeholder in ${markup}`,
  );
  assert.ok(!markup.includes('No jobs yet'), `empty store rendered before the read: ${markup}`);
});

test('the loading placeholder and the settled empty state are different elements', () => {
  const markup = renderToStaticMarkup(<JobsTab selection={SELECTION} open />);

  assert.ok(
    !markup.includes('data-testid="manage-jobs-empty"'),
    `settled id on loading: ${markup}`,
  );
});
