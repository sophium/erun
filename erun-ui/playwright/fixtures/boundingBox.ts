import { expect, type Locator } from '@playwright/test';

// Geometry helper for the layout specs. boundingBox() is nullable, and
// narrowing it inside a test body is a conditional — which the flake rules ban,
// because a spec that returns early on a null box silently asserts nothing. The
// check lives here instead, so a missing box fails the spec by name.

export interface ElementBox {
  x: number;
  y: number;
  width: number;
  height: number;
}

export async function boundingBoxOf(locator: Locator, label: string): Promise<ElementBox> {
  const box = await locator.boundingBox();
  expect(box, `${label} has no bounding box`).not.toBeNull();
  if (box === null) {
    throw new Error(`${label} has no bounding box`);
  }
  return box;
}

// Guards the dialog-card overflow regression (erun-kit's DialogContent):
// with no explicit grid-template-columns, the browser sizes the implicit
// single column to the max-content width of its widest descendant — an
// unbroken string with no min-w-0 anywhere above it forces that column, and
// therefore every sibling grid item, wider than the card's own box. The
// card's background/border still paint at the correct (clamped) width, so the
// break is visible only by measuring descendants against it, never by
// measuring the card alone.
export interface OverflowingDescendant {
  tag: string;
  dataSlot: string | null;
  overflowRight: number;
  rectWidth: number;
  cardWidth: number;
}

async function overflowingDescendants(card: Locator): Promise<OverflowingDescendant[]> {
  return card.evaluate((cardEl) => {
    const cardRect = cardEl.getBoundingClientRect();
    const entries: OverflowingDescendant[] = [];
    const walker = document.createTreeWalker(cardEl, NodeFilter.SHOW_ELEMENT);
    let node: Node | null = walker.currentNode;
    while (node) {
      const el = node as Element;
      const rect = el.getBoundingClientRect();
      // 1px tolerance absorbs sub-pixel layout rounding, not the regression.
      if (rect.width > 0 && rect.height > 0 && rect.right - cardRect.right > 1) {
        entries.push({
          tag: el.tagName,
          dataSlot: el.getAttribute('data-slot'),
          overflowRight: rect.right - cardRect.right,
          rectWidth: rect.width,
          cardWidth: cardRect.width,
        });
      }
      node = walker.nextNode();
    }
    return entries;
  });
}

export async function expectDialogContentStaysWithinCard(
  card: Locator,
  label: string,
): Promise<void> {
  const entries = await overflowingDescendants(card);
  // One blown-out grid track drags every descendant with it, so a raw dump is
  // hundreds of near-identical entries and the actual culprit is invisible.
  // Name the widest few, which are the ones sizing the track.
  const worst = [...entries]
    .sort((a, b) => b.rectWidth - a.rectWidth)
    .slice(0, 5)
    .map(
      (e) =>
        `${e.tag}${e.dataSlot ? `[${e.dataSlot}]` : ''} ${e.rectWidth.toFixed(0)}px ` +
        `in a ${e.cardWidth.toFixed(0)}px card (${e.overflowRight.toFixed(0)}px past its right edge)`,
    );
  expect(
    entries,
    `${label}: ${entries.length} descendant(s) render wider than the card. Widest:\n  ` +
      worst.join('\n  '),
  ).toEqual([]);
}
