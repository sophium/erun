import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { orchestrator } from '../../../wailsjs/go/models';
import {
  whipOutcomeLabel,
  whipOutcomeTone,
  whipReportAllSucceeded,
} from './Titlebar.WhipAction.helpers';

function result(overrides: Partial<orchestrator.WhipResult>): orchestrator.WhipResult {
  return { kind: 'environment', id: 'frs/dev', name: 'frs/dev', outcome: 'pushed', ...overrides };
}

test('whipReportAllSucceeded is true when every result pushed', () => {
  const report = { results: [result({ id: '1' }), result({ id: '2' })] } as orchestrator.WhipReport;
  assert.equal(whipReportAllSucceeded(report), true);
});

test('whipReportAllSucceeded is false if any result capped, failed, or skipped', () => {
  for (const outcome of ['capped', 'failed', 'skipped']) {
    const report = {
      results: [result({ id: '1' }), result({ id: '2', outcome })],
    } as orchestrator.WhipReport;
    assert.equal(
      whipReportAllSucceeded(report),
      false,
      `outcome ${outcome} must block auto-dismiss`,
    );
  }
});

// "Nothing was targeted" reads as an outcome the operator needs to notice,
// not a success to hide -- an empty result list is not "all succeeded".
test('whipReportAllSucceeded is false for an empty result list', () => {
  const report = { results: [] } as unknown as orchestrator.WhipReport;
  assert.equal(whipReportAllSucceeded(report), false);
});

test('whipReportAllSucceeded is false for a null or missing report', () => {
  assert.equal(whipReportAllSucceeded(null), false);
  assert.equal(whipReportAllSucceeded(undefined), false);
});

test('whipOutcomeTone/whipOutcomeLabel are unaffected by the new helper', () => {
  assert.equal(whipOutcomeTone('pushed'), 'success');
  assert.equal(whipOutcomeLabel('pushed'), 'Pushed');
});
