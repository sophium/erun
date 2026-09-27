import assert from 'node:assert/strict';

import { renderToStaticMarkup } from 'react-dom/server';
import { test } from 'vitest';

import { JobsTab } from '@/components/app/ManageDialogJobsTab';

// The jobs tab's first render, which is the one no effect has run for yet: the
// tab has asked the pod nothing at that point, so it must not report an empty
// job store. `renderToStaticMarkup` renders exactly that commit, which is what
// makes this deterministic where a browser cannot see it -- the window closes
// as soon as the effect fires, and a loaded machine can stretch it.
//
// This is the state a convergence on the settled empty state has to be immune
// to: while the two shared one test id, a wait for the empty state settled on
// the loading placeholder, and the assertion that followed spent expect's 10s
// default on a read that had not been issued yet.

const SELECTION = { tenant: 'pw', environment: 'alpha' };

test('the tab does not report an empty store before its read has been answered', () => {
  const markup = renderToStaticMarkup(<JobsTab selection={SELECTION} open />);

  assert.ok(
    markup.includes('data-testid="manage-jobs-loading"'),
    `no loading placeholder in ${markup}`,
  );
  assert.ok(!markup.includes('No jobs yet'), `empty store rendered before the read: ${markup}`);
});

// The settled empty state keeps its own id, so the placeholder above is not
// what a caller waiting on the empty state settles on once the read lands.
test('the loading placeholder and the settled empty state are different elements', () => {
  const markup = renderToStaticMarkup(<JobsTab selection={SELECTION} open />);

  assert.ok(
    !markup.includes('data-testid="manage-jobs-empty"'),
    `settled id on loading: ${markup}`,
  );
});
