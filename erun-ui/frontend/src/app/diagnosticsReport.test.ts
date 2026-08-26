import assert from 'node:assert/strict';
import { test } from 'node:test';

import { formatDiagnosticsReport } from './diagnosticsReport';
import type { OrchestratorInfo } from './slices/orchestratorsSlice';

function orchestrator(overrides: Partial<OrchestratorInfo> = {}): OrchestratorInfo {
  return {
    id: 'erun-issues',
    name: 'erun-issues',
    environments: [{ tenant: 'erun', environment: 'ux', directory: '/tmp/erun-ux' }],
    tenants: ['erun'],
    directories: ['/tmp/erun-ux'],
    sessionId: 42,
    status: 'running',
    busy: false,
    transient: false,
    shellRunning: false,
    shellCommand: '',
    shellStartedAtUnix: 0,
    nudgeCount: 0,
    nudgeCapped: false,
    ...overrides,
  };
}

// The defect this report exists to fix (#1241): the panel derived its context
// from the sidebar's env selection alone, so an orchestrator session — which
// never touches that selection — rendered the environment-shaped report's
// "nothing selected" fallback. Every orchestrator report must carry the
// orchestrator's own identity instead, never that string.
test('an orchestrator context never emits "environment: none selected"', () => {
  const report = formatDiagnosticsReport({
    generatedAt: '2026-08-24T00:00:00.000Z',
    build: null,
    context: {
      kind: 'orchestrator',
      orchestrator: orchestrator(),
      linkedEnvironments: [{ tenant: 'erun', environment: 'ux', status: '' }],
      appLog: null,
    },
    uiTrace: [],
  });

  assert.doesNotMatch(report, /environment: none selected/);
  assert.match(report, /orchestrator: erun-issues \(erun-issues\)/);
  assert.match(report, /status: running/);
  assert.match(report, /erun \/ ux/);
});

test('an orchestrator context surfaces its background shell and app log', () => {
  const report = formatDiagnosticsReport({
    generatedAt: '2026-08-24T00:00:00.000Z',
    build: null,
    context: {
      kind: 'orchestrator',
      orchestrator: orchestrator({ shellRunning: true, shellCommand: 'npm test' }),
      linkedEnvironments: [],
      appLog: { available: true, content: 'ERUN_LOG_MARKER', path: '/tmp/erun-app.log' },
    },
    uiTrace: [],
  });

  assert.match(report, /background shell: npm test/);
  assert.match(report, /ERUN_LOG_MARKER/);
  assert.match(report, /linked environments:\n {2}\(none\)/);
});

test('an environment context reports the selected env, status, and trace', () => {
  const report = formatDiagnosticsReport({
    generatedAt: '2026-08-24T00:00:00.000Z',
    build: { version: '1.0.0' },
    context: {
      kind: 'environment',
      tenant: 'acme',
      environment: 'dev',
      env: { name: 'dev', type: 'local-agent' },
      status: 'failed',
      trace: { available: true, content: 'deploy failed\n', path: '/trace.log' },
    },
    uiTrace: [],
  });

  assert.match(report, /environment: acme \/ dev \(local-agent\)/);
  assert.match(report, /status: failed/);
  assert.match(report, /deploy failed/);
});

test('an app context reports the desktop log when neither an env nor an orchestrator is active', () => {
  const report = formatDiagnosticsReport({
    generatedAt: '2026-08-24T00:00:00.000Z',
    build: null,
    context: { kind: 'app', appLog: { available: false, reason: 'no log captured yet', path: '' } },
    uiTrace: [],
  });

  assert.doesNotMatch(report, /environment: none selected/);
  assert.match(report, /no log captured yet/);
});
