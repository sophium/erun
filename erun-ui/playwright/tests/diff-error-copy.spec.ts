import { expect, test } from '../fixtures/erunApp.js';

// DiffErrorAlert is the alert the review panel renders when LoadDiff fails
// — in particular when the in-cluster MCP port-forward is unreachable. The
// alert wraps the technical error message and exposes a Copy button so the
// user can grab the verbatim text for an issue or chat. Before the fix the
// technical line was `truncate`d to one row with an ellipsis, which both
// hid the error and made cmd+c stop at the visible characters.
//
// Staging a real ERUN_MCP_UNREACHABLE diff-load failure requires a deployed
// runtime whose MCP pod is gone but whose env config still points at a
// remote endpoint, and the headless harness reflects the developer's real
// `~/.erun/` rather than a curated fixture — same constraint that AGENTS.md
// records for #331's AutoStart prompt. So this spec covers the reachable
// negative invariant only: the "Copy error message" button must not leak
// into a healthy review surface.
//
// The positive path — that DiffErrorAlert renders with a wrapping technical
// line and a working Copy button — is covered by the Go-side error wrapping
// (`TestLoadDiffReturnsUnreachableWhenPortClosed`,
// `TestLoadDiffWrapsDialFailureAsUnreachable` in `erun-ui/app_test.go`) plus
// visual review of the surface during PR sign-off.

test.describe('DiffErrorAlert', () => {
  test('Copy error message button does not leak into the healthy state', async ({ page }) => {
    await expect(page.getByRole('button', { name: 'Copy error message' })).toHaveCount(0);
    await expect(page.getByRole('alert').filter({ hasText: 'Cannot reach' })).toHaveCount(0);
  });
});
