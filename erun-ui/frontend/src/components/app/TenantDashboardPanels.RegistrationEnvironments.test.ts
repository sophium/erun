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

// The reason is the only thing on screen saying *why* the provision failed;
// asserting on its presence alone would pass on a node that announces nothing,
// which is exactly the shipped defect.
function roleFor(markup: string, reason: string): string {
  const index = markup.indexOf(reason);
  assert.notEqual(index, -1, `the reason must render: ${reason}`);
  const openingTag = markup.slice(0, index).lastIndexOf('<');
  return /role="([a-z]+)"/.exec(markup.slice(openingTag, index))?.[1] ?? '';
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
