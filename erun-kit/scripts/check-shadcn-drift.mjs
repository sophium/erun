// `shadcn add --overwrite` runs its own install, which can prune dependencies and
// rewrite yarn.lock, so regeneration runs in a scratch copy, never the real tree.
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
  // The relative erun-kit workspace symlink keeps resolving because the scratch
  // copy lands at the same relative position; workspaces are trimmed to this package.
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
