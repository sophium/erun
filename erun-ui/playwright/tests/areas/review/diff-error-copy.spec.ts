import { expect, test } from '../../../fixtures/erunApp.js';

// The real failure needs live-cluster state, so only the negative invariant is
// asserted here; Go tests cover the positive path.

test.describe('DiffErrorAlert', () => {
  test('Copy error message button does not leak into the healthy state', async ({ page }) => {
    await expect(page.getByRole('button', { name: 'Copy error message' })).toHaveCount(0);
    await expect(page.getByRole('alert').filter({ hasText: 'Cannot reach' })).toHaveCount(0);
  });
});
