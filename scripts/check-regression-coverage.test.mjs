// Self-test for check-regression-coverage.mjs, the regression-coverage gate
// for root AGENTS.md § "A Defect Fix Names Its Reproduction" (see that file's
// header for the design rationale and for what it deliberately does not try
// to decide). Run with `node --test scripts/check-regression-coverage.test.mjs`.
//
// Every case below drives the pure classifier against synthetic git facts, an
// injected filesystem and an injected git, so nothing here reads the real tree
// or runs a real git -- the same split erun-integration's structural gates use.
// The gate has to fail what it should catch and pass what it should allow, so
// both directions are asserted for every rule, not just the failing one: a gate
// that only ever fails is as useless as one that only ever passes, and the
// second half is what keeps a legitimate change (a docs fix, a revert, or an
// ordinary change on a branch that is not bug/…) from being blocked.

import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  branchNameForRef,
  defectFixScope,
  evaluateRegressionCoverage,
  exemptionKinds,
  isDocumentationPath,
  isTestFile,
  parseTrailers,
  readChangeFromGit,
  statementIsSubstantive,
} from './check-regression-coverage.mjs';

// A filesystem double: `files` maps a repo-relative path to its contents.
// `env` is the environment double, empty by default -- which is the state
// every gate target actually runs in, and therefore the default the opt-in
// gating cases below are about.
function io(files, env = {}) {
  return {
    fileExists: (path) => Object.hasOwn(files, path),
    readFile: (path) => files[path],
    getEnv: (name) => env[name] || '',
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
    ['erun-devops/terraform-erun/modules/terraform-erun-cluster-edge/tests/edge_transport_policy.tftest.hcl', true],
    ['erun-devops/terraform-erun/modules/terraform-erun-cluster-edge/main.tf', false],
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

test('defectFixScope follows the branch convention, honours the override, and records what decided it', () => {
  // Which of these answers says something about the change, and which says
  // something about the branch it was built on, is the whole difference the
  // caller has to be able to report.
  assert.deepEqual(defectFixScope(change({ branchName: 'bug/2123-thing' })), { defectFix: true, source: 'branch-name' });
  assert.deepEqual(defectFixScope(change({ branchName: 'feature/2123-thing' })), { defectFix: false, source: 'branch-name' });
  assert.deepEqual(defectFixScope(change({ branchName: 'feature/2123-thing', forceDefectFix: false })), {
    defectFix: false,
    source: 'forced',
  });
  assert.deepEqual(defectFixScope(change({ forceDefectFix: true, branchName: 'main' })), { defectFix: true, source: 'forced' });
  assert.deepEqual(
    defectFixScope(change({ branchName: 'feature/2123-thing', commits: [{ sha: '1', subject: 's', body: 'Defect-Fix: yes' }] })),
    { defectFix: true, source: 'trailer' },
  );
  assert.deepEqual(
    defectFixScope(change({ branchName: 'bug/2123-thing', commits: [{ sha: '1', subject: 's', body: 'Defect-Fix: no' }] })),
    { defectFix: false, source: 'trailer' },
  );
});

// --- which ref the scope is read from ---------------------------------

// A git double: `symbolic` maps a ref to what `rev-parse --symbolic-full-name`
// prints for it (nothing at all for a bare object name, a tag's ref, or a
// detached HEAD's literal "HEAD"), and the range facts are the synthetic ones
// the classifier tests above use. Anything else is a call this gate is not
// supposed to be making.
function gitDouble(symbolic, { commits = [], files = [] } = {}) {
  const FS = '\x1f';
  const RS = '\x1e';
  const calls = [];
  const run = (args) => {
    calls.push(args.join(' '));
    if (args[0] === 'rev-parse') return symbolic[args[args.length - 1]] ?? '';
    if (args[0] === 'log') return commits.map((c) => `${c.sha}${FS}${c.subject}${FS}${c.body || ''}${RS}`).join('');
    if (args[0] === 'diff') return files.map((f) => `${f.status}\t${f.path}`).join('\n');
    throw new Error(`unexpected git call: ${args.join(' ')}`);
  };
  return { run, calls };
}

test('the scope signal follows the ref being graded, not the branch that is checked out', () => {
  // The reported failure, in the direction the checkout hides: the same commit
  // and the same tree graded as `--head bug/124-probe` from a feature/ probe
  // checkout came back not-a-defect-fix, because the branch name read for scope
  // was HEAD's rather than the graded ref's. Graded on its own branch the
  // identical range exits 1.
  const { run } = gitDouble(
    {
      HEAD: 'refs/heads/feature/124-probe',
      'feature/124-probe': 'refs/heads/feature/124-probe',
      'bug/124-probe': 'refs/heads/bug/124-probe',
    },
    { commits: [{ sha: '78932dd1', subject: 'Land the probe fix on the integration branch', body: '' }] },
  );

  const graded = readChangeFromGit({ base: 'origin/main', head: 'bug/124-probe' }, run);
  assert.equal(graded.branchName, 'bug/124-probe');
  assert.equal(evaluateRegressionCoverage(graded, io({})).classification, 'undeclared');

  // The same range reached through the checkout is not in scope -- and now says
  // so instead of reporting a clean pass over a range it never looked at.
  const checkedOut = readChangeFromGit({ base: 'origin/main', head: 'HEAD' }, run);
  assert.equal(checkedOut.branchName, 'feature/124-probe');
  const verdict = evaluateRegressionCoverage(checkedOut, io({}));
  assert.equal(verdict.classification, 'not-a-defect-fix');
  assert.deepEqual(verdict.failures, []);
  assert.match(verdict.notes[0], /^UNCHECKED: scope came from the branch name \("feature\/124-probe"\)/);
});

test('branchNameForRef reads a branch out of the ref, and reads nothing out of the rest', () => {
  const { run } = gitDouble({
    HEAD: 'refs/heads/feature/2646-thing',
    'bug/2123-thing': 'refs/heads/bug/2123-thing',
    'origin/bug/2123-thing': 'refs/remotes/origin/bug/2123-thing',
    '9b4a3d0c': '',
    'v1.0.300': 'refs/tags/v1.0.300',
  });
  assert.equal(branchNameForRef('HEAD', run), 'feature/2646-thing');
  assert.equal(branchNameForRef('bug/2123-thing', run), 'bug/2123-thing');
  // A remote-tracking ref for a bug/… branch is that same branch.
  assert.equal(branchNameForRef('origin/bug/2123-thing', run), 'bug/2123-thing');
  // A bare commit and a tag have no branch to read, so the fallback cannot
  // apply to them however the branch convention is spelled.
  assert.equal(branchNameForRef('9b4a3d0c', run), '');
  assert.equal(branchNameForRef('v1.0.300', run), '');

  // A detached checkout prints "HEAD" for HEAD and carries no branch.
  assert.equal(branchNameForRef('HEAD', gitDouble({ HEAD: 'HEAD' }).run), '');

  // A ref that names no branch falls back to the checkout, so grading one
  // commit by SHA from the branch it belongs to keeps working -- and a detached
  // checkout grading one by SHA has nothing to fall back to and reads no branch
  // at all, which the notice above reports rather than passing over.
  const gradedBySha = gitDouble({
    HEAD: 'refs/heads/bug/124-probe',
    '9b4a3d0c': '',
  });
  assert.equal(readChangeFromGit({ base: 'origin/main', head: '9b4a3d0c' }, gradedBySha.run).branchName, 'bug/124-probe');
  const detached = gitDouble({ HEAD: 'HEAD', '9b4a3d0c': '' });
  assert.equal(readChangeFromGit({ base: 'origin/main', head: '9b4a3d0c' }, detached.run).branchName, '');

  // A ref that does not resolve is not a branch either -- git's own failure is
  // reported by the `log` that follows, not as a phantom branch name here.
  assert.equal(
    branchNameForRef('nope', () => {
      throw new Error('fatal: ambiguous argument');
    }),
    '',
  );
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

test('a range scoped by branch name reports that nothing in it was examined', () => {
  // The reported route: squashing source branches into an integration branch
  // replaces their messages, so the declarations they carried are gone from the
  // only place this gate reads. That still exits 0 -- a feature branch is out
  // of scope, and a branch-name decision is the convention every non-bug/
  // change relies on -- but it can no longer do so silently.
  const result = evaluateRegressionCoverage(change({ branchName: 'feature/124-integration' }), io({}));
  assert.equal(result.classification, 'not-a-defect-fix');
  assert.deepEqual(result.failures, []);
  assert.match(result.notes[0], /^UNCHECKED: scope came from the branch name \("feature\/124-integration"\)/);
  assert.match(result.notes[0], /no "Regression-Test:" or "Reproduces:" trailer was read/);
  assert.match(result.notes[1], /"Defect-Fix: yes"/);

  // A detached checkout is the same answer for the same reason: a branch-name
  // decision was made without a branch name, so the range is equally unexamined.
  const detached = evaluateRegressionCoverage(change({ branchName: '' }), io({}));
  assert.equal(detached.classification, 'not-a-defect-fix');
  assert.match(detached.notes[0], /^UNCHECKED: neither the graded ref nor the checkout names a branch/);
});

test('a scope decision that read a declaration is not reported as unexamined', () => {
  // The other direction: an author who says "Defect-Fix: no" has answered the
  // scope question in the range itself, and a caller who passes --not-defect-fix
  // has answered it outright. Neither leaves the gate unable to tell.
  const declaredNo = evaluateRegressionCoverage(
    change({ branchName: 'feature/124-integration', commits: [{ sha: '1', subject: 'Land it', body: 'Defect-Fix: no' }] }),
    io({}),
  );
  assert.equal(declaredNo.classification, 'not-a-defect-fix');
  assert.deepEqual(declaredNo.failures, []);
  assert.deepEqual(declaredNo.notes, ['Not a defect fix (a "Defect-Fix: no" trailer in this range), so no reproduction is required.']);

  const forced = evaluateRegressionCoverage(change({ branchName: 'feature/1-new-thing', forceDefectFix: false }), io({}));
  assert.ok(!forced.notes.some((note) => note.startsWith('UNCHECKED:')));
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

test('audit mode audits a range the branch name cannot scope', () => {
  // Audit mode asks a diff-derived question, and a range of history is usually
  // not a defect fix by convention either. Letting scope short-circuit it is
  // how `--audit` over a trailer-less range of main reported not-a-defect-fix
  // without auditing anything -- the same silent success, one mode over.
  const uncovered = evaluateRegressionCoverage(
    change({ auditOnly: true, branchName: 'feature/124-probe', changedFiles: [{ status: 'M', path: 'erun-common/thing.go' }] }),
    io({}),
  );
  assert.equal(uncovered.classification, 'uncovered');
  assert.equal(uncovered.defectFix, false);
  assert.equal(uncovered.failures.length, 1);

  const covered = evaluateRegressionCoverage(
    change({
      auditOnly: true,
      branchName: '',
      changedFiles: [
        { status: 'M', path: 'erun-common/thing.go' },
        { status: 'M', path: 'erun-integration/thing_test.go' },
      ],
    }),
    io({}),
  );
  assert.equal(covered.classification, 'covered-undeclared');
  assert.deepEqual(covered.failures, []);
});

test('an empty range is not a failure, and is settled before scope is', () => {
  const result = evaluateRegressionCoverage(change({ commits: [], changedFiles: [] }), io({}));
  assert.equal(result.classification, 'empty');
  assert.deepEqual(result.failures, []);
  assert.deepEqual(result.notes, ['No commits in the range yet -- nothing to check.']);

  // Whatever branch it was taken from: there is nothing here that could have
  // been examined, so it is not the "scope decided by convention" case either.
  const fromFeature = evaluateRegressionCoverage(
    change({ branchName: 'feature/1-thing', commits: [], changedFiles: [] }),
    io({}),
  );
  assert.equal(fromFeature.classification, 'empty');
  assert.ok(!fromFeature.notes.some((note) => note.startsWith('UNCHECKED:')));
});


// --- a declared reproduction that never ran ---------------------------
//
// The declaration gate can be satisfied perfectly by a case that no gate ever
// executes. erun-backend-api's opt-in end-to-end suites read an ERUN_E2E_*
// variable and skip when it is unset; no gate target sets one; and
// `go test ./...` prints no SKIP line at all without -v, so `make check-gate`
// reports a clean `ok` for a package whose entire database contract went
// unexercised. A fix for a database-only defect can therefore declare an
// honest reproduction, name it correctly, and ship green against a fake
// repository while the real database rejects the row.
//
// These cases pin that the gate names the hole -- and, just as hard, that
// naming it is all it does. The suites are opt-in by design, so nothing here
// may fail a build, and a case that is not gated must not be accused of being.

// The shape these suites actually take: the case calls a same-file helper,
// and the helper is where the environment check and the skip live.
const reviewsE2ESource = `package repository

import (
	"database/sql"
	"os"
	"testing"
)

func reviewsDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_REVIEWS_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_REVIEWS_DATABASE_URL to a migrated PostgreSQL")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return db, "tenant"
}

func seedReviewsUser(t *testing.T, db *sql.DB, tenantID, username string) string { return username }

func TestReviewMergeQueueIsPerRepository(t *testing.T) {
	db, tenantID := reviewsDatabase(t)
	author := seedReviewsUser(t, db, tenantID, "author")
	if author == "" {
		t.Fatal("no author")
	}
}
`;

const reviewsE2EPath = 'erun-backend/erun-backend-api/internal/repository/reviews_e2e_test.go';

function optInChange(trailers, extraFiles = []) {
  return change({
    branchName: 'bug/9999-a-build-less-merged-report',
    commits: [
      {
        sha: 'abc1234',
        subject: 'Record a review report that carries no build',
        body: [
          'Reproduces: reconcileMerged wrote no row at all for a build-less MERGED report, because every',
          'such write violated reviews_status_build_link_check and surfaced as a bare 500 INTERNAL_SERVER_ERROR',
          ...trailers,
        ].join('\n'),
      },
    ],
    changedFiles: [
      { status: 'M', path: 'erun-backend/erun-backend-api/internal/repository/reviews.go' },
      ...extraFiles,
    ],
  });
}

const reviewsE2ETrailer = `Regression-Test: ${reviewsE2EPath}::TestReviewMergeQueueIsPerRepository`;

test('a declared reproduction the opt-in gate skips is named, not left silent', () => {
  // The reproduction of the reported hole: the declaration resolves and the
  // gate exits 0, so before this the run said nothing at all about the case
  // it had just blessed. The case and its gate helper are the real shapes.
  const result = evaluateRegressionCoverage(
    optInChange([reviewsE2ETrailer], [{ status: 'M', path: reviewsE2EPath }]),
    io({ [reviewsE2EPath]: reviewsE2ESource }),
  );

  assert.equal(result.classification, 'declared');
  const caveat = result.notes.find((note) => note.startsWith('NOT RUN:'));
  assert.ok(caveat, `expected a "NOT RUN:" caveat, got notes: ${JSON.stringify(result.notes)}`);
  assert.match(caveat, /TestReviewMergeQueueIsPerRepository/);
  assert.match(caveat, /ERUN_E2E_REVIEWS_DATABASE_URL/);
});

test('naming a skipped reproduction never fails the gate', () => {
  // Visibility only. These suites are opt-in on purpose, and requiring a
  // PostgreSQL to run them is not this gate's decision to make.
  const result = evaluateRegressionCoverage(
    optInChange([reviewsE2ETrailer], [{ status: 'M', path: reviewsE2EPath }]),
    io({ [reviewsE2EPath]: reviewsE2ESource }),
  );
  assert.deepEqual(result.failures, []);
  assert.equal(result.classification, 'declared');
});

test('a gate reached through a second level of helper is still named', () => {
  const source = `package repository

import (
	"os"
	"testing"
)

func optInDatabase(t *testing.T) string {
	t.Helper()
	// Indirectly reached: the case calls fixtures, which calls this.
	if os.Getenv("ERUN_E2E_TENANTS_DATABASE_URL") == "" {
		t.Skip("opt-in: set ERUN_E2E_TENANTS_DATABASE_URL to a migrated PostgreSQL")
	}
	return "postgres://localhost/erun"
}

func fixtures(t *testing.T) string { return optInDatabase(t) }

func TestTenantRenamePersists(t *testing.T) {
	url := fixtures(t)
	_ = url
}
`;
  const path = 'erun-backend/erun-backend-api/tenants_e2e_test.go';
  const result = evaluateRegressionCoverage(
    optInChange([`Regression-Test: ${path}::TestTenantRenamePersists`], [{ status: 'M', path }]),
    io({ [path]: source }),
  );
  const caveat = result.notes.find((note) => note.startsWith('NOT RUN:'));
  assert.ok(caveat, `expected a "NOT RUN:" caveat, got notes: ${JSON.stringify(result.notes)}`);
  assert.match(caveat, /ERUN_E2E_TENANTS_DATABASE_URL/);
});

test('an opt-in suite whose variable is set is not reported as skipped', () => {
  // If the variable is set the suite really does run, so the caveat would be
  // a lie -- and a gate that cries wolf is one nobody reads.
  const result = evaluateRegressionCoverage(
    optInChange([reviewsE2ETrailer], [{ status: 'M', path: reviewsE2EPath }]),
    io({ [reviewsE2EPath]: reviewsE2ESource }, { ERUN_E2E_REVIEWS_DATABASE_URL: 'postgres://localhost/erun' }),
  );
  assert.equal(result.classification, 'declared');
  assert.ok(!result.notes.some((note) => note.startsWith('NOT RUN:')), 'a configured suite was reported as skipped');
});

test('an ordinary unit test is not accused of being opt-in gated', () => {
  const source = `package repository

import "testing"

func reviewName(t *testing.T, name string) string {
	t.Helper()
	if name == "" {
		t.Skip("a review is named at creation")
	}
	return name
}

func TestReviewNameRoundTrips(t *testing.T) {
	if reviewName(t, "widget") != "widget" {
		t.Fatal("name did not round-trip")
	}
}
`;
  const path = 'erun-backend/erun-backend-api/internal/repository/reviews_name_test.go';
  const result = evaluateRegressionCoverage(
    optInChange([`Regression-Test: ${path}::TestReviewNameRoundTrips`], [{ status: 'M', path }]),
    io({ [path]: source }),
  );
  assert.equal(result.classification, 'declared');
  assert.ok(!result.notes.some((note) => note.startsWith('NOT RUN:')), 'an ungated test was reported as skipped');
});

test('braces inside a string or a comment do not hide the gate', () => {
  // A case body that ends early reads as ungated, which is the silent
  // direction this whole change exists to remove -- so the scanner has to
  // ignore braces that are only text.
  const source = `package repository

import (
	"os"
	"testing"
)

func thingsDatabase(t *testing.T) string {
	t.Helper()
	if os.Getenv("ERUN_E2E_THINGS_DATABASE_URL") == "" {
		t.Skip("opt-in: set ERUN_E2E_THINGS_DATABASE_URL to a migrated PostgreSQL")
	}
	return "postgres://localhost/erun"
}

func TestThingRoundTrips(t *testing.T) {
	label := "a } brace in a string"
	// } a brace in a line comment
	/* } and one in a block comment */
	want := struct{ Name string }{Name: label}
	url := thingsDatabase(t)
	_ = url
	_ = want
}
`;
  const path = 'erun-backend/erun-backend-api/things_e2e_test.go';
  const result = evaluateRegressionCoverage(
    optInChange([`Regression-Test: ${path}::TestThingRoundTrips`], [{ status: 'M', path }]),
    io({ [path]: source }),
  );
  const caveat = result.notes.find((note) => note.startsWith('NOT RUN:'));
  assert.ok(caveat, `the gate was hidden by a brace in text: ${JSON.stringify(result.notes)}`);
  assert.match(caveat, /ERUN_E2E_THINGS_DATABASE_URL/);
});

test('an existence-based exemption that points at a skipped case is named too', () => {
  // "covered-by-existing" is the same hole wearing a different trailer: the
  // case it defers to covers nothing if the gate never runs it.
  const path = 'erun-backend/erun-backend-api/internal/repository/reviews_e2e_test.go';
  const result = evaluateRegressionCoverage(
    optInChange(
      [
        'Regression-Test: none',
        'Regression-Test-Exemption: covered-by-existing: the same merge-queue write through a real PostgreSQL',
        `Regression-Test-Existing: ${path}::TestReviewMergeQueueIsPerRepository`,
      ],
      [{ status: 'M', path: 'erun-backend/erun-backend-api/internal/repository/reviews.go' }],
    ),
    io({ [path]: reviewsE2ESource }),
  );
  assert.equal(result.classification, 'exempt');
  const caveat = result.notes.find((note) => note.startsWith('NOT RUN:'));
  assert.ok(caveat, `expected a "NOT RUN:" caveat, got notes: ${JSON.stringify(result.notes)}`);
  assert.match(caveat, /Regression-Test-Existing/);
  assert.match(caveat, /ERUN_E2E_REVIEWS_DATABASE_URL/);
});
