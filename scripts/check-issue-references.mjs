#!/usr/bin/env node
// check-issue-references.mjs is the TypeScript half of the repo-state
// structural gate for root AGENTS.md § "Code Comments": a tracker reference
// must not reach a code comment, because provenance lives in the git
// history, not the source. The Go half lives in
// erun-integration/issue_reference_test.go and follows the same design;
// this file mirrors its regex shape and its shrink-only baseline pattern so
// the two halves cannot silently drift apart in what they consider a hit.
//
// Why the TypeScript compiler package instead of a plain grep or a custom
// ESLint rule: `typescript` is already a devDependency hoisted to this
// repo's root node_modules (every frontend workspace member depends on it),
// so no new tool is introduced. ts.getLeadingCommentRanges/
// getTrailingCommentRanges is the same tokenizer the compiler itself uses to
// separate comments from string/template literal content, so a reference
// quoted inside a string constant is never mistaken for a comment the way a
// line-based `#` grep would. An ESLint rule was the other option considered:
// rejected because a rule runs once per package (three separate flat
// configs to keep in sync) and ESLint has no natural mechanism for the
// cross-file shrink-only baseline this gate needs, whereas one script
// scanning all frontend source roots in a single pass gives both properties
// for free and needs no type information (a plain syntactic scan), so it
// does not depend on each package's own tsconfig/project service.
//
// Scope is comments only, not string literals or identifiers: TypeScript
// test titles are string arguments to describe/it/test, not declared names,
// and a blanket string-literal scan would flag far more legitimate content
// (URLs in fixtures, golden values) than it would catch real instances of
// this defect. Comments are exactly what the filed defect was about.

import { readFileSync, readdirSync, statSync } from 'node:fs';
import { isAbsolute, join, relative, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';

const repoRoot = dirname(dirname(fileURLToPath(import.meta.url)));

// issueReferencePattern mirrors erun-integration/issue_reference_test.go's
// issueReferencePattern exactly: a bare hash-number, an "issue #" prefix, a
// cross-repo "owner/repo#" form, or a full GitHub issue/pull URL. Group 1
// captures just the "#NNN" portion of the bare-hash alternative (JS regex
// has no lookbehind either, so the boundary character before "#" must be
// consumed by the match); every other alternative's match is already exactly
// the reference text.
export const issueReferencePattern =
  /issue\s*#\s*\d+|[\w][\w.-]*\/[\w][\w.-]*#\d+|(?:^|[^\w#])(#\d{2,6})\b|github\.com\/[\w.-]+\/[\w.-]+\/(?:issues|pull)\/\d+/i;

export function matchIssueReference(text) {
  const m = issueReferencePattern.exec(text);
  if (!m) return null;
  return m[1] || m[0];
}

const skipDirs = new Set(['.git', 'node_modules', 'vendor', 'dist', 'wailsjs', '.claude', '.claude-plugin', '.vscode', 'testdata']);

function walkSourceFiles(dir, out) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      if (skipDirs.has(entry.name)) continue;
      walkSourceFiles(join(dir, entry.name), out);
    } else if (/\.(ts|tsx)$/.test(entry.name)) {
      out.push(join(dir, entry.name));
    }
  }
}

// commentRangesIn returns every leading-comment-range text found scanning
// forward through `text`, using the same ts.getLeadingCommentRanges the
// compiler itself uses to tokenize comments -- so a "#" inside a string or
// template literal is never treated as comment content.
function commentRangesIn(text) {
  const ranges = [];
  let pos = 0;
  while (pos < text.length) {
    const found = ts.getLeadingCommentRanges(text, pos) || [];
    for (const r of found) {
      ranges.push(text.slice(r.pos, r.end));
    }
    if (found.length === 0) {
      pos++;
    } else {
      pos = found[found.length - 1].end;
    }
  }
  return ranges;
}

// findIssueReferenceHits scans every .ts/.tsx file under each given root and
// returns one hit per comment matching issueReferencePattern. File paths in
// each hit are relative to the repo root, so callers can key a baseline
// consistently regardless of which root a file was reached through.
export function findIssueReferenceHits(roots) {
  const hits = [];
  for (const root of roots) {
    const full = isAbsolute(root) ? root : join(repoRoot, root);
    let isDir;
    try {
      isDir = statSync(full).isDirectory();
    } catch {
      continue;
    }
    if (!isDir) continue;
    const files = [];
    walkSourceFiles(full, files);
    for (const file of files) {
      const text = readFileSync(file, 'utf8');
      const rel = relative(repoRoot, file).split('\\').join('/');
      for (const comment of commentRangesIn(text)) {
        const value = matchIssueReference(comment);
        if (value) {
          hits.push({ file: rel, value });
        }
      }
    }
  }
  return hits;
}

// checkBaseline applies the same shrink-only enforcement as
// TestNoIssueReferenceInCode / TestIssueReferenceBaselineIsCurrent in the Go
// gate: a file with no baseline entry gets zero tolerance for a new hit, a
// baselined file may not exceed its recorded count, and a baselined file
// whose real count has dropped below what the baseline claims must have its
// baseline entry lowered in the same change.
export function checkBaseline(hits, baseline) {
  const counts = new Map();
  const violations = [];
  for (const hit of hits) {
    const next = (counts.get(hit.file) || 0) + 1;
    counts.set(hit.file, next);
    if (next > (baseline[hit.file] || 0)) {
      violations.push(hit);
    }
  }
  const staleEntries = [];
  for (const [file, allowed] of Object.entries(baseline)) {
    const actual = counts.get(file) || 0;
    if (actual < allowed) {
      staleEntries.push({ file, allowed, actual });
    }
  }
  return { violations, staleEntries };
}

function messageFor(hit) {
  return `${hit.file}: comment contains a tracker reference "${hit.value}" -- issue references belong in the PR body, not in code (root AGENTS.md § "Code Comments")`;
}

// issueReferenceBaseline is the TypeScript-side shrink-only baseline, same
// pattern as issueReferenceBaseline in erun-integration/issue_reference_test.go:
// a record of pre-existing hits found when this gate was added, not a
// design decision -- it may only shrink.
export const issueReferenceBaseline = {
  'erun-console/src/App.tsx': 3,
  'erun-console/src/app/api/identityApi.ts': 2,
  'erun-console/src/auth/auth.ts': 1,
  'erun-console/src/auth/identity.ts': 1,
  'erun-console/src/config/ConfigView.test.tsx': 1,
  'erun-console/src/config/ConfigView.tsx': 1,
  'erun-console/src/config/platform.ts': 1,
  'erun-console/src/environments/EnvironmentsPanel.test.tsx': 1,
  'erun-console/src/environments/EnvironmentsPanel.tsx': 1,
  'erun-console/src/identity/AcceptInvitePage.tsx': 2,
  'erun-console/src/identity/InvitesPanel.tsx': 2,
  'erun-console/src/identity/OrgSettingsPanel.tsx': 1,
  'erun-console/src/identity/SmtpSettingsPanel.tsx': 1,
  'erun-console/src/identity/UsersPanel.tsx': 3,
  'erun-console/src/identity/controller.ts': 1,
  'erun-console/src/shell/AppShell.tsx': 2,
  'erun-console/src/shell/ConsoleSidebar.tsx': 1,
  'erun-console/src/shell/PreShellScreens.tsx': 1,
  'erun-console/src/shell/landingContent.ts': 1,
  'erun-console/src/shell/sections.ts': 3,
  'erun-kit/src/models/platformConfig.ts': 1,
  'erun-ui/frontend/src/app/TerminalController.ts': 3,
  'erun-ui/frontend/src/app/TerminalSessionRegistry.test.ts': 2,
  'erun-ui/frontend/src/app/TerminalSessionRegistry.ts': 1,
  'erun-ui/frontend/src/app/clipboard.test.ts': 1,
  'erun-ui/frontend/src/app/cloudProviderThunks.ts': 1,
  'erun-ui/frontend/src/app/createReviewDialogThunks.ts': 1,
  'erun-ui/frontend/src/app/diagnosticsReport.test.ts': 1,
  'erun-ui/frontend/src/app/diagnosticsReport.ts': 1,
  'erun-ui/frontend/src/app/mergeQueueThunks.ts': 1,
  'erun-ui/frontend/src/app/middleware/terminalDisplayMiddleware.ts': 1,
  'erun-ui/frontend/src/app/orchestratorBusyLabel.test.ts': 1,
  'erun-ui/frontend/src/app/orchestratorBusyLabel.ts': 2,
  'erun-ui/frontend/src/app/orchestratorBusySeed.ts': 1,
  'erun-ui/frontend/src/app/orchestratorThunks.test.ts': 1,
  'erun-ui/frontend/src/app/orchestratorThunks.ts': 1,
  'erun-ui/frontend/src/app/reconnectCopy.test.ts': 1,
  'erun-ui/frontend/src/app/reconnectCopy.ts': 2,
  'erun-ui/frontend/src/app/reviewDetailThunks.ts': 4,
  'erun-ui/frontend/src/app/reviewEnvTargets.test.ts': 1,
  'erun-ui/frontend/src/app/reviewThunks.ts': 3,
  'erun-ui/frontend/src/app/selectors.ts': 4,
  'erun-ui/frontend/src/app/sessionThunks.ts': 1,
  'erun-ui/frontend/src/app/sidebarFocus.test.ts': 1,
  'erun-ui/frontend/src/app/slices/aiActivitySlice.ts': 1,
  'erun-ui/frontend/src/app/slices/orchestratorsSlice.ts': 1,
  'erun-ui/frontend/src/app/slices/reviewSlice.errorKind.test.ts': 2,
  'erun-ui/frontend/src/app/slices/reviewSlice.test.ts': 3,
  'erun-ui/frontend/src/app/slices/reviewSlice.ts': 3,
  'erun-ui/frontend/src/app/state.ts': 3,
  'erun-ui/frontend/src/app/tenantDashboardPanels.ts': 2,
  'erun-ui/frontend/src/app/terminalFocus.test.ts': 2,
  'erun-ui/frontend/src/app/terminalFocus.ts': 2,
  'erun-ui/frontend/src/app/terminalOrigin.ts': 1,
  'erun-ui/frontend/src/app/terminalPathLinkProvider.ts': 1,
  'erun-ui/frontend/src/app/terminalPathLinks.test.ts': 1,
  'erun-ui/frontend/src/app/terminalPathLinks.ts': 1,
  'erun-ui/frontend/src/app/terminalReattachRepaint.test.ts': 2,
  'erun-ui/frontend/src/app/terminalReattachRepaint.ts': 2,
  'erun-ui/frontend/src/app/terminalUrlLinks.ts': 1,
  'erun-ui/frontend/src/app/useEnvDiffSlot.test.ts': 1,
  'erun-ui/frontend/src/app/useEnvDiffSlot.ts': 1,
  'erun-ui/frontend/src/components/app/DebugPanel.tsx': 1,
  'erun-ui/frontend/src/components/app/DiffList.CommentAction.tsx': 1,
  'erun-ui/frontend/src/components/app/DiffList.tsx': 5,
  'erun-ui/frontend/src/components/app/InlineAlert.tsx': 1,
  'erun-ui/frontend/src/components/app/ManageDialogJobsTab.tsx': 1,
  'erun-ui/frontend/src/components/app/OrchestratorDialog.Guidance.tsx': 1,
  'erun-ui/frontend/src/components/app/PlatformSignInAlert.tsx': 4,
  'erun-ui/frontend/src/components/app/ReviewDetailDialog.Comments.tsx': 4,
  'erun-ui/frontend/src/components/app/ReviewPanel.ChangedFiles.tsx': 2,
  'erun-ui/frontend/src/components/app/ReviewPanel.EnvLabel.tsx': 1,
  'erun-ui/frontend/src/components/app/ReviewPanel.tsx': 2,
  'erun-ui/frontend/src/components/app/Sidebar.EnvironmentRow.tsx': 1,
  'erun-ui/frontend/src/components/app/Sidebar.OrchestratorHoverCard.tsx': 1,
  'erun-ui/frontend/src/components/app/TenantDashboardMessage.tsx': 2,
  'erun-ui/frontend/src/components/app/TenantDashboardPanels.Reviews.tsx': 3,
  'erun-ui/frontend/src/components/app/TerminalTabStrip.tsx': 1,
  'erun-ui/frontend/src/components/app/Titlebar.Controls.tsx': 2,
  'erun-ui/frontend/src/components/app/Titlebar.Status.tsx': 3,
  'erun-ui/frontend/src/components/app/UsageMeter.helpers.test.ts': 1,
  'erun-ui/frontend/src/uiExposureTypes.ts': 1,
};

function main() {
  const roots = process.argv.slice(2);
  if (roots.length === 0) {
    console.error('usage: node scripts/check-issue-references.mjs <root> [<root> ...]');
    process.exit(2);
  }
  const hits = findIssueReferenceHits(roots);
  const { violations, staleEntries } = checkBaseline(hits, issueReferenceBaseline);

  for (const hit of violations) {
    console.error(messageFor(hit));
  }
  for (const entry of staleEntries) {
    console.error(
      `${entry.file}: issueReferenceBaseline claims ${entry.allowed} hit(s) but only ${entry.actual} remain -- lower the baseline entry in scripts/check-issue-references.mjs`,
    );
  }
  if (violations.length > 0 || staleEntries.length > 0) {
    process.exit(1);
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  main();
}
