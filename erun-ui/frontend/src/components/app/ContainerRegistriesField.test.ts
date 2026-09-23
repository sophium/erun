import assert from 'node:assert/strict';

import { TooltipProvider } from 'erun-kit';
import * as React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { test } from 'vitest';

import type { UIContainerRegistryEntry } from '@/types';

import { ContainerRegistriesField } from './ContainerRegistriesField';

// The registries field's inline hint previews the backend's marker rules so an
// operator can fix an invalid list before saving, and it used to render as a
// bare amber line:
//
//   <p role="alert" className="text-xs text-amber-700 dark:text-amber-400">
//
// It kept the role and the amber tone and dropped the glyph, so the one signal
// saying "this save is blocked until the entries change" was colour — the
// signal a reader who cannot see it never receives (WCAG 1.4.1). An amber line
// alone is the same failure as a red one alone; the site's own comment now says
// so, and these are the assertions that keep it true, because an element with
// no glyph satisfies every assertion the tone alone could pass.
//
// The amber tone is also the reason these read the element rather than the
// markup: the field renders a dozen icons above the hint, so "the field
// contains an svg" was already true of the defective line.

const twoBuildRoles: UIContainerRegistryEntry[] = [
  { registry: 'registry.example/test', roles: ['build', 'deploy'] },
  { registry: 'registry.internal/pw', roles: ['build', 'deploy'] },
];

// A copy source with no destination. Reaching this one matters because its
// message is the field's longest — the case the wrapping half exists for.
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

// The hint element and everything it renders. Its `<p>` holds no nested `<p>`,
// so the first closing tag ends the block.
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

// The other half of the same contract: the hint is a sentence in a dialog that
// owns its width, so it wraps inside the field rather than widening it.
//
// Laying the glyph beside the text makes the hint a flex row, and a flex item's
// initial `min-width: auto` is its min-content width — so the text has to be
// allowed to shrink for the wrap to reach it at all, and a long marker or host
// name in the message would otherwise push the row wider than the dialog.
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
