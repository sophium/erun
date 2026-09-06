import { expect, test } from '../../../fixtures/erunApp.js';

// The real diff-load failure this alert exists for needs live-cluster state
// the isolated harness lacks, so this spec asserts only the negative
// invariant: the Copy button must not leak into a healthy review surface.
// The positive path is covered by TestLoadDiffReturnsUnreachableWhenPortClosed
// and TestLoadDiffWrapsDialFailureAsUnreachable in erun-ui/app_test.go.

test.describe('DiffErrorAlert', () => {
  test('Copy error message button does not leak into the healthy state', async ({ page }) => {
    await expect(page.getByRole('button', { name: 'Copy error message' })).toHaveCount(0);
    await expect(page.getByRole('alert').filter({ hasText: 'Cannot reach' })).toHaveCount(0);
  });
});
