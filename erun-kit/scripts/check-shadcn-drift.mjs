// `shadcn add --overwrite` runs its own dependency install as a side effect,
// and that install has been observed to prune a declared dependency
// (node_modules/radix-ui) and rewrite the root yarn.lock (dropping the exact-
// version key `yarn`'s `resolutions` field pins) -- against the real
// workspace. `yarn install --frozen-lockfile` afterwards reports
// "Already up-to-date" while the pruned package stays missing, so the drift
// check must never run that regeneration against the real tree.
//
// Instead, this script mirrors just enough of the workspace into a scratch
// copy outside the repo -- the hoisted node_modules, the root package.json
// (workspaces trimmed to this package alone) and yarn.lock, and this
// package's own source -- and runs the exact same regenerate-then-reapply
// pipeline there. `shadcn`'s install step is free to mutate the scratch
// copy's node_modules and yarn.lock; both are thrown away with the rest of
// the scratch directory once the diff is taken. The real workspace's
// node_modules and yarn.lock are never touched.
import { execFileSync } from 'node:child_process';
import { cpSync, copyFileSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const KIT_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const REPO_ROOT = path.resolve(KIT_DIR, '..');

const COMPONENTS = [
  'button',
  'card',
  'checkbox',
  'command',
  'dialog',
  'input',
  'label',
  'popover',
  'select',
  'table',
  'tabs',
  'textarea',
  'tooltip',
];

const DIFF_TARGETS = [
  'src/components/ui',
  'src/lib/utils.ts',
  'src/styles/theme.css',
  'components.json',
  'package.json',
];

const scratchRoot = mkdtempSync(path.join(tmpdir(), 'erun-kit-shadcn-check-'));
let drifted = false;

try {
  // Mirror the workspace root just enough for the package manager to resolve
  // this as the same project: hoisted node_modules (including the relative
  // `node_modules/erun-kit -> ../erun-kit` workspace symlink, which keeps
  // resolving correctly because the scratch copy of erun-kit lands at the
  // same relative position), yarn.lock, and a root package.json scoped to
  // this one workspace member so the install step never needs the sibling
  // erun-ui/frontend or erun-console checkouts to exist.
  cpSync(path.join(REPO_ROOT, 'node_modules'), path.join(scratchRoot, 'node_modules'), {
    recursive: true,
  });
  copyFileSync(path.join(REPO_ROOT, 'yarn.lock'), path.join(scratchRoot, 'yarn.lock'));
  const rootPackageJson = JSON.parse(readFileSync(path.join(REPO_ROOT, 'package.json'), 'utf8'));
  rootPackageJson.workspaces = ['erun-kit'];
  writeFileSync(path.join(scratchRoot, 'package.json'), JSON.stringify(rootPackageJson, null, 2));

  const scratchKit = path.join(scratchRoot, 'erun-kit');
  cpSync(KIT_DIR, scratchKit, {
    recursive: true,
    filter: (source) => {
      const base = path.basename(source);
      return base !== 'node_modules' && base !== 'dist';
    },
  });

  const shadcnBin = path.join(REPO_ROOT, 'node_modules', 'shadcn', 'dist', 'index.js');
  execFileSync(
    'node',
    [shadcnBin, 'add', ...COMPONENTS, '--overwrite', '--yes', '--cwd', scratchKit],
    { stdio: 'inherit' },
  );
  execFileSync('node', [path.join(scratchKit, 'scripts', 'reapply-dialog-clamp.mjs')], {
    cwd: scratchKit,
    stdio: 'inherit',
  });

  for (const target of DIFF_TARGETS) {
    try {
      execFileSync('diff', ['-ru', path.join(KIT_DIR, target), path.join(scratchKit, target)], {
        stdio: 'inherit',
      });
    } catch {
      drifted = true;
    }
  }
} finally {
  rmSync(scratchRoot, { recursive: true, force: true });
}

if (drifted) {
  console.error(
    '\nshadcn:check: the committed primitives differ from what the pinned shadcn CLI ' +
      'produces (diff above). Regenerate for real with `yarn shadcn add ' +
      `${COMPONENTS.join(' ')} --overwrite` +
      '` from erun-kit/, then commit the result.',
  );
  process.exitCode = 1;
}
