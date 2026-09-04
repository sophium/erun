#!/usr/bin/env node
// check-regression-coverage.mjs is the repo-state structural gate for root
// AGENTS.md § "A Defect Fix Names Its Reproduction": a change that fixes a
// reported defect must, in the same change, carry a test that reproduces the
// failure mode the report described -- or say, explicitly and in a reviewable
// place, why it cannot.
//
// The defect this exists to prevent is not "a fix with no tests". It is the
// narrower and much more common shape found by auditing closed issues: the
// fix is correct, the neighbouring states all get tests, and the one state
// the report actually described gets none. A test that merely exercises the
// changed function is not the same as a test that reproduces the reported
// failure, and only the second one stops the defect coming back.
//
// What is mechanically decidable, and what is not
// -----------------------------------------------
// Decidable, and therefore checked here:
//   - whether a change adds or modifies any test at all;
//   - whether the change names a specific test case as its reproduction;
//   - whether that named case resolves -- the file exists, the file is a
//     test file, the file is part of this change's own diff, and the named
//     case string is really in it;
//   - whether the failure mode is stated in prose at all, and whether that
//     prose says something beyond generic fix vocabulary;
//   - whether an exemption names a kind from a closed set, and -- for the
//     two kinds that are machine-verifiable -- whether the change actually
//     has that shape.
//
// NOT decidable, and deliberately not faked with a check that would be wrong
// most of the time: whether the named test genuinely reproduces the reported
// failure mode. No static analysis can read an issue report and judge that.
// What this gate buys is that the claim is *named, specific, resolvable and
// impossible to make by silence* -- so a reviewer answers one bounded
// question ("does that case reproduce what the report described?") instead of
// having to notice the absence of something. Silence is what let three
// independent instances of this ship; a wrong named claim is a review
// finding, which is a strictly better failure mode.
//
// Why this cannot be trivially satisfied by an unrelated test: the trailer
// must name `<path>::<case>`, that path must appear in this change's own
// diff, and the case string must be in the file. Pointing at some pre-existing
// test elsewhere in the repo fails the diff check; pointing at a test you did
// add but that has nothing to do with the report is a visible, attributable
// claim in the commit message that a reviewer reads, not an omission nobody
// sees.
//
// Why this is not in check-gate: it reads git history, and check-gate runs
// inside the erun-devops image test stage's Docker build context, which has
// no `.git`. It runs in `make fast-check` instead -- which root AGENTS.md
// already requires before every push, i.e. exactly at closing time -- and in
// the erun-merge skill's own pre-push rung. The pure classifier below is
// unit-tested in check-regression-coverage.test.mjs, which `test-frontend`
// runs inside check-gate, so the enforcement *logic* is gated even though the
// git-dependent invocation is not. That split is the same one
// erun-integration's structural gates use (classifier unit-tested on
// synthetic data; wiring supplies the real repo state).

import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = dirname(dirname(fileURLToPath(import.meta.url)));

// The closed set of reasons a defect fix may carry no reproduction. Each
// entry says whether the claim can be verified mechanically; `verify` is null
// for the kinds that cannot be, and those are surfaced in the output as an
// explicit reviewer-facing claim rather than checked.
export const exemptionKinds = {
  'docs-only': {
    description: 'the change touches documentation and nothing executable',
    verify: (change) => {
      const executable = change.changedFiles.filter((f) => !isDocumentationPath(f.path));
      if (executable.length === 0) return null;
      return `claims docs-only but changes ${executable.length} non-documentation file(s), e.g. ${executable[0].path}`;
    },
  },
  revert: {
    description: 'the change reverts a commit whose own regression coverage still stands',
    verify: (change) => {
      const reverts = change.commits.some((c) => /^This reverts commit [0-9a-f]{7,40}\b/m.test(c.body || ''));
      if (reverts) return null;
      return 'claims revert but no commit in the range carries a "This reverts commit <sha>" line';
    },
  },
  'covered-by-existing': {
    description: 'an existing test already reproduces the reported failure mode',
    // Verified indirectly: this kind additionally requires a
    // Regression-Test-Existing trailer, resolved the same way a
    // Regression-Test trailer is, minus the in-this-diff requirement.
    verify: null,
  },
  'no-reproducible-failure': {
    description: 'the reported failure cannot be reproduced by any test this repo can run',
    // Not machine-verifiable by construction -- this is the honest escape for
    // a real case (a fix whose failure needs hardware, a third-party outage,
    // or a human-perceptual judgement). It costs the author a written reason
    // that a reviewer reads, which is the point.
    verify: null,
  },
};

const testFilePatterns = [
  /(^|\/)[^/]+_test\.go$/,
  /(^|\/)[^/]+\.(spec|test)\.(ts|tsx|js|jsx|mjs|cjs)$/,
  /(^|\/)[^/]+[_-]test\.(sh|mjs|py|sql)$/,
  /(^|\/)[^/]+[_-]tests?\.sh$/,
  /(^|\/)conftest\.py$/,
];

export function isTestFile(path) {
  return testFilePatterns.some((re) => re.test(path));
}

// isDocumentationPath is deliberately tight: prose and images only. An
// `erun-docs/` config file or a chart's YAML is not documentation for the
// purposes of the docs-only exemption, because either can change behaviour.
export function isDocumentationPath(path) {
  return /\.(md|mdx|txt|png|jpe?g|svg|gif|webp)$/i.test(path);
}

// Generic fix/test vocabulary carries no information about which failure was
// reproduced, so it does not count toward the substance bar below.
const genericWords = new Set(
  (
    'a an and are as at be been bug bugs but by can case cases defect defects did do does fail failed failing fails ' +
    'fix fixed fixes fixing for from get gets had has have in is issue issues it its make makes not of on or problem ' +
    'regression regressions report reported reports should so test tested testing tests that the their then there ' +
    'these this to was were when which why will with work works working wrong'
  ).split(' '),
);

// statementIsSubstantive is the bar a "Reproduces:" line or an exemption
// reason has to clear. It cannot judge truth; it rejects the boilerplate that
// makes a required field a formality ("fixes the bug", "regression test
// added") while accepting any real sentence about a failure.
export function statementIsSubstantive(statement) {
  const text = (statement || '').trim();
  if (text.length < 30) return { ok: false, reason: `too short (${text.length} chars, need at least 30)` };
  const words = text.toLowerCase().match(/[a-z0-9_.[\]/-]+/g) || [];
  if (words.length < 5) return { ok: false, reason: `too few words (${words.length}, need at least 5)` };
  const specific = new Set(words.filter((w) => !genericWords.has(w) && w.length > 1));
  if (specific.size < 3) {
    return {
      ok: false,
      reason: `says nothing specific -- ${specific.size} non-generic word(s), need at least 3. Name the state, the input and the wrong output, not "fixes the bug"`,
    };
  }
  return { ok: true };
}

const trailerPattern = /^(Regression-Test|Regression-Test-Exemption|Regression-Test-Existing|Reproduces|Defect-Fix):[ \t]*(.*)$/;

// parseTrailers collects the declaration trailers from every commit in the
// range, not only the last one: a branch may state its reproduction in the
// commit that adds the test rather than in whichever commit happens to be on
// top when the branch is pushed.
export function parseTrailers(commits) {
  const trailers = {};
  for (const commit of commits) {
    const text = `${commit.subject || ''}\n${commit.body || ''}`;
    for (const line of text.split('\n')) {
      const m = trailerPattern.exec(line.trim());
      if (!m) continue;
      const key = m[1];
      const value = m[2].trim();
      if (!value) continue;
      (trailers[key] ||= []).push(value);
    }
  }
  return trailers;
}

// isDefectFix decides whether this change is in scope. The branch-name
// prefix is the repo's own documented convention (root AGENTS.md § "Branching
// Strategy": `bug/<issue>-...` for a fix), so it needs no network and no
// issue-tracker read. `Defect-Fix: yes` is the opt-in for a change that fixes
// a reported defect from a branch not named that way.
export function isDefectFix(change) {
  if (typeof change.forceDefectFix === 'boolean') return change.forceDefectFix;
  const trailers = parseTrailers(change.commits);
  if ((trailers['Defect-Fix'] || []).some((v) => /^(yes|true)$/i.test(v))) return true;
  if ((trailers['Defect-Fix'] || []).some((v) => /^(no|false)$/i.test(v))) return false;
  return /^bug\//.test(change.branchName || '');
}

function resolveNamedCase(spec, change, io, { requireInDiff }) {
  const sep = spec.lastIndexOf('::');
  if (sep < 0) {
    return `"${spec}" is not in the required <path>::<case> form (e.g. erun-integration/push_test.go::real_run_promote_refuses_when_the_registry_lacks_the_fingerprint)`;
  }
  const path = spec.slice(0, sep).trim();
  const testCase = spec.slice(sep + 2).trim();
  if (!path || testCase.length < 3) {
    return `"${spec}" must name both a path and a test case of at least 3 characters`;
  }
  if (!isTestFile(path)) {
    return `"${path}" is not a recognised test file (expected *_test.go, *.spec.ts/tsx, *.test.ts/tsx/mjs, *_test.sh, ...)`;
  }
  if (!io.fileExists(path)) {
    return `"${path}" does not exist in the working tree`;
  }
  if (requireInDiff) {
    const touched = change.changedFiles.find((f) => f.path === path && (f.status === 'A' || f.status === 'M'));
    if (!touched) {
      return `"${path}" is not added or modified by this change -- a reproduction has to be written or extended here, not pointed at somewhere else in the repo. Use "Regression-Test: none" with "Regression-Test-Exemption: covered-by-existing: <reason>" if an existing test really does reproduce the reported failure.`;
    }
  }
  if (!io.readFile(path).includes(testCase)) {
    return `"${path}" does not contain the case "${testCase}"`;
  }
  return null;
}

// evaluateRegressionCoverage is the whole classifier. `change` carries the
// facts a caller reads from git; `io` carries the two filesystem reads, both
// injected so the unit tests never touch the real tree.
export function evaluateRegressionCoverage(change, io) {
  const failures = [];
  const notes = [];
  const defectFix = isDefectFix(change);
  const touchedTests = change.changedFiles.filter((f) => isTestFile(f.path) && (f.status === 'A' || f.status === 'M'));

  if (!defectFix) {
    return {
      defectFix: false,
      classification: 'not-a-defect-fix',
      failures: [],
      notes: [
        'Not a defect fix (branch is not bug/… and no "Defect-Fix: yes" trailer), so no reproduction is required. Add "Defect-Fix: yes" to a commit if this change does fix a reported defect.',
      ],
    };
  }

  if (change.commits.length === 0) {
    return {
      defectFix: true,
      classification: 'empty',
      failures: [],
      notes: ['No commits in the range yet -- nothing to check.'],
    };
  }

  // Audit mode answers only the question a pre-convention commit can answer:
  // does the change carry any test at all? It exists so this gate can be run
  // against real history, where no declaration trailer could possibly exist.
  if (change.auditOnly) {
    if (touchedTests.length === 0) {
      return {
        defectFix: true,
        classification: 'uncovered',
        failures: ['This defect fix adds or modifies no test file at all.'],
        notes: [],
      };
    }
    return {
      defectFix: true,
      classification: 'covered-undeclared',
      failures: [],
      notes: [
        `Adds or modifies ${touchedTests.length} test file(s): ${touchedTests.map((f) => f.path).join(', ')}.`,
        'Audit mode cannot tell whether any of them reproduces the reported failure mode -- that is what the Regression-Test/Reproduces trailers exist to state.',
      ],
    };
  }

  const trailers = parseTrailers(change.commits);
  const declared = trailers['Regression-Test'] || [];

  if (declared.length === 0) {
    failures.push(
      'No "Regression-Test:" trailer on any commit in this range. A defect fix has to name the case that reproduces the reported failure, or say why it cannot. See root AGENTS.md § "A Defect Fix Names Its Reproduction".',
    );
    if (touchedTests.length > 0) {
      notes.push(
        `This change does touch ${touchedTests.length} test file(s) (${touchedTests.map((f) => f.path).join(', ')}) -- naming which case is the reproduction is the missing half.`,
      );
    }
    return { defectFix: true, classification: 'undeclared', failures, notes };
  }

  const exemptClaims = declared.filter((v) => /^none$/i.test(v));
  const namedClaims = declared.filter((v) => !/^none$/i.test(v));

  if (exemptClaims.length > 0 && namedClaims.length > 0) {
    failures.push('This range declares both "Regression-Test: none" and a named regression test. Pick one.');
    return { defectFix: true, classification: 'contradictory', failures, notes };
  }

  if (exemptClaims.length > 0) {
    return evaluateExemption(change, io, trailers, failures, notes);
  }

  const reproduces = (trailers['Reproduces'] || []).join(' ');
  if (!reproduces) {
    failures.push(
      'A named "Regression-Test:" needs a "Reproduces:" trailer stating, in one line, the failure the report described -- the state, the input, and the wrong behaviour.',
    );
  } else {
    const substance = statementIsSubstantive(reproduces);
    if (!substance.ok) failures.push(`"Reproduces:" ${substance.reason}.`);
  }

  for (const spec of namedClaims) {
    const problem = resolveNamedCase(spec, change, io, { requireInDiff: true });
    if (problem) failures.push(`Regression-Test: ${problem}`);
  }

  if (failures.length > 0) {
    return { defectFix: true, classification: 'declared-invalid', failures, notes };
  }
  notes.push(`Reproduction declared: ${namedClaims.join(', ')}`);
  notes.push(
    'This gate confirms the case exists, is a test, and is part of this change. Whether it reproduces the reported failure is a review judgement -- read the "Reproduces:" line against the issue.',
  );
  return { defectFix: true, classification: 'declared', failures: [], notes };
}

function evaluateExemption(change, io, trailers, failures, notes) {
  const raw = (trailers['Regression-Test-Exemption'] || [])[0];
  if (!raw) {
    failures.push(
      `"Regression-Test: none" needs a "Regression-Test-Exemption: <kind>: <reason>" trailer. Kinds: ${Object.keys(exemptionKinds).join(', ')}.`,
    );
    return { defectFix: true, classification: 'exempt-invalid', failures, notes };
  }
  const sep = raw.indexOf(':');
  const kind = (sep < 0 ? raw : raw.slice(0, sep)).trim();
  const reason = sep < 0 ? '' : raw.slice(sep + 1).trim();
  const spec = exemptionKinds[kind];
  if (!spec) {
    failures.push(`Unknown exemption kind "${kind}". Kinds: ${Object.keys(exemptionKinds).join(', ')}.`);
    return { defectFix: true, classification: 'exempt-invalid', failures, notes };
  }
  const substance = statementIsSubstantive(reason);
  if (!substance.ok) failures.push(`Exemption reason ${substance.reason}.`);

  if (spec.verify) {
    const problem = spec.verify(change);
    if (problem) failures.push(`Exemption ${problem}.`);
  }

  if (kind === 'covered-by-existing') {
    const existing = trailers['Regression-Test-Existing'] || [];
    if (existing.length === 0) {
      failures.push(
        '"covered-by-existing" needs a "Regression-Test-Existing: <path>::<case>" trailer naming the test that already reproduces the reported failure.',
      );
    }
    for (const value of existing) {
      const problem = resolveNamedCase(value, change, io, { requireInDiff: false });
      if (problem) failures.push(`Regression-Test-Existing: ${problem}`);
    }
  }

  if (failures.length > 0) {
    return { defectFix: true, classification: 'exempt-invalid', failures, notes };
  }
  notes.push(`Exempt: ${kind} -- ${reason}`);
  if (!spec.verify) {
    notes.push(`"${kind}" cannot be machine-verified. It is a claim a reviewer has to accept or reject.`);
  }
  return { defectFix: true, classification: 'exempt', failures: [], notes };
}

// --- git plumbing -----------------------------------------------------

function git(args) {
  return execFileSync('git', args, { cwd: repoRoot, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
}

function resolveBase(explicit) {
  if (explicit) return explicit;
  for (const ref of ['origin/main', 'main']) {
    try {
      return git(['merge-base', ref, 'HEAD']).trim();
    } catch {
      /* try the next candidate */
    }
  }
  throw new Error('cannot resolve a base commit: neither origin/main nor main is reachable. Pass --base <ref>.');
}

export function readChangeFromGit({ base, head = 'HEAD', forceDefectFix, auditOnly }) {
  const baseRef = resolveBase(base);
  // ASCII unit/record separators: a commit body is arbitrary multi-line text,
  // so no printable delimiter is safe to split on.
  const FS = '\x1f';
  const RS = '\x1e';
  const log = git(['log', '--no-merges', `--format=%H${FS}%s${FS}%b${RS}`, `${baseRef}..${head}`]);
  const commits = log
    .split(RS)
    .map((chunk) => chunk.trim())
    .filter(Boolean)
    .map((chunk) => {
      const [sha, subject, ...rest] = chunk.split(FS);
      return { sha, subject: subject || '', body: rest.join(FS) || '' };
    });
  const nameStatus = git(['diff', '--name-status', '--find-renames', `${baseRef}`, head]);
  const changedFiles = nameStatus
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const parts = line.split('\t');
      // A rename reports "R100\told\tnew"; the new path is the one that
      // exists now, and a rename counts as a modification of it.
      const status = parts[0].startsWith('R') ? 'M' : parts[0][0];
      return { status, path: parts[parts.length - 1] };
    });
  let branchName = '';
  try {
    branchName = git(['rev-parse', '--abbrev-ref', 'HEAD']).trim();
  } catch {
    /* detached HEAD: fall through to the trailer-based signal */
  }
  return { base: baseRef, head, branchName, commits, changedFiles, forceDefectFix, auditOnly };
}

function isWorkingTreeDirty() {
  try {
    return git(['status', '--porcelain']).trim().length > 0;
  } catch {
    return false;
  }
}

const realIO = {
  fileExists: (path) => existsSync(join(repoRoot, path)),
  readFile: (path) => readFileSync(join(repoRoot, path), 'utf8'),
};

function usage() {
  return [
    'usage: node scripts/check-regression-coverage.mjs [options]',
    '',
    '  --base <ref>     base of the range (default: merge-base with origin/main)',
    '  --head <ref>     head of the range (default: HEAD)',
    '  --defect-fix     treat the range as a defect fix regardless of branch name',
    '  --not-defect-fix treat the range as not a defect fix',
    '  --audit          diff-derived classification only (does the change carry any test?);',
    '                   for auditing history, where no declaration trailer can exist',
    '  --json           machine-readable result on stdout',
  ].join('\n');
}

function main(argv) {
  const opts = { head: 'HEAD' };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === '--base') opts.base = argv[++i];
    else if (arg === '--head') opts.head = argv[++i];
    else if (arg === '--defect-fix') opts.forceDefectFix = true;
    else if (arg === '--not-defect-fix') opts.forceDefectFix = false;
    else if (arg === '--audit') opts.auditOnly = true;
    else if (arg === '--json') opts.json = true;
    else if (arg === '--help' || arg === '-h') {
      console.log(usage());
      return 0;
    } else {
      console.error(`unknown argument: ${arg}\n${usage()}`);
      return 2;
    }
  }

  let change;
  try {
    change = readChangeFromGit(opts);
  } catch (err) {
    console.error(`regression-coverage gate: ${err.message}`);
    return 2;
  }

  const result = evaluateRegressionCoverage(change, realIO);
  if (opts.json) {
    console.log(JSON.stringify({ base: change.base, head: change.head, branch: change.branchName, ...result }, null, 2));
  } else {
    const scope = `${change.base.slice(0, 12)}..${change.head}${change.branchName ? ` (${change.branchName})` : ''}`;
    console.log(`regression-coverage gate: ${result.classification} [${scope}]`);
    for (const note of result.notes) console.log(`  note: ${note}`);
    // The declaration lives in a commit message, so uncommitted work cannot
    // carry one and is not evaluated. Say so rather than let a clean run read
    // as a verdict on a change that is not in the range yet.
    if (isWorkingTreeDirty()) {
      console.log('  note: the working tree has uncommitted changes; they are not part of this range. Re-run after committing.');
    }
    for (const failure of result.failures) console.error(`  FAIL: ${failure}`);
    if (result.failures.length > 0) {
      console.error('');
      console.error('Add to any commit in this range (git commit --amend, or a follow-up commit):');
      console.error('');
      console.error('    Reproduces: <the state, input and wrong behaviour the report described>');
      console.error('    Regression-Test: <path/to/file_test.go>::<case name>');
      console.error('');
      console.error('or, when the fix genuinely cannot carry one:');
      console.error('');
      console.error('    Regression-Test: none');
      console.error(`    Regression-Test-Exemption: <${Object.keys(exemptionKinds).join('|')}>: <why>`);
    }
  }
  return result.failures.length > 0 ? 1 : 0;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  process.exit(main(process.argv.slice(2)));
}
