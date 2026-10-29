import assert from 'node:assert/strict';

import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, test, vi } from 'vitest';

import { InlineAlert } from '@/components/app/InlineAlert';
import { RuntimeActivityField } from '@/components/app/ManageDialogRuntimeActivity';
import { RuntimeSizingField } from '@/components/app/ManageDialogRuntimeSizing';
import { RuntimeUsageField } from '@/components/app/ManageDialogRuntimeUsage';
import type { UISelection } from '@/types';

// One failed-read class must render identically across the Runtime tab's three panels.

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

const probeFailure = 'exit status 1: kubectl stub: no cluster in the Playwright harness';

// Shares available=false with a failed read (erun-ui/internal/runtimeview/sizing.go).
const sizingEmptyState = 'No standing sizing recommendation is available for this environment yet.';

function renderPanels(): { usage: string; activity: string; sizing: string } {
  return {
    usage: renderToStaticMarkup(<RuntimeUsageField selection={selection} disabled={false} />),
    activity: renderToStaticMarkup(<RuntimeActivityField selection={selection} disabled={false} />),
    sizing: renderToStaticMarkup(<RuntimeSizingField selection={selection} disabled={false} />),
  };
}

// InlineAlert holds no nested div, so the first closing tag ends it.
function alertBlock(markup: string): string {
  const match = /<div role="alert"[\s\S]*?<\/div>/.exec(markup);
  assert.ok(match, `no role="alert" element rendered in ${markup}`);
  return match[0];
}

// Blanks the cause: messages differ, the rendering must not.
function alertShell(markup: string): string {
  return alertBlock(markup).replace(/(<span class="min-w-0">)[\s\S]*(<\/span>)/, '$1$2');
}

beforeEach(() => {
  query.mockReset();
  mutation.mockReset();
});

test('a failed read renders the same in all three Runtime-tab panels', () => {
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
    assert.ok(block.includes(probeFailure), `failure text missing from ${block}`);
    assert.ok(block.includes('<svg'), `failure rendered without an icon: ${block}`);
  }

  assert.deepEqual(rendered.map(alertShell), [
    alertShell(usage),
    alertShell(usage),
    alertShell(usage),
  ]);

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
    // An empty state, not a fault.
    assert.ok(!markup.includes('role="alert"'), `empty state rendered as a failure: ${markup}`);
    assert.ok(markup.includes('role="status"'), `empty state lost its status role: ${markup}`);
  }

  assert.ok(sizing.includes(sizingEmptyState));
});
