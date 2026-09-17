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

const registrationEnvironments = join(
  frontendSrc,
  'components/app/TenantDashboardPanels.RegistrationEnvironments.tsx',
);

test('the registration environments table announces its failure reasons', () => {
  // The row's failure reason is the whole payload of a failed or
  // deletion-blocked environment and appears asynchronously; rendering it as a
  // bare destructive paragraph leaves it unannounced next to a changed badge.
  const source = readFileSync(registrationEnvironments, 'utf8');

  assert.ok(
    source.includes('<InlineAlert>{environment.provisionError}</InlineAlert>'),
    'a failed environment must render its provisionError through InlineAlert',
  );
  assert.ok(
    source.includes('role="status" aria-live="polite"'),
    'a deletion-blocked environment must render its deleteError in a live status region',
  );
  assert.equal(
    source.includes('<p className="mt-1 text-xs text-destructive">'),
    false,
    'no environment failure reason may render as a bare destructive paragraph',
  );
});
