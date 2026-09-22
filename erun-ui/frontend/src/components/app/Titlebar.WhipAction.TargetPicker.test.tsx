import assert from 'node:assert/strict';

import * as React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { test, vi } from 'vitest';

import type { WhipTargetSelection } from '@/app/model';

// IconTooltip renders its label into a Radix portal, which static markup does
// not contain -- so the only visible text on these icon-only buttons would be
// invisible to this test, and the property under test is precisely the
// agreement between that visible text and the button's accessible name.
// Rendering the label inline instead (as an attribute on a wrapper) is what
// lets the assertion compare the two rather than restate one twice.
vi.mock('erun-kit', async (importOriginal) => {
  const actual = await importOriginal<typeof import('erun-kit')>();
  return {
    ...actual,
    IconTooltip: ({ label, children }: { label: string; children: React.ReactElement }) => (
      <span data-tooltip-label={label}>{children}</span>
    ),
  };
});

const { TitlebarWhipTargetPicker } =
  await import('@/components/app/Titlebar.WhipAction.TargetPicker');

// The untouched default selection: nothing focused, nothing checked. The
// ambient notice it renders is irrelevant here; only the three group
// shortcuts are.
const EMPTY_SELECTION: WhipTargetSelection = {
  environmentMode: 'custom',
  orchestratorMode: 'custom',
  selectedEnvironmentIds: [],
  selectedOrchestratorIds: [],
};

function renderShortcuts(): string {
  return renderToStaticMarkup(
    <TitlebarWhipTargetPicker
      targets={null}
      targetsLoading
      selection={EMPTY_SELECTION}
      onToggleEnvironment={() => undefined}
      onToggleOrchestrator={() => undefined}
      onSelectAllEnvironments={() => undefined}
      onSelectAllOrchestrators={() => undefined}
      onSelectAll={() => undefined}
    />,
  );
}

// The shortcuts' rendered order and the accessible name each button carries.
function renderedShortcuts(markup: string): { label: string; ariaLabel: string }[] {
  const wrappers = markup.matchAll(
    /data-tooltip-label="([^"]*)"[^>]*>\s*<button[^>]*aria-label="([^"]*)"/g,
  );
  return [...wrappers].map((match) => {
    const [, label, ariaLabel] = match;
    assert.ok(label !== undefined && ariaLabel !== undefined, `unreadable shortcut in ${markup}`);
    return { label, ariaLabel };
  });
}

test('every group shortcut announces the label its own tooltip shows', () => {
  // The reported failure. IconTooltip associates its label as a *description*
  // (aria-describedby), not as a name, so the hand-written aria-label is the
  // accessible name -- and the third shortcut's read "Select all" while the
  // tooltip the operator can see read "Select all orchestrators and
  // environments". WCAG 2.5.3 "Label in Name" requires the accessible name to
  // contain the visible text, and a voice-control operator reading the tooltip
  // ("click Select all orchestrators and environments") had nothing in the
  // name to match. Written once per shortcut and consumed by both, the two
  // cannot drift apart again -- this assertion is what says so.
  const shortcuts = renderedShortcuts(renderShortcuts());

  assert.equal(shortcuts.length, 3, `expected three group shortcuts in ${renderShortcuts()}`);
  for (const shortcut of shortcuts) {
    assert.equal(
      shortcut.ariaLabel,
      shortcut.label,
      `a group shortcut's accessible name diverged from its visible label: ${shortcut.ariaLabel} vs ${shortcut.label}`,
    );
  }
  assert.deepEqual(
    shortcuts.map((shortcut) => shortcut.label),
    [
      'Select all orchestrators',
      'Select all environments',
      'Select all orchestrators and environments',
    ],
  );
});
