import assert from 'node:assert/strict';

import * as React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { Provider } from 'react-redux';
import { test } from 'vitest';

import { store } from '@/app/store';
import type { UIPlatformEnvironment } from '@/types';

import { RegistrationEnvironmentsSection } from './TenantDashboardPanels.RegistrationEnvironments';

// Row reasons arrive asynchronously, so they must be reachable by role: alert
// for `failed`, status for `deletion-blocked`.

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

// A single optional-chained exec reads the same under both type-checkers.
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

// Walks a tag stack so a reason wrapped one level down still reports its real container.
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

function elementMarkup(markup: string, open: Tag): string {
  let depth = 0;
  for (const tag of tags(markup.slice(open.start))) {
    if (tag.selfClosing) continue;
    depth += tag.closing ? -1 : 1;
    if (depth === 0) return markup.slice(open.start, open.start + tag.end);
  }
  return markup.slice(open.start);
}

function roleFor(markup: string, reason: string): string {
  const index = markup.indexOf(reason);
  assert.notEqual(index, -1, `the reason must render: ${reason}`);
  for (const tag of ancestorsOf(markup, index).reverse()) {
    const role = /role="([a-z]+)"/.exec(tag.attrs)?.[1];
    if (role !== undefined) return role;
  }
  return '';
}

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

// Colour alone states nothing to a reader who cannot see it (WCAG 1.4.1).
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

// DataCell's truncate sets nowrap, so wrapping needs both overflow-wrap and white-space.
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
