import assert from 'node:assert/strict';
import { test } from 'node:test';

import { buildTitlebarFailureReport } from './titlebarFailureReport';

test('buildTitlebarFailureReport carries a labelled Message line for a bare error', () => {
  const report = buildTitlebarFailureReport({
    message: 'Could not reach the runtime.',
    detail: '',
    copyOutput: '',
  });
  assert.match(report, /^Message: Could not reach the runtime\.$/m);
  assert.doesNotMatch(report, /^Target:/m);
});

test('buildTitlebarFailureReport includes the target and detail when known', () => {
  const report = buildTitlebarFailureReport({
    message: 'Deploy failed',
    detail: 'exit status 1',
    copyOutput: '',
    envTenant: 'acme',
    envEnvironment: 'dev',
  });
  assert.match(report, /^Target: acme\/dev$/m);
  assert.match(report, /^Detail: exit status 1$/m);
});

test('buildTitlebarFailureReport appends captured output as its own labelled section', () => {
  const report = buildTitlebarFailureReport({
    message: 'Deploy failed',
    detail: '',
    copyOutput: 'helm upgrade --install\nError: UPGRADE FAILED',
  });
  assert.match(report, /Output:\nhelm upgrade --install\nError: UPGRADE FAILED/);
});

test('buildTitlebarFailureReport does not duplicate output identical to the message', () => {
  const report = buildTitlebarFailureReport({
    message: 'Deploy failed',
    detail: '',
    copyOutput: 'Deploy failed',
  });
  assert.doesNotMatch(report, /Output:/);
});
