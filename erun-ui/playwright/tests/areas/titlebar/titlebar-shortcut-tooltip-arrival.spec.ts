import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';

// The centre of a resolved box. `boundingBox()` is nullable, and the callers
// assert that it is not; this only keeps the arithmetic out of the test body.
function centre(box: { x: number; y: number; width: number; height: number } | null): {
  x: number;
  y: number;
} {
  return { x: (box?.x ?? 0) + (box?.width ?? 0) / 2, y: (box?.y ?? 0) + (box?.height ?? 0) / 2 };
}

// The three group shortcuts sit side by side, so an operator reaching for the
// next one is a handover rather than a fresh open -- and a quick flick reaches
// it in one pointermove.
//
// Radix's tooltip content is hoverable, and leaving a trigger arms a
// provider-wide pointer-in-transit latch while a document-level tracker is
// attached to clear it. A trigger's own pointermove is dispatched before that
// document tracker (React's delegated handler sits on the app root, below the
// document), so the arriving shortcut saw the latch still set and refused to
// open -- and the latch cleared only afterwards, on a move the operator had no
// reason to make, because the point they reached sits inside the departed
// tooltip's grace area. The tooltip did not mount slowly; it did not mount at
// all. IconTooltip carries its latch per tooltip, so the arrival opens it while
// the label stays hoverable.
//
// The control is the discriminator: the identical single move onto the FIRST
// shortcut opened its tooltip even before the change, so a failure of the
// second assertion is the handover and not a locator that resolves to nothing.
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

  // Each hop parks the pointer and then reaches the shortcut in ONE
  // pointermove -- the shape a fast flick produces, and the one that used to
  // be refused.
  await app.page.mouse.move(0, 0);
  await app.page.mouse.move(first.x, first.y, { steps: 1 });
  await expect(tooltip('Select all orchestrators')).toBeVisible();

  await app.page.mouse.move(0, 0);
  await app.page.mouse.move(second.x, second.y, { steps: 1 });
  await expect(tooltip('Select all environments')).toBeVisible();

  // Opening a tooltip dismisses whichever one was up, so the handover leaves
  // one label on screen and not two. The departing tooltip animates out first,
  // which is why this converges rather than reading the moment the arrival
  // lands -- and why a handover that left both shortcuts labelled fails here.
  await expect.poll(() => app.page.getByRole('tooltip').count(), withTestBudget()).toBe(1);
});
