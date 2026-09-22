import assert from 'node:assert/strict';

import * as React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { Provider } from 'react-redux';
import { test } from 'vitest';

import { store } from '@/app/store';
import type { UIPlatformEnvironment } from '@/types';

import { RegistrationEnvironmentsSection } from './TenantDashboardPanels.RegistrationEnvironments';

// The Registration tab's row reasons used to render as bare
// `<p className="mt-1 text-xs text-destructive">` with no role — and no live
// region on any ancestor either, since the enclosing DataTable is a plain
// `<table>`. The reason is the whole payload of a `failed` or
// `deletion-blocked` row and it appears asynchronously, after the badge has
// already flipped: the operator starts a provision or a delete, the status
// changes, and the text materialises beside it. Nothing was announced, so a
// screen-reader user learned the row had failed only by re-reading the table.
//
// These render the real section and assert the reasons are reachable by role,
// which is the property a bare text node cannot satisfy. The guard on each
// site names its own state, so the roles the two take are the split the
// console's `deployFeedbackRole` already draws — a fault for `failed`, and a
// standing state for `deletion-blocked`.

const REGISTRATION_CAPS = {
  canCreateContext: false,
  canRegisterEnvironment: false,
  canDeployEnvironment: false,
  canStopEnvironment: false,
  canDeleteEnvironment: false,
};

function environment(overrides: Partial<UIPlatformEnvironment>): UIPlatformEnvironment {
  return {
    environmentId: 'env-1',
    name: 'prod',
    type: 'runtime',
    status: 'running',
    ...overrides,
  };
}

function renderSection(environments: UIPlatformEnvironment[]): string {
  return renderToStaticMarkup(
    React.createElement(Provider, {
      store,
      children: React.createElement(RegistrationEnvironmentsSection, {
        data: { ...REGISTRATION_CAPS, environments },
      } as never),
    }),
  );
}

interface Tag {
  attrs: string;
  start: number;
  end: number;
  closing: boolean;
  selfClosing: boolean;
}

// attrsOf pulls a tag's attribute list back out of its own text. Reading it
// from the scanning regex's capture groups instead would go through an array
// index the two type-checkers in this module's gate disagree about; this is a
// single optional-chained exec, which both read the same way.
const attrsOf = /^<\/?[A-Za-z][A-Za-z0-9.]*((?:"[^"]*"|[^>])*?)\/?>$/;

function tags(markup: string): Tag[] {
  const re = /<\/([A-Za-z][A-Za-z0-9.]*)|<([A-Za-z][A-Za-z0-9.]*)((?:"[^"]*"|[^>])*?)(\/?)>/g;
  const found: Tag[] = [];
  for (let match = re.exec(markup); match !== null; match = re.exec(markup)) {
    const tagText = match[0];
    const closing = tagText.startsWith('</');
    found.push({
      attrs: attrsOf.exec(tagText)?.[1] ?? '',
      start: match.index,
      end: match.index + tagText.length,
      closing,
      selfClosing: !closing && tagText.endsWith('/>'),
    });
  }
  return found;
}

// ancestorsOf walks the rendered markup as a tag stack, so the element a
// fragment actually sits inside is the one reported. Reading the single '<'
// preceding the reason instead would name the innermost element -- correct
// only while the reason is a direct child of the element that carries the
// role, and silently '' once a primitive wraps it one level down.
function ancestorsOf(markup: string, index: number): Tag[] {
  const stack: Tag[] = [];
  for (const tag of tags(markup)) {
    if (tag.start >= index) break;
    if (tag.selfClosing) continue;
    if (tag.closing) stack.pop();
    else stack.push(tag);
  }
  return stack;
}

// elementMarkup spans a tag from its opening '<' to its matching close, so an
// assertion can speak about everything the element renders, not just its own
// attributes.
function elementMarkup(markup: string, open: Tag): string {
  let depth = 0;
  for (const tag of tags(markup.slice(open.start))) {
    if (tag.selfClosing) continue;
    depth += tag.closing ? -1 : 1;
    if (depth === 0) return markup.slice(open.start, open.start + tag.end);
  }
  return markup.slice(open.start);
}

// The reason is the only thing on screen saying *why* the provision failed;
// asserting on its presence alone would pass on a node that announces nothing,
// which is exactly the shipped defect.
function roleFor(markup: string, reason: string): string {
  const index = markup.indexOf(reason);
  assert.notEqual(index, -1, `the reason must render: ${reason}`);
  for (const tag of ancestorsOf(markup, index).reverse()) {
    const role = /role="([a-z]+)"/.exec(tag.attrs)?.[1];
    if (role !== undefined) return role;
  }
  return '';
}

// The element that announces the reason -- what a reader lands on when the live
// region fires, and so what has to carry the primitive's whole contract.
function announcerFor(markup: string, reason: string): string {
  const index = markup.indexOf(reason);
  assert.notEqual(index, -1, `the reason must render: ${reason}`);
  const announcer = ancestorsOf(markup, index)
    .reverse()
    .find((tag) => /role="[a-z]+"/.test(tag.attrs));
  assert.ok(announcer, `an element must announce the reason: ${markup}`);
  return elementMarkup(markup, announcer);
}

test('a failed provision announces its reason as a fault', () => {
  const markup = renderSection([
    environment({ status: 'failed', provisionError: 'deploy job did not succeed' }),
  ]);
  assert.equal(roleFor(markup, 'deploy job did not succeed'), 'alert');
});

test('a blocked delete announces its reason as a standing state, not a fault', () => {
  const markup = renderSection([
    environment({
      status: 'deletion-blocked',
      deleteError: 'namespace held by finalizer erun.io/teardown',
    }),
  ]);
  assert.equal(roleFor(markup, 'namespace held by finalizer erun.io/teardown'), 'status');
});

// Both statuses can be on the page at once, in different rows. The role is
// computed per row, so one must not drag the other's treatment with it.
test('the two reasons take their own roles when both kinds of row are present', () => {
  const markup = renderSection([
    environment({ environmentId: 'env-a', status: 'failed', provisionError: 'build image failed' }),
    environment({
      environmentId: 'env-b',
      status: 'deletion-blocked',
      deleteError: 'namespace held by finalizer erun.io/teardown',
    }),
  ]);
  assert.equal(roleFor(markup, 'build image failed'), 'alert');
  assert.equal(roleFor(markup, 'namespace held by finalizer erun.io/teardown'), 'status');
});

// The roles above are only half the contract. They were added to a reason that
// still rendered as a bare
//
//   <p className="mt-1 text-xs text-destructive">{reason}</p>
//
// which states the failure in the destructive colour and nothing else. Colour
// is the one signal a reader who cannot see it never receives, and
// `text-destructive` on an element with no glyph is exactly the WCAG 1.4.1
// failure InlineAlert.tsx was written to prevent -- `failed` is a fault, so it
// announces as an alert, and the alert has to carry the glyph that says so.
//
// This is the assertion a bare colour-carrying line cannot satisfy, so
// re-hand-rolling it fails here rather than shipping.
test('a failed provision states its reason with a glyph, not colour alone', () => {
  const markup = renderSection([
    environment({ status: 'failed', provisionError: 'deploy job did not succeed' }),
  ]);
  const announcer = announcerFor(markup, 'deploy job did not succeed');
  assert.ok(
    announcer.includes('<svg'),
    `the reason must not be signalled by colour alone (WCAG 1.4.1): ${announcer}`,
  );
});

// The blocked row's reason is the same line in the next cell, rendered
// `role="status"` because it is a standing state rather than a fault. The
// colour-alone failure is not a property of the role, so the standing state
// carries the same glyph obligation as the fault beside it.
test('a blocked delete states its reason with a glyph, not colour alone', () => {
  const markup = renderSection([
    environment({
      status: 'deletion-blocked',
      deleteError: 'namespace held by finalizer erun.io/teardown',
    }),
  ]);
  const announcer = announcerFor(markup, 'namespace held by finalizer erun.io/teardown');
  assert.ok(
    announcer.includes('<svg'),
    `the reason must not be signalled by colour alone (WCAG 1.4.1): ${announcer}`,
  );
});

// The other half of the same contract, and the one a table cell is most likely
// to lose: a reason is often a URL, an ARN or a wrapped upstream error, and the
// row owns its width -- the value wraps inside it or the reason is unreadable.
//
// `DataCell` renders a `<td className="truncate">`, whose `white-space: nowrap`
// inherits into whatever the cell holds, so the wrapping guarantee needs both
// the overflow-wrap and a white-space that lets it apply.
test('a failed provision wraps a long reason instead of losing it to the cell', () => {
  const reason =
    'run-instances: InsufficientInstanceCapacity - https://console.aws.amazon.com/ec2/home#LaunchInstanceWizard';
  const markup = renderSection([environment({ status: 'failed', provisionError: reason })]);
  const announcer = announcerFor(markup, 'run-instances');
  assert.match(
    announcer,
    /overflow-wrap:anywhere/,
    `a long reason must wrap inside the row: ${announcer}`,
  );
  assert.match(
    announcer,
    /whitespace-normal/,
    `the cell's nowrap must not clip the reason: ${announcer}`,
  );
  assert.ok(
    announcer.includes(reason),
    `the whole reason must render, not an ellipsis: ${announcer}`,
  );
});
