import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

import { test } from 'vitest';

// erun-ui/AGENTS.md § "Design-Language Decision Record" names two collisions
// this repository decided to close for good: a second component named
// StatusBadge that hand-rolled its own status colors, and inline failure
// surfaces that rendered without going through InlineAlert / PermissionNotice.
// These tests fail the moment either regresses,
// rather than relying on a reviewer noticing a new hand-rolled color mapping.

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
  // TenantDashboardMessage.tsx's DashboardMessage and ActivityQueueDrawer.tsx's
  // RecoveryFeedback both used to branch their own border/background/text
  // classes on a destructive flag, with no ARIA role naming the failure. Both
  // now render a refused write through InlineAlert instead. Scoped to the
  // raw Tailwind utility classes rather than the bare word
  // "destructive" so a legitimate `variant="destructive"` Button elsewhere in
  // these files would not false-positive this check.
  const offenders = [
    'components/app/TenantDashboardMessage.tsx',
    'components/app/ActivityQueueDrawer.tsx',
  ]
    .map((relPath) => join(frontendSrc, relPath))
    .filter((file) => handRolledDestructiveStyling.test(readFileSync(file, 'utf8')));

  assert.deepEqual(offenders, []);
});

// A destructive confirmation rendered inside a dialog that has its own footer
// Cancel must name its negative by the effect, not reuse the generic word: the
// confirmation's "no" and the dialog's "no" do different things (abandon this
// one action, versus discard every unsaved edit in the dialog), so two buttons
// reading "Cancel" a few pixels apart are distinguishable only by what they do.
// The job-cancel confirmation closed that collision first by labelling its
// negative "Keep running"; the unexpose confirmation was the site still
// rendering the bare word alongside the footer's Cancel.

const manageDialogComponents = join(frontendSrc, 'components/app');

function readComponent(name: string): string {
  return readFileSync(join(manageDialogComponents, name), 'utf8');
}

// Reads the rendered text of the button whose click handler is `handler`, so
// these assertions are on the label an operator reads rather than on the mere
// existence of a second button.
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
  // ManageDialogJobCancel.tsx is the precedent: same surface, same collision
  // with the dialog footer's Cancel, solved by naming the effect. Pinning both
  // keeps a future edit from re-diverging them.
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
