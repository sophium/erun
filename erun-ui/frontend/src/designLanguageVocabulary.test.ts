import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

import { test } from 'vitest';

// Pins two decisions from erun-ui/AGENTS.md's Design-Language Decision Record.

const frontendSrc = fileURLToPath(new URL('.', import.meta.url));
const erunKitSrc = join(frontendSrc, '../../../erun-kit/src');

function collectSourceFiles(root: string): string[] {
  const files: string[] = [];
  for (const name of readdirSync(root)) {
    if (name === 'node_modules' || name === 'ui') continue;
    const full = join(root, name);
    const stats = statSync(full);
    if (stats.isDirectory()) {
      files.push(...collectSourceFiles(full));
      continue;
    }
    if (/\.(ts|tsx)$/.test(name) && !name.endsWith('.test.ts')) {
      files.push(full);
    }
  }
  return files;
}

const statusBadgeDefinition =
  /\b(?:export\s+)?function\s+StatusBadge\s*\(|\bconst\s+StatusBadge\s*[:=]/;

test('StatusBadge is defined in exactly one place: erun-kit', () => {
  const definitions = [...collectSourceFiles(frontendSrc), ...collectSourceFiles(erunKitSrc)]
    .filter((file) => statusBadgeDefinition.test(readFileSync(file, 'utf8')))
    .map((file) => relative(join(frontendSrc, '../../..'), file));

  assert.deepEqual(definitions, ['erun-kit/src/components/StatusBadge.tsx']);
});

const handRolledDestructiveStyling =
  /\btext-destructive\b|\bborder-destructive\b|\bbg-destructive\b/;

test('the desktop no longer hand-rolls a destructive color mapping outside InlineAlert', () => {
  // Raw Tailwind classes, not the bare word, so `variant="destructive"` stays allowed.
  const offenders = [
    'components/app/TenantDashboardMessage.tsx',
    'components/app/ActivityQueueDrawer.tsx',
  ]
    .map((relPath) => join(frontendSrc, relPath))
    .filter((file) => handRolledDestructiveStyling.test(readFileSync(file, 'utf8')));

  assert.deepEqual(offenders, []);
});

// Inside a dialog with its own footer Cancel, a destructive confirmation names
// its negative by effect.

const manageDialogComponents = join(frontendSrc, 'components/app');

function readComponent(name: string): string {
  return readFileSync(join(manageDialogComponents, name), 'utf8');
}

function negativeLabel(source: string, handler: string): string {
  const escaped = handler.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = new RegExp(`${escaped}\\s*\\}\\}\\s*>\\s*([^<>{}]+?)\\s*<\\/Button>`).exec(source);
  assert.ok(match, `no button whose handler is ${handler} found`);
  const label = match[1];
  assert.ok(label, `the button whose handler is ${handler} carries no label`);
  return label.trim();
}

const effectNamingNegative = /^Keep \S+/;

test('the unexpose confirmation names its negative by its effect', () => {
  const label = negativeLabel(
    readComponent('ManageDialogPortsExposures.tsx'),
    'dispatch(cancelUnexposeConfirm());',
  );
  assert.notEqual(label, 'Cancel', 'the manage dialog footer already renders a Cancel');
  assert.match(label, effectNamingNegative);
  assert.equal(label, 'Keep exposed');
});

test("the unexpose confirmation's negative matches the job-cancel precedent", () => {
  const jobCancel = negativeLabel(
    readComponent('ManageDialogJobCancel.tsx'),
    'setConfirming(false);',
  );
  const unexpose = negativeLabel(
    readComponent('ManageDialogPortsExposures.tsx'),
    'dispatch(cancelUnexposeConfirm());',
  );
  assert.equal(jobCancel, 'Keep running');
  assert.match(jobCancel, effectNamingNegative);
  assert.match(unexpose, effectNamingNegative);
});
