import * as fs from 'node:fs';
import * as path from 'node:path';

import { expect, test } from '@playwright/test';
import ts from 'typescript';

// A bare `envRowButton(...).waitFor({ timeout })` reads the sidebar once, so a
// reload that lands before seeding can never recover; `waitForSeededRow` retries the reload.

const PLAYWRIGHT_ROOT = path.resolve(__dirname, '..');
const REPO_ROOT = path.resolve(PLAYWRIGHT_ROOT, '..', '..');
const SCAN_DIRS = ['tests', 'fixtures', 'pages'];

// Shrink-only: `fixtures/erunApp.ts` holds the one legitimate hit (waitForSeededRow's retry body).
const envRowWaitBaseline: Record<string, number> = {
  'erun-ui/playwright/fixtures/erunApp.ts': 1,
};

interface EnvRowWaitHit {
  file: string; // path relative to the repo root, forward-slash separated
  line: number;
}

function isEnvRowButtonCall(node: ts.Node): node is ts.CallExpression {
  return (
    ts.isCallExpression(node) &&
    ts.isPropertyAccessExpression(node.expression) &&
    node.expression.name.text === 'envRowButton'
  );
}

function objectLiteralHasTimeoutProperty(objectLiteral: ts.ObjectLiteralExpression): boolean {
  return objectLiteral.properties.some(
    (property) =>
      property.name && ts.isIdentifier(property.name) && property.name.text === 'timeout',
  );
}

// Deliberately ignores surrounding statements; the baseline carries waitForSeededRow's exception.
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

// A baselined file absent from this checkout (narrowed build context) is skipped.
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

test.describe('env row wait baseline', () => {
  test('no file exceeds its envRowWaitBaseline entry', () => {
    expect(findOverages()).toEqual([]);
  });

  test('envRowWaitBaseline is current', () => {
    expect(findStaleBaselineEntries()).toEqual([]);
  });
});
