import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';

function centre(box: { x: number; y: number; width: number; height: number } | null): {
  x: number;
  y: number;
} {
  return { x: (box?.x ?? 0) + (box?.width ?? 0) / 2, y: (box?.y ?? 0) + (box?.height ?? 0) / 2 };
}

// Leaving a trigger arms Radix's pointer-in-transit latch, which can refuse a
// single-pointermove arrival on the adjacent shortcut. The first shortcut is the
// control, so a failure on the second is the handover.
test('a group shortcut’s tooltip opens on a single-pointermove arrival', async ({ app }) => {
  await app.titlebar.openWhipPanel();

  const shortcut = (label: string) =>
    app.titlebar.whipPanel().getByRole('button', { name: label, exact: true });
  const tooltip = (label: string) => app.page.getByRole('tooltip', { name: label, exact: true });

  const firstBox = await shortcut('Select all orchestrators').boundingBox();
  const secondBox = await shortcut('Select all environments').boundingBox();
  expect(firstBox).not.toBeNull();
  expect(secondBox).not.toBeNull();
  const first = centre(firstBox);
  const second = centre(secondBox);

  // Reach each shortcut in one pointermove, like a fast flick.
  await app.page.mouse.move(0, 0);
  await app.page.mouse.move(first.x, first.y, { steps: 1 });
  await expect(tooltip('Select all orchestrators')).toBeVisible();

  await app.page.mouse.move(0, 0);
  await app.page.mouse.move(second.x, second.y, { steps: 1 });
  await expect(tooltip('Select all environments')).toBeVisible();

  // The handover leaves exactly one label once the departing one animates out.
  await expect.poll(() => app.page.getByRole('tooltip').count(), withTestBudget()).toBe(1);
});
