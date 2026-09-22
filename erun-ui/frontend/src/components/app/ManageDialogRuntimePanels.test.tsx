import assert from 'node:assert/strict';

import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, test, vi } from 'vitest';

import { InlineAlert } from '@/components/app/InlineAlert';
import { RuntimeActivityField } from '@/components/app/ManageDialogRuntimeActivity';
import { RuntimeSizingField } from '@/components/app/ManageDialogRuntimeSizing';
import { RuntimeUsageField } from '@/components/app/ManageDialogRuntimeUsage';
import type { UISelection } from '@/types';

// The Runtime tab's three panels answer different questions but share one
// failure class: the probe could not read this environment, so the panel
// states why rather than rendering blank. They used to render that one failure
// three ways -- amber with a warning icon, amber without, and muted -- which
// meant the tone encoded which panel the operator happened to be looking at
// rather than anything about what went wrong. A per-panel test cannot catch
// that, which is why the suite's three existing "an unreachable pod fails soft"
// cases did not: only rendering all three side by side and comparing them does.

const { query, mutation } = vi.hoisted(() => ({ query: vi.fn(), mutation: vi.fn() }));

vi.mock('@/app/api/environmentApi', () => ({
  useGetRuntimeUsageQuery: (...args: unknown[]): unknown => query('usage', ...args) as unknown,
  useGetRuntimeActivityQuery: (...args: unknown[]): unknown =>
    query('activity', ...args) as unknown,
  useGetRuntimeSizingQuery: (...args: unknown[]): unknown => query('sizing', ...args) as unknown,
  useReclaimRuntimeResourcesMutation: () => [
    mutation,
    { isLoading: false, isError: false, error: undefined },
  ],
  useResizeRuntimeToRecommendationMutation: () => [
    mutation,
    { isLoading: false, isError: false, error: undefined },
  ],
}));

const selection: UISelection = { tenant: 'acme', environment: 'dev' };

// The same underlying cause the panel captures show in all three panels.
const probeFailure = 'exit status 1: kubectl stub: no cluster in the Playwright harness';

// The reader's own copy for "nothing to recommend yet", which shares
// available=false with a failed read (erun-ui/runtime_sizing.go).
const sizingEmptyState = 'No standing sizing recommendation is available for this environment yet.';

function renderPanels(): { usage: string; activity: string; sizing: string } {
  return {
    usage: renderToStaticMarkup(<RuntimeUsageField selection={selection} disabled={false} />),
    activity: renderToStaticMarkup(<RuntimeActivityField selection={selection} disabled={false} />),
    sizing: renderToStaticMarkup(<RuntimeSizingField selection={selection} disabled={false} />),
  };
}

// The alert element and everything inside it. InlineAlert's own markup holds no
// nested div, so the first closing tag ends the block.
function alertBlock(markup: string): string {
  const match = /<div role="alert"[\s\S]*?<\/div>/.exec(markup);
  assert.ok(match, `no role="alert" element rendered in ${markup}`);
  return match[0];
}

// The same block with the stated cause blanked out: each reader legitimately
// names what it could not read, so the message differs while the rendering --
// element, role, class list, icon, wrapper -- must not.
function alertShell(markup: string): string {
  return alertBlock(markup).replace(/(<span class="min-w-0">)[\s\S]*(<\/span>)/, '$1$2');
}

beforeEach(() => {
  query.mockReset();
  mutation.mockReset();
});

test('a failed read renders the same in all three Runtime-tab panels', () => {
  // Each reader prefixes its own sentence, exactly as the shipped panels do;
  // the cause, and therefore the failure class, is identical.
  query.mockImplementation((panel: string) => {
    const subject: Record<string, string> = {
      usage: "this environment's resource usage",
      activity: 'what the runtime is running',
      sizing: "this environment's sizing recommendation",
    };
    const name = subject[panel] ?? 'this environment';
    return {
      data: { available: false, message: `Cannot read ${name}: ${probeFailure}` },
      isFetching: false,
      refetch: () => undefined,
    };
  });

  const { usage, activity, sizing } = renderPanels();
  const rendered = [usage, activity, sizing];

  for (const markup of rendered) {
    const block = alertBlock(markup);
    // The cause is stated, and the state is not signalled by colour alone
    // (the icon) nor left unannounced (role="alert").
    assert.ok(block.includes(probeFailure), `failure text missing from ${block}`);
    assert.ok(block.includes('<svg'), `failure rendered without an icon: ${block}`);
  }

  // The point of the issue: one failure class, one rendering. Comparing the
  // three to each other is the assertion a single-panel test cannot make.
  assert.deepEqual(rendered.map(alertShell), [
    alertShell(usage),
    alertShell(usage),
    alertShell(usage),
  ]);

  // ...and that one rendering is the inline family's member for an attempted
  // failure, not a fourth style the three panels happen to agree on.
  const family = renderToStaticMarkup(<InlineAlert>{probeFailure}</InlineAlert>);
  assert.deepEqual(rendered.map(alertShell), [family, family, family].map(alertShell));
});

test('a panel with nothing to report stays plain status text, not a failure', () => {
  const emptyByPanel: Record<string, unknown> = {
    usage: { available: false, message: '' },
    activity: { available: false, message: '' },
    sizing: { available: false, message: sizingEmptyState },
  };
  query.mockImplementation((panel: string) => ({
    data: emptyByPanel[panel],
    isFetching: false,
    refetch: () => undefined,
  }));

  const { usage, activity, sizing } = renderPanels();

  for (const markup of [usage, activity, sizing]) {
    // "Nothing to recommend yet" is an empty state, not a fault: an alert here
    // would cry wolf on every environment that has no standing recommendation.
    assert.ok(!markup.includes('role="alert"'), `empty state rendered as a failure: ${markup}`);
    assert.ok(markup.includes('role="status"'), `empty state lost its status role: ${markup}`);
  }

  assert.ok(sizing.includes(sizingEmptyState));
});
