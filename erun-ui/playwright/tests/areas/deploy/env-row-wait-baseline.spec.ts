import * as fs from 'node:fs';
import * as path from 'node:path';

import { expect, test } from '@playwright/test';
import ts from 'typescript';

// env-row-wait-baseline.spec.ts is a repo-state structural gate: a
// seeded-row wait shaped like
//
//   await app.reloadEnvironments();
//   await app.sidebar
//     .envRowButton(tenant, environment)
//     .waitFor({ state: 'visible', timeout: 15_000 });
//
// reads the sidebar exactly once and then waits — if that single reload lands
// before the row is seeded, or is overwritten by the backend sweep, the
// following timeout cannot recover it because there is no second read. This
// is not a "too short" timeout, it is an *unrepeated* one.
// `waitForSeededRow` (fixtures/erunApp.ts) is the fix: it puts the reload
// inside the retry, so every attempt re-reads. This gate targets the shape
// the bug takes in source — a bare `envRowButton(...).waitFor({ ...,
// timeout })` call — via the TypeScript AST rather than an enumerated list of
// phrasings, the same reasoning erun-integration's issue_reference_test.go
// and bare_required_input_test.go give for matching shape over literals: a
// grep tuned to today's exact call layout is one reflow away from missing
// the next instance.
//
// A file's count may only shrink from here, never grow: `waitForSeededRow`'s
// own implementation legitimately contains this exact shape (it is the retry
// body the whole fix is built from), so the baseline is not zero everywhere
// — it is "no more than what already exists", enforced per file the same way
// erun-integration's bareRequiredInputBaseline/issueReferenceBaseline and
// erun-backend-api's KnownUnsurfacedRoutes are.

const PLAYWRIGHT_ROOT = path.resolve(__dirname, '..');
const REPO_ROOT = path.resolve(PLAYWRIGHT_ROOT, '..', '..');
const SCAN_DIRS = ['tests', 'fixtures', 'pages'];

// envRowWaitBaseline is a shrink-only baseline (the same pattern as
// erun-integration's bareRequiredInputBaseline/issueReferenceBaseline): the
// key is the file path relative to the repo root, the value is the exact
// count of matching call sites still in that file. `fixtures/erunApp.ts`
// carries the one legitimate hit — `waitForSeededRow`'s own retry body.
// Every other file must stay at zero: any new hit there is a fresh instance
// of the unrepeated-reload bug this gate exists to catch.
const envRowWaitBaseline: Record<string, number> = {
  'erun-ui/playwright/fixtures/erunApp.ts': 1,
};

interface EnvRowWaitHit {
  file: string; // path relative to the repo root, forward-slash separated
  line: number;
}

// isEnvRowButtonCall matches `<expr>.envRowButton(...)`, regardless of the
// receiver expression, so `app.sidebar.envRowButton(...)` and any future
// wrapper around it are both caught.
function isEnvRowButtonCall(node: ts.Node): node is ts.CallExpression {
  return (
    ts.isCallExpression(node) &&
    ts.isPropertyAccessExpression(node.expression) &&
    node.expression.name.text === 'envRowButton'
  );
}

// objectLiteralHasTimeoutProperty matches shape, not phrasing: any property
// named `timeout` (declared or shorthand), regardless of the timeout value
// or the other properties alongside it (e.g. `state: 'visible'`).
function objectLiteralHasTimeoutProperty(objectLiteral: ts.ObjectLiteralExpression): boolean {
  return objectLiteral.properties.some(
    (property) =>
      property.name && ts.isIdentifier(property.name) && property.name.text === 'timeout',
  );
}

// isBareEnvRowButtonWaitFor matches `<expr>.envRowButton(...).waitFor({ ...,
// timeout: N })` — a seeded-row wait with no retry around it. `waitForSeededRow`
// puts the identical call inside an `expect(async () => { ... }).toPass(...)`
// retry; this matcher intentionally does not look at the surrounding
// statements, since the baseline (not a zero-tolerance rule) is what carries
// that one legitimate exception.
function isBareEnvRowButtonWaitFor(node: ts.Node): boolean {
  if (!ts.isCallExpression(node)) {
    return false;
  }
  if (!ts.isPropertyAccessExpression(node.expression) || node.expression.name.text !== 'waitFor') {
    return false;
  }
  if (!isEnvRowButtonCall(node.expression.expression)) {
    return false;
  }
  const [firstArg] = node.arguments;
  return (
    !!firstArg &&
    ts.isObjectLiteralExpression(firstArg) &&
    objectLiteralHasTimeoutProperty(firstArg)
  );
}

function findEnvRowWaitHitsInFile(absPath: string, relPath: string): EnvRowWaitHit[] {
  const sourceText = fs.readFileSync(absPath, 'utf8');
  const sourceFile = ts.createSourceFile(absPath, sourceText, ts.ScriptTarget.Latest, true);
  const hits: EnvRowWaitHit[] = [];

  const visit = (node: ts.Node): void => {
    if (isBareEnvRowButtonWaitFor(node)) {
      const { line } = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile));
      hits.push({ file: relPath, line: line + 1 });
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  return hits;
}

function walkTypeScriptFiles(dir: string, out: string[]): void {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const entryPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      walkTypeScriptFiles(entryPath, out);
    } else if (entry.name.endsWith('.ts')) {
      out.push(entryPath);
    }
  }
}

function findAllEnvRowWaitHits(): EnvRowWaitHit[] {
  const files: string[] = [];
  for (const dir of SCAN_DIRS) {
    const absDir = path.join(PLAYWRIGHT_ROOT, dir);
    if (fs.existsSync(absDir)) {
      walkTypeScriptFiles(absDir, files);
    }
  }
  const hits: EnvRowWaitHit[] = [];
  for (const absPath of files) {
    const relPath = path.relative(REPO_ROOT, absPath).split(path.sep).join('/');
    hits.push(...findEnvRowWaitHitsInFile(absPath, relPath));
  }
  return hits;
}

// findOverages reports one entry per hit beyond what envRowWaitBaseline
// allows for its file -- a file with no entry gets zero tolerance.
function findOverages(): string[] {
  const counts = new Map<string, number>();
  const overages: string[] = [];
  for (const hit of findAllEnvRowWaitHits()) {
    const count = (counts.get(hit.file) ?? 0) + 1;
    counts.set(hit.file, count);
    const baseline = envRowWaitBaseline[hit.file] ?? 0;
    const isOverage = count > baseline;
    const message = isOverage
      ? `${hit.file}:${hit.line}: bare envRowButton(...).waitFor({ ..., timeout }) reintroduced ` +
        `-- migrate to waitForSeededRow (see fixtures/erunApp.ts) instead of a single unrepeated reload+wait`
      : null;
    if (message) {
      overages.push(message);
    }
  }
  return overages;
}

// findStaleBaselineEntries reports a baseline entry that claims more hits
// than a file still has -- the shrink-only enforcement half of the gate. A
// baselined file absent from this checkout (a narrowed build context) is
// skipped rather than compared, the same reasoning erun-integration's own
// baseline-is-current checks use.
function findStaleBaselineEntries(): string[] {
  const counts = new Map<string, number>();
  for (const hit of findAllEnvRowWaitHits()) {
    counts.set(hit.file, (counts.get(hit.file) ?? 0) + 1);
  }
  return Object.entries(envRowWaitBaseline)
    .filter(([file]) => fs.existsSync(path.join(REPO_ROOT, file)))
    .filter(([file, baseline]) => (counts.get(file) ?? 0) < baseline)
    .map(
      ([file, baseline]) =>
        `${file}: envRowWaitBaseline claims ${baseline} hit(s) but only ${counts.get(file) ?? 0} remain -- lower the baseline entry`,
    );
}

// This gate scans source, not rendered UI, so it needs neither the app
// fixture nor the headless backend.
test.describe('env row wait baseline', () => {
  test('no file exceeds its envRowWaitBaseline entry', () => {
    expect(findOverages()).toEqual([]);
  });

  test('envRowWaitBaseline is current', () => {
    expect(findStaleBaselineEntries()).toEqual([]);
  });
});
