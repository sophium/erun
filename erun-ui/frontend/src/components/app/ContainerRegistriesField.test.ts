import assert from 'node:assert/strict';

import { TooltipProvider } from 'erun-kit';
import * as React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { test } from 'vitest';

import type { UIContainerRegistryEntry } from '@/types';

import { ContainerRegistriesField } from './ContainerRegistriesField';

// The hint must carry a glyph, not colour alone (WCAG 1.4.1). The field has
// other icons, so these read the hint element itself.

const twoBuildRoles: UIContainerRegistryEntry[] = [
  { registry: 'registry.example/test', roles: ['build', 'deploy'] },
  { registry: 'registry.internal/pw', roles: ['build', 'deploy'] },
];

const copySourceWithoutDestination: UIContainerRegistryEntry[] = [
  { registry: 'registry.example/test', roles: ['build', 'from', 'deploy'] },
];

function render(entries: UIContainerRegistryEntry[]): string {
  return renderToStaticMarkup(
    React.createElement(TooltipProvider, {
      children: React.createElement(ContainerRegistriesField, {
        entries,
        suggestions: [],
        onChange: () => undefined,
      }),
    }),
  );
}

// The hint `<p>` holds no nested `<p>`, so the first closing tag ends it.
function hintAlert(markup: string): string {
  const match = /<p role="alert"[\s\S]*?<\/p>/.exec(markup);
  assert.ok(match, `the hint must be announced as an alert: ${markup}`);
  return match[0];
}

test('a blocked save states its hint with a glyph, not colour alone', () => {
  const alert = hintAlert(render(twoBuildRoles));
  assert.ok(
    alert.includes('Only one registry can be marked build.'),
    `the hint carries the message itself: ${alert}`,
  );
  assert.ok(
    alert.includes('<svg'),
    `the hint must not be signalled by colour alone (WCAG 1.4.1): ${alert}`,
  );
});

// The text must be allowed to shrink (flex min-width: auto) to wrap inside the dialog.
test('a blocked save wraps a long hint instead of widening the field', () => {
  const hint = 'From and to must be set together (a copy needs both a source and a destination).';
  const alert = hintAlert(render(copySourceWithoutDestination));
  assert.ok(alert.includes(hint), `the whole hint must render, not an ellipsis: ${alert}`);
  assert.match(alert, /overflow-wrap:anywhere/, `a long hint must wrap: ${alert}`);
  assert.match(
    alert,
    /<span class="min-w-0">/,
    `the flex item holding the hint must be allowed to shrink: ${alert}`,
  );
});
