import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  buildDiagnosticsIssueURL,
  diagnosticsIssueBody,
  diagnosticsIssueTitle,
} from './diagnosticsIssue';
import type { DiagnosticsReportContext } from './diagnosticsReport';
import type { OrchestratorInfo } from './slices/orchestratorsSlice';

function orchestrator(overrides: Partial<OrchestratorInfo> = {}): OrchestratorInfo {
  return {
    id: 'erun-issues',
    name: 'erun-issues',
    environments: [],
    tenants: [],
    directories: [],
    sessionId: 42,
    status: 'running',
    busy: false,
    transient: false,
    shellRunning: false,
    shellCommand: '',
    shellStartedAtUnix: 0,
    ...overrides,
  };
}

test('diagnosticsIssueTitle prefills from the failing environment rather than leaving it blank', () => {
  const failing: DiagnosticsReportContext = {
    kind: 'environment',
    tenant: 'acme',
    environment: 'dev',
    env: null,
    status: 'failed',
    trace: null,
  };
  assert.equal(diagnosticsIssueTitle(failing), 'acme/dev: failed');

  const healthy: DiagnosticsReportContext = { ...failing, status: '' };
  assert.equal(diagnosticsIssueTitle(healthy), 'acme/dev: diagnostics');
});

test('diagnosticsIssueTitle prefills from the orchestrator, naming a non-running status', () => {
  const running: DiagnosticsReportContext = {
    kind: 'orchestrator',
    orchestrator: orchestrator(),
    linkedEnvironments: [],
    appLog: null,
  };
  assert.equal(diagnosticsIssueTitle(running), 'Orchestrator erun-issues: diagnostics');

  const stopped: DiagnosticsReportContext = {
    kind: 'orchestrator',
    orchestrator: orchestrator({ status: 'stopped' }),
    linkedEnvironments: [],
    appLog: null,
  };
  assert.equal(diagnosticsIssueTitle(stopped), 'Orchestrator erun-issues: stopped');
});

test('diagnosticsIssueTitle falls back to the app context', () => {
  assert.equal(diagnosticsIssueTitle({ kind: 'app', appLog: null }), 'ERun desktop: diagnostics');
});

test('diagnosticsIssueBody carries a reproduction, observed-vs-expected, and environment shape', () => {
  const body = diagnosticsIssueBody('EVIDENCE_MARKER', { version: '1.0.0', commit: 'abc123' });
  assert.match(body, /## What happened/);
  assert.match(body, /## What you expected/);
  assert.match(body, /## Reproduction/);
  assert.match(body, /## Environment/);
  assert.match(body, /- erun version: 1\.0\.0 \(abc123\)/);
  assert.match(body, /## Diagnostics evidence/);
  assert.match(body, /EVIDENCE_MARKER/);
});

test('buildDiagnosticsIssueURL leaves a short body untouched', () => {
  const { url, truncated } = buildDiagnosticsIssueURL('title', 'short body');
  assert.equal(truncated, false);
  assert.match(url, /^https:\/\/github\.com\/sophium\/erun\/issues\/new\?/);
  const params = new URL(url).searchParams;
  assert.equal(params.get('title'), 'title');
  assert.equal(params.get('body'), 'short body');
  assert.equal(params.get('labels'), 'bug');
});

test('buildDiagnosticsIssueURL trims an oversized body to fit the cap and says so', () => {
  const hugeBody = 'x'.repeat(50_000);
  const { url, truncated } = buildDiagnosticsIssueURL('title', hugeBody, 2000);
  assert.equal(truncated, true);
  assert.ok(url.length <= 2000, `expected url.length <= 2000, got ${String(url.length)}`);
  const params = new URL(url).searchParams;
  assert.match(params.get('body') ?? '', /truncated — the full report is on your clipboard/);
});
