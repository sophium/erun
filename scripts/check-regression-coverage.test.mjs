// Self-test for check-regression-coverage.mjs, the regression-coverage gate
// for root AGENTS.md § "A Defect Fix Names Its Reproduction" (see that file's
// header for the design rationale and for what it deliberately does not try
// to decide). Run with `node --test scripts/check-regression-coverage.test.mjs`.
//
// Every case below drives the pure classifier against synthetic git facts and
// an injected filesystem, so nothing here reads the real tree -- the same
// split erun-integration's structural gates use. The gate has to fail what it
// should catch and pass what it should allow, so both directions are asserted
// for every rule, not just the failing one: a gate that only ever fails is as
// useless as one that only ever passes, and the second half is what keeps a
// legitimate change (a docs fix, a revert) from being blocked.

import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  evaluateRegressionCoverage,
  exemptionKinds,
  isDefectFix,
  isDocumentationPath,
  isTestFile,
  parseTrailers,
  statementIsSubstantive,
} from './check-regression-coverage.mjs';

// A filesystem double: `files` maps a repo-relative path to its contents.
function io(files) {
  return {
    fileExists: (path) => Object.hasOwn(files, path),
    readFile: (path) => files[path],
  };
}

function change(overrides = {}) {
  return {
    branchName: 'bug/9999-a-defect',
    commits: [{ sha: 'abc1234', subject: 'Fix the thing', body: '' }],
    changedFiles: [{ status: 'M', path: 'erun-common/thing.go' }],
    ...overrides,
  };
}

const goodReproduces =
  'Reproduces: the gates tab renders the empty state instead of panel.Error when ListGateRuns returns a 404';

test('isTestFile recognises every test shape this repo actually has', () => {
  const cases = [
    ['erun-integration/push_test.go', true],
    ['erun-ui/playwright/tests/tenant-dashboard-gates.spec.ts', true],
    ['erun-ui/frontend/src/app/selectors.test.ts', true],
    ['erun-kit/src/widgets/Meter.test.tsx', true],
    ['scripts/check-regression-coverage.test.mjs', true],
    ['scripts/agent-gate_test.sh', true],
    ['erun-devops/k8s/erun-backend-db-chart_test.sh', true],
    ['erun-common/thing.go', false],
    ['erun-integration/testdata/push/dry_run.txt', false],
    ['erun-ui/frontend/src/app/selectors.ts', false],
    ['AGENTS.md', false],
  ];
  for (const [path, want] of cases) {
    assert.equal(isTestFile(path), want, `isTestFile(${path})`);
  }
});

test('isDocumentationPath covers prose and nothing executable', () => {
  assert.equal(isDocumentationPath('AGENTS.md'), true);
  assert.equal(isDocumentationPath('erun-docs/docs/cli/exec.mdx'), true);
  assert.equal(isDocumentationPath('erun-docs/static/img/screenshot.png'), true);
  assert.equal(isDocumentationPath('erun-docs/docusaurus.config.ts'), false);
  assert.equal(isDocumentationPath('erun-devops/k8s/values.yaml'), false);
});

test('statementIsSubstantive rejects boilerplate and accepts a real failure statement', () => {
  const rejected = [
    '',
    'fixes the bug',
    'regression test added',
    'this fixes the reported issue and adds a test for it',
    'fix fix fix fix fix fix fix fix fix fix fix fix',
  ];
  for (const value of rejected) {
    assert.equal(statementIsSubstantive(value).ok, false, `expected rejection: ${JSON.stringify(value)}`);
  }
  const accepted = [
    'one failed read blanked the other panel already-resolved exposures content',
    'promote used a local image inspect hit as evidence the registry had the fingerprint blob',
    'the gates tab renders the empty state instead of panel.Error when ListGateRuns returns 404',
  ];
  for (const value of accepted) {
    assert.equal(statementIsSubstantive(value).ok, true, `expected acceptance: ${JSON.stringify(value)}`);
  }
});

test('parseTrailers reads declarations from any commit in the range, not just the tip', () => {
  const trailers = parseTrailers([
    { sha: '1', subject: 'Add the failing case', body: 'Regression-Test: a_test.go::case_one' },
    { sha: '2', subject: 'Fix it', body: 'Reproduces: something specific went wrong here\nCloses #1' },
  ]);
  assert.deepEqual(trailers['Regression-Test'], ['a_test.go::case_one']);
  assert.deepEqual(trailers['Reproduces'], ['something specific went wrong here']);
  assert.equal(trailers['Closes'], undefined);
});

test('isDefectFix follows the branch convention and honours the explicit override', () => {
  assert.equal(isDefectFix(change({ branchName: 'bug/2123-thing' })), true);
  assert.equal(isDefectFix(change({ branchName: 'feature/2123-thing' })), false);
  assert.equal(
    isDefectFix(change({ branchName: 'feature/2123-thing', commits: [{ sha: '1', subject: 's', body: 'Defect-Fix: yes' }] })),
    true,
  );
  assert.equal(
    isDefectFix(change({ branchName: 'bug/2123-thing', commits: [{ sha: '1', subject: 's', body: 'Defect-Fix: no' }] })),
    false,
  );
  assert.equal(isDefectFix(change({ forceDefectFix: true, branchName: 'main' })), true);
});

// --- the case the gate exists to catch --------------------------------

test('a defect fix that touches production code and adds no test is rejected', () => {
  const result = evaluateRegressionCoverage(change(), io({}));
  assert.equal(result.classification, 'undeclared');
  assert.equal(result.failures.length, 1);
  assert.match(result.failures[0], /No "Regression-Test:" trailer/);
});

test('a defect fix that adds a test but names none is still rejected', () => {
  // This is the exact shape the reported defect takes: neighbouring states
  // get tests, the reported one does not, and nothing says which is which.
  const result = evaluateRegressionCoverage(
    change({
      changedFiles: [
        { status: 'M', path: 'erun-common/thing.go' },
        { status: 'M', path: 'erun-integration/thing_test.go' },
      ],
    }),
    io({ 'erun-integration/thing_test.go': 't.Run("some_neighbouring_state", ...)' }),
  );
  assert.equal(result.classification, 'undeclared');
  assert.match(result.notes.join(' '), /naming which case is the reproduction is the missing half/);
});

test('naming a test that this change did not touch is rejected', () => {
  // The anti-cheat: a declaration satisfied by pointing at any pre-existing
  // test in the repo would make the whole gate a formality.
  const result = evaluateRegressionCoverage(
    change({
      commits: [
        {
          sha: '1',
          subject: 'Fix it',
          body: `${goodReproduces}\nRegression-Test: erun-integration/unrelated_test.go::some_case`,
        },
      ],
    }),
    io({ 'erun-integration/unrelated_test.go': 't.Run("some_case", ...)' }),
  );
  assert.equal(result.classification, 'declared-invalid');
  assert.match(result.failures.join(' '), /is not added or modified by this change/);
});

test('naming a case that is not in the file is rejected', () => {
  const result = evaluateRegressionCoverage(
    change({
      commits: [
        {
          sha: '1',
          subject: 'Fix it',
          body: `${goodReproduces}\nRegression-Test: erun-integration/thing_test.go::case_that_does_not_exist`,
        },
      ],
      changedFiles: [
        { status: 'M', path: 'erun-common/thing.go' },
        { status: 'M', path: 'erun-integration/thing_test.go' },
      ],
    }),
    io({ 'erun-integration/thing_test.go': 't.Run("a_different_case", ...)' }),
  );
  assert.equal(result.classification, 'declared-invalid');
  assert.match(result.failures.join(' '), /does not contain the case/);
});

test('naming a non-test file as the reproduction is rejected', () => {
  const result = evaluateRegressionCoverage(
    change({
      commits: [{ sha: '1', subject: 'Fix it', body: `${goodReproduces}\nRegression-Test: erun-common/thing.go::Foo` }],
    }),
    io({ 'erun-common/thing.go': 'func Foo() {}' }),
  );
  assert.equal(result.classification, 'declared-invalid');
  assert.match(result.failures.join(' '), /is not a recognised test file/);
});

test('a named reproduction with no failure statement, or a boilerplate one, is rejected', () => {
  const base = change({
    changedFiles: [
      { status: 'M', path: 'erun-common/thing.go' },
      { status: 'A', path: 'erun-integration/thing_test.go' },
    ],
  });
  const files = io({ 'erun-integration/thing_test.go': 't.Run("the_case", ...)' });

  const missing = evaluateRegressionCoverage(
    { ...base, commits: [{ sha: '1', subject: 'Fix', body: 'Regression-Test: erun-integration/thing_test.go::the_case' }] },
    files,
  );
  assert.equal(missing.classification, 'declared-invalid');
  assert.match(missing.failures.join(' '), /needs a "Reproduces:" trailer/);

  const boilerplate = evaluateRegressionCoverage(
    {
      ...base,
      commits: [
        {
          sha: '1',
          subject: 'Fix',
          body: 'Reproduces: fixes the reported bug\nRegression-Test: erun-integration/thing_test.go::the_case',
        },
      ],
    },
    files,
  );
  assert.equal(boilerplate.classification, 'declared-invalid');
  assert.match(boilerplate.failures.join(' '), /"Reproduces:"/);
});

test('declaring both a named test and none at once is rejected', () => {
  const result = evaluateRegressionCoverage(
    change({
      commits: [
        {
          sha: '1',
          subject: 'Fix',
          body: 'Regression-Test: none\nRegression-Test: erun-integration/thing_test.go::the_case',
        },
      ],
    }),
    io({}),
  );
  assert.equal(result.classification, 'contradictory');
});

// --- the case the gate must not block ---------------------------------

test('a properly declared defect fix passes', () => {
  const result = evaluateRegressionCoverage(
    change({
      commits: [
        {
          sha: '1',
          subject: 'Fix it',
          body: `${goodReproduces}\nRegression-Test: erun-integration/thing_test.go::gate_runs_read_failure_shows_the_error`,
        },
      ],
      changedFiles: [
        { status: 'M', path: 'erun-common/thing.go' },
        { status: 'A', path: 'erun-integration/thing_test.go' },
      ],
    }),
    io({ 'erun-integration/thing_test.go': 't.Run("gate_runs_read_failure_shows_the_error", ...)' }),
  );
  assert.equal(result.classification, 'declared');
  assert.deepEqual(result.failures, []);
});

test('a change that is not a defect fix is out of scope entirely', () => {
  const result = evaluateRegressionCoverage(change({ branchName: 'feature/1-new-thing' }), io({}));
  assert.equal(result.defectFix, false);
  assert.equal(result.classification, 'not-a-defect-fix');
  assert.deepEqual(result.failures, []);
});

test('a docs-only fix is allowed, and the claim is verified against the real diff', () => {
  const commits = [
    {
      sha: '1',
      subject: 'Correct the retention default described on the deployment page',
      body: 'Regression-Test: none\nRegression-Test-Exemption: docs-only: the page described the chart default backwards; no executable file changes',
    },
  ];
  const allowed = evaluateRegressionCoverage(
    change({ commits, changedFiles: [{ status: 'M', path: 'erun-docs/docs/deployment/data-retention.md' }] }),
    io({}),
  );
  assert.equal(allowed.classification, 'exempt');
  assert.deepEqual(allowed.failures, []);

  const lying = evaluateRegressionCoverage(
    change({
      commits,
      changedFiles: [
        { status: 'M', path: 'erun-docs/docs/deployment/data-retention.md' },
        { status: 'M', path: 'erun-common/retention.go' },
      ],
    }),
    io({}),
  );
  assert.equal(lying.classification, 'exempt-invalid');
  assert.match(lying.failures.join(' '), /claims docs-only but changes 1 non-documentation file/);
});

test('a revert is allowed, and the claim is verified against the commit body', () => {
  const body =
    'Regression-Test: none\nRegression-Test-Exemption: revert: the reverted change broke chart rendering on arm64 clusters\n';
  const allowed = evaluateRegressionCoverage(
    change({
      commits: [{ sha: '1', subject: 'Revert "Add the thing"', body: `This reverts commit 0123456789abcdef.\n\n${body}` }],
    }),
    io({}),
  );
  assert.equal(allowed.classification, 'exempt');

  const lying = evaluateRegressionCoverage(change({ commits: [{ sha: '1', subject: 'Not a revert', body }] }), io({}));
  assert.equal(lying.classification, 'exempt-invalid');
  assert.match(lying.failures.join(' '), /no commit in the range carries a "This reverts commit/);
});

test('covered-by-existing must name a test that resolves, and that one need not be in the diff', () => {
  const files = io({ 'erun-integration/thing_test.go': 't.Run("already_reproduces_this", ...)' });
  const allowed = evaluateRegressionCoverage(
    change({
      commits: [
        {
          sha: '1',
          subject: 'Fix',
          body:
            'Regression-Test: none\n' +
            'Regression-Test-Exemption: covered-by-existing: the scenario already drives this exact registry disagreement\n' +
            'Regression-Test-Existing: erun-integration/thing_test.go::already_reproduces_this',
        },
      ],
    }),
    files,
  );
  assert.equal(allowed.classification, 'exempt');

  const unnamed = evaluateRegressionCoverage(
    change({
      commits: [
        {
          sha: '1',
          subject: 'Fix',
          body:
            'Regression-Test: none\n' +
            'Regression-Test-Exemption: covered-by-existing: the scenario already drives this exact registry disagreement',
        },
      ],
    }),
    files,
  );
  assert.equal(unnamed.classification, 'exempt-invalid');
  assert.match(unnamed.failures.join(' '), /needs a "Regression-Test-Existing:/);
});

test('an exemption needs a kind from the closed set and a substantive reason', () => {
  const unknown = evaluateRegressionCoverage(
    change({
      commits: [
        {
          sha: '1',
          subject: 'Fix',
          body: 'Regression-Test: none\nRegression-Test-Exemption: too-hard: it would take a while to write a test',
        },
      ],
    }),
    io({}),
  );
  assert.equal(unknown.classification, 'exempt-invalid');
  assert.match(unknown.failures.join(' '), /Unknown exemption kind "too-hard"/);

  const empty = evaluateRegressionCoverage(
    change({ commits: [{ sha: '1', subject: 'Fix', body: 'Regression-Test: none' }] }),
    io({}),
  );
  assert.equal(empty.classification, 'exempt-invalid');
  assert.match(empty.failures.join(' '), /needs a "Regression-Test-Exemption:/);

  const thin = evaluateRegressionCoverage(
    change({
      commits: [{ sha: '1', subject: 'Fix', body: 'Regression-Test: none\nRegression-Test-Exemption: no-reproducible-failure: n/a' }],
    }),
    io({}),
  );
  assert.equal(thin.classification, 'exempt-invalid');
  assert.match(thin.failures.join(' '), /Exemption reason/);
});

test('the unverifiable exemption kinds say so in the output rather than passing silently', () => {
  const result = evaluateRegressionCoverage(
    change({
      commits: [
        {
          sha: '1',
          subject: 'Fix',
          body:
            'Regression-Test: none\n' +
            'Regression-Test-Exemption: no-reproducible-failure: the wrong glyph baseline is only visible to a human reading the rendered titlebar',
        },
      ],
    }),
    io({}),
  );
  assert.equal(result.classification, 'exempt');
  assert.match(result.notes.join(' '), /cannot be machine-verified/);
  assert.equal(exemptionKinds['no-reproducible-failure'].verify, null);
});

// --- audit mode, for classifying history ------------------------------

test('audit mode classifies a historical fix by whether it carries any test at all', () => {
  const uncovered = evaluateRegressionCoverage(
    change({ auditOnly: true, forceDefectFix: true, changedFiles: [{ status: 'M', path: 'erun-common/thing.go' }] }),
    io({}),
  );
  assert.equal(uncovered.classification, 'uncovered');
  assert.equal(uncovered.failures.length, 1);

  const covered = evaluateRegressionCoverage(
    change({
      auditOnly: true,
      forceDefectFix: true,
      changedFiles: [
        { status: 'M', path: 'erun-common/thing.go' },
        { status: 'M', path: 'erun-integration/thing_test.go' },
      ],
    }),
    io({}),
  );
  assert.equal(covered.classification, 'covered-undeclared');
  assert.deepEqual(covered.failures, []);
  // Audit mode must not overclaim: carrying a test is not the same as
  // reproducing the reported failure, and the output has to say so.
  assert.match(covered.notes.join(' '), /cannot tell whether any of them reproduces the reported failure mode/);
});

test('an empty range is not a failure', () => {
  const result = evaluateRegressionCoverage(change({ commits: [], changedFiles: [] }), io({}));
  assert.equal(result.classification, 'empty');
  assert.deepEqual(result.failures, []);
});
